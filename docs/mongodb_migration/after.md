# New System: MongoDB-backed MetaStore

This page describes the MetaServer and Admin Console as they are at the head of PR #16 (commit `081b48f`). The matching diagrams are in [Flow Diagrams](diagrams.md).

## 1. Components

| Component | Role |
|---|---|
| MongoDB 7 (`dvfs` database) | Durable record of node membership, user placement and share grants. Written by the MetaServer; read by the MetaServer once at boot and by the Admin Console every 10 s. |
| MetaServer | Serves every request from in-memory maps. Hydrates them once from `LoadSnapshot` and persists each change as one targeted write after releasing its lock. |
| FileServer | Registers with `fs_id` (its `-id` flag) and address, retrying every 3 s; heartbeats every 5 s with the same identity. |
| Client | Unchanged: `GetRoots` at login, `Navigate` per root. |
| Admin Console | Discovers nodes from the store, so it can run on any host with database access. Changes membership only through the MetaServer's `DeregisterFileServer` RPC. |

## 2. Design rules

1. **Memory is the read path.** `GetRoots` and `Navigate` never touch the store. A store outage is invisible to users already in the index.
2. **Writes are targeted and idempotent.** One record, or one bulk batch, per change. No whole-state save exists, so a stale write cannot overwrite a newer one.
3. **Writes happen after the lock is released.** A store call is network I/O and may wait on a replica-set election. It must not sit in front of every request.
4. **Structural writes keep memory order.** `writeOrderMu` is taken before `mu` by registration, first login, share, unshare and deregistration, so a grant and its revocation cannot swap on the way to the database.
5. **Failure policy is split.** Heartbeats and stale transitions are best-effort. User assignment, shares and deregistration fail closed and roll memory back. Registration fails without rollback because the fileserver retries within seconds and every write is idempotent.
6. **Identity is the operator-assigned id.** Nodes are keyed on `fs_id`. The numeric id the console displays is allocated once per node id and never recycled.

## 3. Data model

### In the store

| Collection | Key | Fields |
|---|---|---|
| `fileservers` | `_id` = node id (`fs1`), or `addr:<address>` for pre-`fs_id` builds | `numeric_id`, `address`, `last_heartbeat_unix`, `status`, `updated_at` |
| `users` | `_id` = username | `home_node_id`, `updated_at` |
| `shares` | `_id` = `{grantee, owner, path}` | `display_name`, `created_at` |
| `counters` | `_id` = `numeric_fs_id` | `seq` |

### In memory

```go
type MetaServer struct {
    store          storage.MetaStore
    fileservers    map[uint64]*domain.FileServerInfo // numeric id -> NodeID, Address, UserCount, LastHeartbeatUnix, Status
    users          map[string]uint64                 // username -> numeric id
    shared         map[string][]SharedDirEntry       // grantee -> Owner, Path (normalised), DisplayName
    orphanedUsers  map[string]string                 // username -> node id that has no current record
    writeOrderMu   sync.Mutex                        // lock before mu; structural writers only
    allowUserPurge bool
    mu             sync.RWMutex
    // heartbeat configuration and monitor control
}
```

`UserCount` is derived at hydration and maintained in memory. It is not stored.

## 4. Flows

### 4.1 Startup and hydration

1. `cmd/metaserver/main.go` resolves `-mongo_uri`, else `MONGO_URI`. Missing: `log.Fatal`.
2. `mongostore.Open`: connect and `Ping` within the 5 s server-selection window. Failure: `log.Fatal`.
3. `EnsureIndexes`. Failure: `log.Fatal`.
4. `NewMetaServer(ctx, store)` runs `hydrate`:
   - `LoadSnapshot` reads all three collections. Failure: error, then `log.Fatal`. Starting with empty routing state would strand every user, so this is deliberately fatal.
   - Each `FileServerRecord` becomes `fileservers[numeric_id]` with `NodeID`, `Address`, `LastHeartbeatUnix` (defaulted to now if 0) and `Status` (defaulted to `healthy`).
   - Each `UserRecord` whose `HomeNodeID` has a record becomes `users[username]`. Users whose node has no record go to `orphanedUsers` and are logged.
   - `UserCount` is incremented per assignment.
   - Each `ShareRecord` is appended to `shared[grantee]`; every known user gets at least an empty slice.
   - Log line: `Recovered state: fileservers=N users=N shares=N orphaned_users=N`.
5. `SetHeartbeatConfig`, `SetAllowUserPurge`, `StartHeartbeatMonitor`, serve gRPC on `:50051`. `store.Close` runs on exit.

### 4.2 Fileserver registration

`RegisterFileServer(fs_id, address, users, shared)`:

1. Empty address: error. `nodeID = fs_id`, or `addr:<address>` if the fileserver predates `fs_id` (logged as a warning).
2. `writeOrderMu.Lock()`, then `mu.Lock()`.
3. Look the node up by `nodeID`. If unknown and `fs_id` was given, look for an `addr:`-keyed entry at this address; if found, adopt it so the numeric id and users carry over. The store rename happens in step 14.
4. **Duplicate-id guard.** If the id is known, the address differs, and the existing record is still within its heartbeat window, refuse: `fs_id "fs1" is already registered from <addr> and still sending heartbeats`. A node that moved address is accepted once its old registration has gone stale.
5. **Conflict check before any mutation.** For each incoming user: mapped to another node, error; orphaned on a different node id, error (`deregister that node before another claims the user`). Nothing has changed in memory or the store at this point.
6. Apply the adoption re-key in memory, if any.
7. **New node.** Release `mu`, call `store.UpsertFileServer` (10 s) to allocate the numeric id, re-take `mu`. If the allocated id is already held in memory, the `counters` collection is out of step with `fileservers`: remove the provisional document and return an error. Re-run the conflict check, since the lock was released; on conflict, drop the in-memory node, remove the provisional document, return an error.
8. **Commit point.** Set `NodeID`, `Address`, `LastHeartbeatUnix = now`, `Status = healthy`.
9. **Unlisted users.** Users mapped to this node that the request did not list are *retained* with a `WARN` unless the MetaServer runs with `-allow_user_purge`. The request's user list is a disk scan, and an unmounted data directory looks like "no users".
10. **Orphan recovery.** Every `orphanedUsers` entry pointing at this `nodeID` is moved back to `users`, whether or not the request named them.
11. Map each incoming user to this node, clear the shares they own from memory, and queue `AssignUsers` plus `RemoveSharesByOwner`.
12. Rebuild shares from `req.Shared` with `NormalizeSharePath`, skipping owners not on this node and self-shares, deduping on (owner, path). Queue `AddShares`.
13. Recompute `UserCount`. `mu.Unlock()`.
14. Run the queued writes in order with a 30 s budget: `RenameFileServer` (adoption only) and `UpsertFileServer` first, so later writes never reference an unknown node; then the user purge, if enabled; then `AssignUsers` and `RemoveSharesByOwner`; then `AddShares`.
15. On any write error, return `failed to persist metaserver state` and keep memory as is. The fileserver treats this as "not registered" and retries in 3 s; the writes are idempotent, so the retry converges.
16. `writeOrderMu.Unlock()` (deferred).

### 4.3 Heartbeat

`Heartbeat(fs_id, address)`:

1. `mu.Lock()`. Resolve by node id only; a real id never falls back to address matching. Unknown: `unknown file server`.
2. If the registered address differs from the sender's, refuse with `re-register`: either a second process is running with the same `-id`, or the node restarted elsewhere and must go through registration.
3. `LastHeartbeatUnix = now`, `Status = healthy`. `mu.Unlock()`.
4. `store.RecordHeartbeat` on the `w:1` handle with a 5 s timeout. Failure is logged at `WARN`; the RPC still succeeds.

### 4.4 Stale monitor

Every 5 s: `mu.Lock()`, `markStaleFileServersLocked(now)` returns the node ids that just turned stale, `mu.Unlock()`. For each, `store.SetFileServerStatus(nodeID, stale)` with a 5 s timeout, `WARN` on failure.

`Navigate` and `GetRoots` no longer flip status themselves. `isHealthyLocked` judges liveness by heartbeat age, so routing is correct without the mutation.

### 4.5 First login: `GetRoots`

1. **Fast path.** `mu.RLock()`. Known user: build roots from memory and return. No write, no exclusive lock.
2. **Slow path.** `writeOrderMu.Lock()` so a first login cannot overtake a deregistration that releases the node it is about to pick, then `mu.Lock()`, then re-check the user.
3. **Orphaned user.** Refuse: `your home file server (<node>) is currently unavailable; please try again once it is back online`. No reassignment, because that would overwrite the stored placement and strand the data on the original node.
4. Pick the healthy node with the lowest `UserCount`. None: `no healthy file server registered`.
5. `users[user] = node`, `UserCount++`, `shared[user] = []`. `mu.Unlock()`.
6. `store.AssignUser` (10 s). On failure, re-take `mu`, undo the three changes, return `failed to persist metaserver state`.
7. Return `mydrive` plus the user's shared roots.

### 4.6 `Navigate`

`mu.RLock()` only. Both users must exist; the root user's node must be present and within its heartbeat window; access is ownership or an entry in `shared[username]` with `Owner == root_user`. No store access, no status mutation.

### 4.7 Share and unshare

`RootShare`:

1. `path = NormalizeSharePath(root_path)`. `writeOrderMu.Lock()`, `mu.Lock()`.
2. Both users must exist. If `shared[share_with]` already has `(owner, path)`, return success.
3. Append the entry. `mu.Unlock()`.
4. `store.AddShare` upserts `_id = {grantee, owner, path}` (10 s). On failure, remove the entry from memory and return an error.

`RootUnshare`:

1. Normalise, lock as above, both users must exist.
2. Remove the `(owner, path)` entry from memory if present. `mu.Unlock()`.
3. `store.RemoveShare` is **always** issued. The store is the authority for revocation and the keyed delete is idempotent. Failure returns an error.

### 4.8 `DeregisterFileServer`

`DeregisterFileServer(fs_id, address)`:

1. Need `fs_id` or `address`. `writeOrderMu.Lock()`, `mu.Lock()`.
2. Resolve by node id when `fs_id` is given. An unknown id does not fall through to whichever live node holds that address. Address lookup is only for callers that predate `fs_id`.
3. Collect every user mapped to the node and every user orphaned on that node id. A node that has no record, because it was gone before the last restart, can therefore still be cleaned up by id.
4. Nothing to remove: success (idempotent).
5. Snapshot the node, the affected users and orphan markers, and a deep copy of `shared`. Delete them from memory, drop the shares they own, delete the node. `mu.Unlock()`.
6. Store writes with a 10 s budget, in this order: `RemoveUsers`, `RemoveSharesInvolving`, then `RemoveFileServer` **last**. If a later write fails, the store holds a node with fewer users, which a restart reads cleanly and a retry finishes. Removing the node first would leave users homed on a node the store no longer has.
7. On failure, restore the snapshot so memory and store agree, and return an error.

### 4.9 Admin Console: cluster refresh

`cmd/admin/main.go` resolves `-mongo_uri` or `MONGO_URI` (required), opens the store (fatal on failure), and passes it to `NewAdminServer`. `Run` keeps the same tickers: metrics every 5 s, `refreshNodes()` every 10 s, snapshot every 60 s.

`refreshNodes()`:

1. `store.LoadSnapshot` with a 10 s timeout. Failure: warning, keep the previous view.
2. Build `numericByNodeID` from the fileserver records.
3. Users: map each to the numeric id string of their home node. A user whose node has no record is logged once per (user, node) and omitted from the node view, with a hint to release them via `DELETE /api/nodes/<node id>`.
4. Nodes: keyed on the numeric id string as before, now carrying `NodeID`. Tombstoned nodes are skipped unless their heartbeat is newer than the removal. The display id is clamped so a corrupt counter cannot render `FS-0` or a negative machine name. Gist discovery still overrides the machine name when it resolves the host.
5. Prune nodes absent from the snapshot. Evaluate quota alerts.

### 4.10 Admin Console: remove node

`DELETE /api/nodes/{id}`:

1. Find the node by numeric key, display name or display id.
2. **Not in the console's list.** If a MetaServer address is configured, treat the path as a stable node id and call `DeregisterFileServer(fs_id = id)`. This is how users orphaned on a node that has no record are released; the response carries `released_only: true`. Without a configured address: 404. Gist discovery is deliberately not used for an id the console has never seen.
3. **In the list.** Resolve the MetaServer address from the flag, the environment or Gist discovery.
    - No address: proceed to hide the node, but the response says the cluster was not changed.
    - Address: `CallDeregisterFileServer(msAddr, node.NodeID, node.Address)`. On failure return 502 and leave the node visible.
4. Only after the cluster has changed, or no address was available: delete from `a.nodes`, write the tombstone, drop its users. The tombstone keeps a removed node hidden across refreshes until it heartbeats again, which means it was redeployed.

The console never writes to the store. The MetaServer owns membership.

### 4.11 Admin Console: SSH restart

Systemd mode is unchanged: `sudo -n systemctl restart dvfs-<service>`.

Binary mode for `metaserver`, `admin` and `all`:

1. Source `~/.profile` and `~/.bashrc` on the target.
2. Require `MONGO_URI` to be set there; otherwise exit 1 with a clear message instead of starting a binary that would exit a second later into a log file.
3. Kill the old process and `nohup` the binary with no URI on the command line. The binary reads `MONGO_URI` itself. Handing it the console's URI would have pointed a remote MetaServer at the console host's loopback and put credentials on a remote command line.

Fileservers are relaunched with `-id=<node.NodeID>`, shell-quoted because the id is operator-chosen, so the restarted process registers as the same node. Only an `addr:` legacy node falls back to the numeric key. Anything shown in the command history or action output passes through `redactMongoURI`.

### 4.12 Deployment

- `MONGO_URI` (default `mongodb://127.0.0.1:27017/dvfs`) in the systemd units and start scripts; `EnvironmentFile=/etc/dvfs/mongo.env` for credentials; `MONGO_DB` optional.
- Units ordered `After=mongod.service`. The admin unit no longer needs to follow the MetaServer on the same host.
- `DVFS_METASERVER_ADDR` is surfaced in the admin unit and start script so node removal can reach the MetaServer without Gist discovery.
- `FS_ID` must be unique per node; the start script warns when it falls back to `fs1`.
- CI runs the conformance suite against a real `mongo:7`.

## 5. Failure policy

| Operation | Store unavailable | Memory after failure | Caller sees |
|---|---|---|---|
| MetaServer start | Fatal | n/a | Process exits |
| `GetRoots` for a known user, `Navigate` | Not contacted | Unchanged | Normal response |
| `Heartbeat` | `WARN` | Updated | Success |
| Stale transition | `WARN` | Updated | n/a |
| `GetRoots` first login | Error | Rolled back | `failed to persist metaserver state` |
| `RootShare` | Error | Rolled back | Error |
| `RootUnshare` | Error | Entry already removed | Error; the delete is idempotent, so a retry is safe |
| `RegisterFileServer` | Error | Kept | Error; fileserver retries in 3 s |
| `DeregisterFileServer` | Error | Snapshot restored | Error; console returns 502 |
| Admin `refreshNodes` | Warning | Previous view kept | Stale but consistent console |

## 6. Lock discipline

| Path | Locks |
|---|---|
| Registration, first login, share, unshare, deregistration | `writeOrderMu`, then `mu.Lock()`, mutate, `mu.Unlock()`, store writes, `writeOrderMu.Unlock()` |
| `Navigate`, `GetRoots` for known users | `mu.RLock()` only |
| Heartbeat, stale monitor | `mu.Lock()` briefly, then a best-effort store write; never `writeOrderMu` |

Readers are therefore never blocked by a slow backend, and structural writes reach the database in the order memory applied them.

## 7. Orphaned users

A user is *orphaned* when their `users` record names a node id that has no `fileservers` record at hydration time.

| Event | Effect |
|---|---|
| Hydration finds no record for the home node | User goes to `orphanedUsers`, is logged, and is counted in `orphaned_users` |
| `GetRoots` | Refused with the node id in the message; never reassigned |
| `Navigate` | `does not exist`, since the user is not in `users` |
| A node with that id registers, with any user list, even empty | Marker cleared, user routable again |
| Another node tries to claim the user | Registration refused until the home node is deregistered |
| `DeregisterFileServer(fs_id)` for the home node | Marker cleared, user record deleted, user placed afresh on next login |

## 8. Legacy fileservers

A fileserver built before `fs_id` existed still works:

- it is keyed `addr:<address>` in memory and in the store;
- its heartbeats resolve to that key;
- when the upgraded binary first registers with a real id from the same address, the `addr:` entry is adopted and `RenameFileServer` moves the document and its users under the new id, keeping the numeric id;
- the console's restart presets fall back to the numeric key only for such nodes.

## 9. Operational notes

- There is no importer from `metaserver_state.json`. A cluster starts with an empty database and rebuilds itself: fileservers re-register and republish users and shares from their on-disk ACLs, and users without a record are placed on next login.
- The MetaServer and Admin Console refuse to start without a reachable MongoDB.
- The default `MONGO_URI` points at loopback. Production should use the replica-set URI and an `EnvironmentFile`.
- Share grants and placement are authorisation data. Never expose an unauthenticated `mongod` on the network.
