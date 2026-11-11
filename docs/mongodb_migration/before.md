# Previous System: JSON State File

This page describes the MetaServer and Admin Console as they were on `main` before PR #16 (commit `24bbfb5`, the merge base). File references are to that revision. The matching diagrams are in [Flow Diagrams](diagrams.md).

## 1. Components

| Component | Role in the routing flow |
|---|---|
| MetaServer (`internal/metaserver`) | Advisory coordinator. Keeps node membership, user placement and shared-root visibility in memory and writes all of it to `metaserver_state.json`. |
| FileServer (`internal/fileserver/msclient.go`) | Registers with the MetaServer at startup, retrying every 3 s until accepted, then heartbeats every 5 s. Identifies itself by its network address only. |
| Client | Calls `GetRoots` at login and `Navigate` to find the node that hosts a root. |
| Admin Console (`internal/admin`) | Reads `metaserver_state.json` from local disk every 10 s to discover nodes, scrapes `/metrics` every 5 s, and can remove nodes or restart services over SSH. |

Because the Admin Console discovered the cluster by reading the MetaServer's state file, both had to run on the same machine or share a filesystem.

## 2. State model

All routing state lived in one struct guarded by one `sync.RWMutex`:

```go
type MetaServer struct {
    fileservers map[uint64]*domain.FileServerInfo // numeric fs id -> node
    users       map[string]uint64                 // username -> numeric fs id
    shared      map[string][]SharedDirEntry       // username -> roots visible to them
    nextFsID    uint64
    stateFile   string
    mu          sync.RWMutex
    // heartbeat configuration and monitor control
}
```

`FileServerInfo` carried `Address`, `UserCount`, `LastHeartbeatUnix` and `Status` (`healthy` or `stale`). `SharedDirEntry` carried `Owner`, `Path` and `DisplayName`.

The persisted file mirrored the struct exactly:

```json
{
  "fileservers": {
    "0": { "address": "10.0.171.41:50052", "user_count": 2, "last_heartbeat_unix": 1758000000, "status": "healthy" }
  },
  "users": { "alice": 0, "bob": 0 },
  "shared": {
    "bob": [ { "Owner": "alice", "Path": "/alice/proj", "DisplayName": "proj" } ]
  },
  "next_fs_id": 1
}
```

Identity was positional: a node was "the entry whose `address` matches". `UserCount` was stored rather than derived.

## 3. Persistence mechanics

### 3.1 Saving

`saveStateLocked()` in `metaserver.go` serialised the entire struct to a temp file in the same directory and renamed it over `metaserver_state.json`. It was called:

- at the end of every mutating RPC: `RegisterFileServer`, `Heartbeat`, `GetRoots` (first login), `RootShare`, `RootUnshare`, `DeregisterFileServer`;
- from `Navigate` and `GetRoots` whenever their inline stale check flipped a node;
- from the heartbeat monitor whenever a node turned stale.

Every call ran while the caller held the `ms.mu` write lock. With N fileservers heartbeating every 5 s, the whole state was rewritten N times per 5 s, and every other RPC queued behind that disk I/O.

### 3.2 Loading

`NewMetaServer(stateFile)` called `loadState()`:

- missing file: start empty;
- unreadable or malformed file: **log a warning and start empty**;
- valid file: load all four maps as-is, defaulting `LastHeartbeatUnix` to now and `Status` to `healthy` where missing.

Nothing checked that a user's numeric id pointed at an existing node.

## 4. Flows

### 4.1 Startup

1. `cmd/metaserver/main.go` parses `-state_file` (default `./metaserver_state.json`).
2. `NewMetaServer` loads the file as described above.
3. `SetHeartbeatConfig` (30 s timeout, 5 s check) and `StartHeartbeatMonitor`.
4. gRPC listener on `:50051`.

### 4.2 Fileserver registration

`RegisterFileServer(address, users, shared)`, all under `ms.mu.Lock()`:

1. Reject an empty address.
2. `findFileServerByAddressLocked(address)`. Unknown address: allocate `fsID = nextFsID`, increment the counter, create the entry.
3. Set `Address`, `LastHeartbeatUnix = now`, `Status = healthy`.
4. Delete every user currently mapped to this `fsID` that the request did not list, plus their shares and the shares they own. The request's user list came from the fileserver's disk scan, so an unmounted data directory presented as "no users" and purged the mappings.
5. For each incoming user, if already mapped to a *different* node, return `User X already exists in file server: <addr>`. Earlier users in the same loop had already been remapped and the deletions from step 4 had already happened. Nothing was saved and nothing was rolled back.
6. Otherwise map the user to `fsID`.
7. Remove all shares owned by incoming users from everyone's list, then re-add them from `req.Shared`, storing `Path` as `"/" + dirPath`.
8. `UserCount = count of users mapped to fsID`.
9. `saveStateLocked()`. Failure returns `failed to persist metaserver state`.

### 4.3 Heartbeat

`Heartbeat(address)`, under `ms.mu.Lock()`:

1. Look the node up by address. Unknown: `unknown file server`.
2. Set `LastHeartbeatUnix = now`, `Status = healthy`.
3. `saveStateLocked()` (whole file). Failure fails the heartbeat.

### 4.4 Stale monitor

Every 5 s the monitor goroutine took `ms.mu.Lock()`, ran `markStaleFileServersLocked(now)` (any node with `now - LastHeartbeatUnix > 30 s` became `stale`), and if anything changed called `saveStateLocked()` before unlocking.

### 4.5 First login: `GetRoots`

`GetRoots(username)`, under `ms.mu.Lock()` (exclusive, even for the pure read case):

1. If the user is known, skip to step 6.
2. Run the stale check inline and save the file if anything flipped. A save error fails the login.
3. Pick the healthy node with the smallest `UserCount`. None: `no healthy file server registered`.
4. `users[user] = fs`, `UserCount++`, `shared[user] = []`.
5. `saveStateLocked()`. On failure return an error, but the in-memory assignment stays.
6. Return `mydrive` plus every entry in `shared[user]`.

### 4.6 `Navigate`

`Navigate(username, root_user)`, also under `ms.mu.Lock()`:

1. Stale check inline, with a file save if anything flipped.
2. Both users must exist.
3. The root user's node must be present and healthy, else `currently unavailable`.
4. Access: `username == root_user`, or `shared[username]` has an entry whose `Owner == root_user`.
5. Return the node's address.

### 4.7 Share and unshare

`RootShare(root_path, owner, share_with, name)`:

1. Both users must exist.
2. If `shared[share_with]` already has an entry with the same `Owner`, return success without adding. The path was not compared, so a second directory from the same owner to the same grantee was silently dropped.
3. Append `{Owner, Path: root_path as sent, DisplayName}` and save.

`RootUnshare(root_path, owner, unshare_with)`:

1. Both users must exist.
2. Find the entry with `Owner == owner && Path == root_path` by exact string comparison.
3. Not found: return success (silent no-op). Found: remove and save.

Registration stored paths as `/alice/proj`, `RootShare` stored whatever the fileserver sent, and `RootUnshare` compared the raw request value. After a fileserver restart, which re-registers and republishes its shares, revoking a surviving share compared `alice/proj` against `/alice/proj` and did nothing.

### 4.8 `DeregisterFileServer`

`DeregisterFileServer(address)`, under `ms.mu.Lock()`:

1. Look up by address. Unknown: success (idempotent).
2. Delete every user mapped to the node, their shares, and the shares they own.
3. Delete the node entry.
4. `saveStateLocked()`. Failure returns an error with memory already changed.

### 4.9 Admin Console: cluster refresh

`cmd/admin/main.go` took `-state_file` (default `./bin/metaserver_state.json`). `Run` started three tickers: metrics poll every 5 s, `refreshNodes()` every 10 s, telemetry snapshot every 60 s.

`refreshNodes()` in `poller.go` called `LoadMetaState(path)` in `state.go`, which opened the configured path and, if it was missing, tried `./metaserver_state.json`, `./bin/metaserver_state.json` and `../metaserver_state.json` in turn. From the parsed file it:

- rebuilt `users` (username to numeric id string);
- for each fileserver, skipped nodes the admin had tombstoned unless the file showed a heartbeat newer than the removal, derived the metrics URL as `port - 41000`, set display name `FS-(id+1)` and machine name `dvfs(id+1)` (overridden by Gist discovery when it resolved the host), and created or updated the `NodeState`;
- pruned nodes absent from the file;
- evaluated quota alerts.

### 4.10 Admin Console: remove node

`DELETE /api/nodes/{id}` in `handlers.go`:

1. Find the node by numeric key, display name or display id. Unknown: 404.
2. Under the console lock: delete it from `a.nodes`, write a removal tombstone, drop its users from `a.users`.
3. Resolve the MetaServer address from `-metaserver_addr`, `DVFS_METASERVER_ADDR`, or Gist discovery.
4. Call `RemoveNodeFromMetaStateFile(stateFile, fsID)` in `state.go`, which rewrote `metaserver_state.json` on disk with the node and its users removed. Any error was only logged.
5. If a MetaServer address was found, call `DeregisterFileServer(address)`. Failure was reported as `metaserver_warning` with `success: true`. If no address was found, the RPC was skipped and the response was still `success: true`.

The direct file edit raced with the MetaServer, which held the authoritative copy in memory and rewrote the whole file on its next heartbeat or mutation, undoing the console's edit. With no MetaServer address configured, the node vanished from the console while the cluster kept routing users to it.

### 4.11 Admin Console: SSH restart (binary mode)

`Orchestrator.FormatCommand` in `actions.go` generated the remote shell commands. In binary mode it passed `-state_file=<repo>/bin/metaserver_state.json` to both `bin/metaserver` and `bin/admin`, and relaunched fileservers with `-id=<fsID>` where `fsID` was the console's numeric key (`0`, `1`, ...), not the id the fileserver had actually been started with. Systemd mode ran `sudo -n systemctl restart dvfs-<service>`.

### 4.12 Deployment

- `scripts/dvfs-metaserver.service`, `scripts/dvfs-admin.service` and their `@` templates set `Environment=STATE_FILE=...`.
- `dvfs-admin.service` was ordered `After=dvfs-metaserver.service` on the same host.
- `scripts/start-metaserver.sh` created the state directory and passed `-state_file`; `scripts/start-admin.sh` passed `-state_file`.
- `Makefile` target `run-admin` passed `-state_file=./metaserver_state.json`.
- CI pinned Go 1.24 and ran `make test` with no database.

## 5. Weaknesses this design had

Each item below is tied to code on `main` and is addressed in [What Changed](changes.md).

1. **Whole-state rewrite under the global lock.** Every heartbeat from every node re-serialised all nodes, users and shares while holding `ms.mu`. Reads (`GetRoots`, `Navigate`) took the same exclusive lock and could themselves trigger a file write.
2. **Admin Console pinned to the MetaServer host.** Cluster discovery was a local file read with a path-guessing fallback.
3. **Two writers to one file.** The console's node removal edited the file directly while the MetaServer treated memory as authoritative and overwrote the file on its next save.
4. **Address as identity.** After a DHCP lease change `findFileServerByAddressLocked` missed, so the node was allocated a new id. Its users were still mapped to the old id, so that same registration failed with `already exists in file server`, and the fileserver retried forever. The phantom entry created before the check stayed in memory and was persisted by the next save.
5. **Registration conflicts left partial state.** Users were remapped and purged before the conflict check for a later user failed, with no rollback.
6. **Silent empty start.** An unreadable state file produced a warning and an empty cluster. Every user then looked new on next login and was placed on an arbitrary node while their data stayed on the original.
7. **Share path mismatch.** Three call sites disagreed on leading slashes, so unshare was a silent no-op for shares that had survived a fileserver restart.
8. **Share dedupe by owner only.** Sharing a second directory with the same person returned success and recorded nothing.
9. **Node removal reported success without changing the cluster** whenever the console had no MetaServer address.
10. **Unconditional user purge on registration.** A node whose data directory was not mounted at boot registered with an empty user list, and the MetaServer deleted every mapping for that node.
