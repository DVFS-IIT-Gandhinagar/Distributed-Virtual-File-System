# What PR #16 Changed

Diff range: `24bbfb5..081b48f` (`origin/main...shardul-mongo-integration`). 74 files, 5026 insertions, 1553 deletions.

## 1. Commits

| Commit | Summary |
|---|---|
| `77036b6` | docs: record deferred work found during the MongoDB migration |
| `e867eea` | ci: add MongoDB service for the storage backend conformance suite |
| `fb0064e` | storage: add the MetaStore persistence seam with Mongo and memory backends |
| `929b146` | metaserver: move cluster routing state from JSON to MongoDB |
| `c6141c7` | admin: read cluster state from MongoDB instead of the metaserver's state file |
| `4abc062` | docs, scripts: update deployment and documentation for MongoDB |
| `e699a32` | metaserver: clear orphan markers when the node returns, not just its users |
| `0a478f2` | docs, scripts: document fileserver deregistration and wire its metaserver address |
| `1a63dd1` | admin: keep the remove-node tests off the network |
| `490b4e9` | fix comments |
| `15b6bd7` | build: move to Go 1.27.1 and mongo-driver v2.9.1 |
| `84aef7f` | storage: add RenameFileServer and bulk writes; drop the dead user counter |
| `b681d47` | metaserver: make fs_id the only identity a live node can be matched on |
| `3121ae5` | metaserver: order first logins with deregistrations; remove the node document last |
| `bb0f375` | admin: restart fileservers under their own id; stop pushing the console's Mongo target to remote hosts |
| `66b4b9c` | deploy: make the MongoDB configuration reach the services and document the database |
| `449630c` | admin: gofmt ssh_test.go |
| `081b48f` | build: stay on Go 1.26 |

`MONGODB_MIGRATION_PLAN.md` and `docs/deferred-work.md` were added early in the series and removed again before the head commit, so they are not part of the final diff.

## 2. File map

### Added

| File | Purpose |
|---|---|
| `internal/storage/meta.go` | `MetaStore` interface, record types, `NormalizeSharePath`, `LegacyNodeIDPrefix`. |
| `internal/storage/mongo/mongo.go` | Production backend on `go.mongodb.org/mongo-driver/v2`. |
| `internal/storage/memory/memory.go` | In-process test double with the same semantics. |
| `internal/storage/storagetest/conformance.go` | Conformance suite that both backends run. |
| `internal/storage/meta_test.go`, `mongo/mongo_test.go`, `memory/memory_test.go` | Unit tests. The Mongo tests skip unless `DVFS_MONGO_TEST_URI` is set. |
| `internal/metaserver/identity_test.go` | `fs_id` keying, legacy adoption, duplicate-id refusal, address changes. |
| `internal/metaserver/writeorder_test.go` | Store writes land in memory order; first login versus deregistration ordering. |
| `internal/admin/node_identity_test.go` | Console uses `NodeID` for restart and removal. |
| `internal/admin/ssh_test.go` | Regression test over the generated restart command strings. |

### Deleted

| File | Why |
|---|---|
| `internal/admin/state.go` | `LoadMetaState` and `RemoveNodeFromMetaStateFile` are gone. The console reads the store and never edits membership itself. |
| `internal/admin/state_test.go`, `internal/admin/state_edge_test.go` | Tests for the above. |
| `metaserver_state.json` (working tree) | Nothing reads it and there is no import path. |

### Modified, by area

| Area | Files |
|---|---|
| Wire protocol | `api/metaserver/metaserver.proto` and the generated `metaserver.pb.go`, `metaserver_grpc.pb.go` |
| MetaServer | `internal/metaserver/metaserver.go`, `internal/metaserver/handler.go`, `cmd/metaserver/main.go`, tests |
| FileServer client | `internal/fileserver/msclient.go` (sends `fs_id`) |
| Domain | `internal/domain/types.go` (`FileServerInfo.NodeID`) |
| Admin Console | `internal/admin/server.go`, `poller.go`, `handlers.go`, `actions.go`, `grpc_client.go`, `metrics.go`, `snapshot.go`, `cmd/admin/main.go`, tests |
| Integration | `integration/e2e_test.go`, `internal/client/client_test.go` (construct the MetaServer with a store) |
| Build and deploy | `go.mod`, `go.sum`, `Makefile`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `scripts/*` |
| Docs | `README.md`, `docs/index.md`, `docs/architecture.md`, `docs/setup.md`, `docs/features/crash_recovery.md`, `docs/features/admin_telemetry.md`, `docs/diagrams/overview.md`, `docs/diagrams/metaserver_coordinator.md` |

## 3. The storage seam

`internal/storage/meta.go` defines the only persistence interface the MetaServer and Admin Console use. Two rules are written into its package comment and enforced by the callers:

1. `LoadSnapshot` is the only read, once at boot. Requests are served from memory.
2. Every write is a targeted, idempotent change to one record or one bulk batch. Callers write after releasing their mutex.

```go
type MetaStore interface {
    LoadSnapshot(ctx context.Context) (*MetaSnapshot, error)

    UpsertFileServer(ctx context.Context, rec FileServerRecord) (uint64, error)
    RemoveFileServer(ctx context.Context, nodeID string) error
    RenameFileServer(ctx context.Context, oldNodeID, newNodeID string) error
    RecordHeartbeat(ctx context.Context, nodeID string, at time.Time, status string) error
    SetFileServerStatus(ctx context.Context, nodeID, status string) error

    AssignUser(ctx context.Context, username, nodeID string) error
    AssignUsers(ctx context.Context, users []UserRecord) error
    RemoveUsers(ctx context.Context, usernames []string) error

    AddShare(ctx context.Context, s ShareRecord) error
    AddShares(ctx context.Context, shares []ShareRecord) error
    RemoveShare(ctx context.Context, grantee, owner, path string) error
    RemoveSharesInvolving(ctx context.Context, usernames ...string) error
    RemoveSharesByOwner(ctx context.Context, owners ...string) error

    EnsureIndexes(ctx context.Context) error
    Ping(ctx context.Context) error
    Close(ctx context.Context) error
}
```

Record types: `FileServerRecord{NodeID, NumericID, Address, LastHeartbeatUnix, Status}`, `UserRecord{Username, HomeNodeID}`, `ShareRecord{Grantee, Owner, Path, DisplayName}`. `UserCount` is not persisted anywhere; it is derived at hydration and maintained in memory.

`NormalizeSharePath` turns any of `/alice/proj`, `alice/proj/`, `alice\proj` or `./alice/proj` into `alice/proj`. Every share write and lookup goes through it.

### Backends

| Backend | Package | Used by |
|---|---|---|
| MongoDB | `internal/storage/mongo` | `cmd/metaserver`, `cmd/admin` |
| In-memory | `internal/storage/memory` | Unit and integration tests. The remove-node tests share one instance between an in-process MetaServer and the console. |

Both run `storagetest.RunConformance`, so the double cannot drift from the real backend.

## 4. MongoDB schema

Database: `-mongo_db` if given, else the database named in the URI, else `dvfs`.

| Collection | `_id` | Other fields | Indexes |
|---|---|---|---|
| `fileservers` | Node id (`fs1`), or `addr:<address>` for pre-`fs_id` nodes | `numeric_id`, `address`, `last_heartbeat_unix`, `status`, `updated_at` | `numeric_id` unique sparse; `address`; `(status, last_heartbeat_unix)` |
| `users` | Username | `home_node_id`, `updated_at` | `home_node_id` |
| `shares` | Subdocument `{grantee, owner, path}` in exactly that field order | `display_name`, `created_at` | `_id.grantee`; `_id.owner` |
| `counters` | Counter name (`numeric_fs_id`) | `seq` | none |

Two facts about this schema were verified against a live MongoDB 7 and are recorded in code comments:

- A compound subdocument `_id` is compared byte for byte, so field order is part of the key. Share ids are always built from the `shareKeyDoc` struct, never from a map.
- A unique index treats a missing field as null, so the `numeric_id` index is sparse.

Connection settings: write concern `majority` on the client, with a second handle on `fileservers` at `w:1` used only for heartbeats. Server selection timeout 5 s and per-operation timeout 3 s unless the caller's context already carries a deadline.

Numeric ids come from `FindOneAndUpdate` with `$inc` on `counters/numeric_fs_id` (upsert, return-after). The sequence is 1-based on the server and exposed 0-based, matching the old `next_fs_id` numbering so the console's `FS-1`, `FS-2` names are unchanged. Ids are never recycled, which matters because the console keys its removal tombstones on them.

## 5. MetaServer changes

### Constructor and hydration

| Aspect | Before | After |
|---|---|---|
| Constructor | `NewMetaServer(stateFile string)` | `NewMetaServer(ctx, store storage.MetaStore)` |
| Load failure | Warning, empty state | Error, `main` exits |
| Users with unknown home node | Loaded as-is, pointing at a missing id | Kept in `orphanedUsers`, not routable, never reassigned |
| `UserCount` | Read from the file | Counted from assignments |

### New fields on `MetaServer`

- `store storage.MetaStore`.
- `orphanedUsers map[string]string` (username to node id).
- `writeOrderMu sync.Mutex`: taken before `mu` by every structural writer so store writes reach the backend in the same order as the in-memory changes. Readers take only `mu`. Heartbeats skip it.
- `allowUserPurge bool`, set by the new `-allow_user_purge` flag, default `false`.

### Per-RPC changes

| RPC | Before | After |
|---|---|---|
| `RegisterFileServer` | Keyed on address. Mutated state before the conflict check. Purged unlisted users unconditionally. Whole-file save under lock. | Keyed on `fs_id`, falling back to `addr:<address>`. Adopts a legacy `addr:` entry at the same address. Refuses a second live node claiming the same id. Validates every conflict before any mutation. Allocates the numeric id through the store with the lock released. Retains unlisted users with a warning unless `-allow_user_purge`. Clears orphan markers for the node id even when the user list is empty. Writes node, then users, then shares after unlock, in bulk. No rollback on write failure: the fileserver retries and the writes are idempotent. |
| `Heartbeat` | Keyed on address. Whole-file save; failure fails the RPC. | Keyed on `fs_id` only. A heartbeat from an address other than the registered one is refused with "re-register". Single `w:1` field update after unlock; failure is logged and the RPC succeeds. |
| Stale monitor | Save under lock | Collects transitioned node ids under lock, writes `SetFileServerStatus` per node after unlock. |
| `GetRoots` | Exclusive lock always. Inline stale check with possible save. Memory kept on save failure. | Read lock for known users, no write. Slow path takes `writeOrderMu` then `mu`, refuses orphaned users, writes `AssignUser` after unlock, rolls memory back on failure. No inline stale marking. |
| `Navigate` | Exclusive lock, inline stale check with possible save. | Read lock only. No store access, no status mutation. |
| `RootShare` | Dedupe on owner. Raw path. Save under lock. | Dedupe on (owner, normalised path). `AddShare` after unlock, in-memory rollback on failure. |
| `RootUnshare` | Exact raw path match, silent no-op if absent. | Normalised path. Store delete always issued (idempotent keyed delete); error surfaced. |
| `DeregisterFileServer` | Keyed on address. Save under lock. | `fs_id` preferred, address only as a legacy fallback. Also releases users orphaned on the node id, so a node with no record can still be cleaned up. Writes users, then shares, then the node document last, with full in-memory rollback on failure. |

## 6. Wire protocol

`api/metaserver/metaserver.proto` gains one field in three messages:

| Message | New field |
|---|---|
| `RegisterFileServerRequest` | `string fs_id = 4` |
| `HeartbeatRequest` | `string fs_id = 2` |
| `DeregisterFileServerRequest` | `string fs_id = 2` |

`internal/fileserver/msclient.go` sends `fs.serverID` (the `-id` flag) in both `RegisterFileServer` and `Heartbeat`. Fileservers built before this change omit the field; the MetaServer keys them on `addr:<address>` and re-keys them to their real id the first time they register with one from the same address.

`domain.FileServerInfo` gains `NodeID string` with the JSON tag `node_id,omitempty`.

## 7. Admin Console changes

| Aspect | Before | After |
|---|---|---|
| Constructor | `NewAdminServer(stateFile, staticDir)` | `NewAdminServer(store storage.MetaStore, staticDir)` |
| Cluster discovery | `LoadMetaState` from a local file every 10 s | `store.LoadSnapshot` every 10 s with a 10 s timeout |
| `NodeState` | `FsID` (numeric key) only | Adds `NodeID` (JSON `nodeID`), exposed on `/api/cluster` |
| Users on unlisted nodes | Numeric id kept in the `users` map | Logged once per (user, node) and omitted from the node view |
| Display id | `int(fsID) + 1`, unchecked | Clamped below `MaxInt32`, floor of 1 |
| Remove node | Hide, edit the JSON file, then best-effort RPC | RPC first with `fs_id` and address; hide only after success; 502 and node left visible on failure; explicit warning when no MetaServer address is configured; unlisted ids are forwarded as `fs_id` to release orphaned users (`released_only: true`) |
| `CallDeregisterFileServer` | `(msAddr, fsAddress)` | `(msAddr, fsID, fsAddress)` |
| SSH binary restart | `-state_file=...` on metaserver and admin; fileserver `-id=<numeric key>` | Sources `~/.profile` and `~/.bashrc`, refuses to launch if `MONGO_URI` is unset, passes no URI on the command line; fileserver `-id=<node.NodeID>` shell-quoted; falls back to the numeric key only for `addr:` legacy nodes |
| Command history and display | Raw | MongoDB passwords masked by `redactMongoURI` |
| Remove-node tests | Against a temp JSON file | Shared `memory.Store` and an in-process MetaServer, with no discovery resolver so nothing dials the network |

## 8. Entry points and flags

| Binary | Removed | Added |
|---|---|---|
| `bin/metaserver` | `-state_file` | `-mongo_uri` (or `MONGO_URI`, required), `-mongo_db`, `-allow_user_purge` |
| `bin/admin` | `-state_file` | `-mongo_uri` (or `MONGO_URI`, required), `-mongo_db` |
| `bin/fileserver` | nothing | nothing new; `-id` is now the node's cluster identity and must be unique |

Both the MetaServer and the Admin Console exit at startup if no URI is given or MongoDB cannot be reached. The MetaServer also runs `EnsureIndexes` at startup.

## 9. Deployment, build and CI

- **systemd** (`scripts/dvfs-metaserver.service`, `scripts/dvfs-admin.service`, and the `@` templates): `STATE_FILE` replaced by `Environment=MONGO_URI=mongodb://127.0.0.1:27017/dvfs`; `After=` and `Wants=` include `mongod.service`; comments point to `EnvironmentFile=/etc/dvfs/mongo.env` for credentials and to `DVFS_METASERVER_ADDR`. The admin unit now runs the start script with `runuser -u` rather than a login shell so `Environment=` values reach it.
- **Start scripts**: `start-metaserver.sh` and `start-admin.sh` pass `-mongo_uri` and the optional `-mongo_db`, echo the URI with the password masked, and `start-admin.sh` echoes `DVFS_METASERVER_ADDR`. `start-fileserver.sh` warns loudly when it falls back to `FS_ID=fs1`. `rp_115/run_fileserver.sh` now requires `--id`; `rp_115/run_metaserver.sh` and `run_admin.sh` pass `-mongo_uri`.
- **Makefile**: `MONGO_URI ?= mongodb://127.0.0.1:27017/dvfs`; `run-metaserver` and `run-admin` pass it.
- **go.mod**: adds `go.mongodb.org/mongo-driver/v2 v2.9.1` and its indirect dependencies.
- **CI** (`.github/workflows/ci.yml`): `mongo:7` service container with a health check; `DVFS_MONGO_TEST_URI=mongodb://localhost:27017` for `make test` and a new `make test-google-auth` step; `go vet -tags use_google_auth`; Go pinned to 1.26.x in both `ci.yml` and `release.yml` to match `go.mod`.
- **Docs**: README quick start; `docs/setup.md` gains a MongoDB section (install, auth, bind address, replica set, `EnvironmentFile`); `docs/architecture.md`; `docs/features/crash_recovery.md` keeps the old JSON shape as historical and documents orphan behaviour; `docs/features/admin_telemetry.md`; the overview and coordinator diagrams.

## 10. Bugs fixed

Each has a regression test that the author confirmed fails without the fix.

### Present on `main` before the PR

| Bug | Before | After | Tests |
|---|---|---|---|
| Registration conflict after partial mutation | Users were remapped and purged before a later user's conflict returned an error. | All conflicts are checked before anything is mutated or written. | `internal/metaserver` tests |
| Share paths not normalised | `/alice/proj` stored by registration, `alice/proj` compared by unshare: revocation silently no-opped after a fileserver restart. | `NormalizeSharePath` at every call site, keyed delete in the store. | `internal/storage/meta_test.go`, conformance suite, `internal/metaserver` tests |
| Share dedupe by owner only | A second directory shared by the same owner with the same grantee was dropped with `success`. | Dedupe on (owner, path). | `internal/metaserver` tests |
| Node removal with no MetaServer address | Console reported success and hid the node; the cluster kept routing to it. | Response carries an explicit warning that the cluster was not changed. | `internal/admin/remove_node_test.go` |
| Address as identity | A DHCP change allocated a phantom node, and the real node could not re-register while its users pointed at the old id. | Keyed on `fs_id`; an address change updates the record in place. | `internal/metaserver/identity_test.go` |

### Introduced and fixed within the migration

| Bug | Problem | Fix | Tests |
|---|---|---|---|
| Users dropped at hydration | An early `hydrate` discarded users whose node id had no record, so they were re-placed on next login and their data stranded. | Retained as orphaned: refused at login, routable when the node returns. | `internal/metaserver` tests |
| Orphan markers cleared only by name | A node registering with an empty disk scan left its users orphaned indefinitely. | Markers are cleared by node id. | `internal/metaserver` tests (register with no users) |
| Address change zeroed `UserCount` | An early `UpsertFileServer` persisted a whole record with `UserCount` 0, so the node looked empty after a restart. | `UserCount` is never persisted; the address is a targeted update. | `internal/metaserver/identity_test.go` |
| SSH restart passed the removed `-state_file` | Go's flag parser exits non-zero on an unknown flag, so an admin-triggered restart took the MetaServer and Admin down. | Commands rely on the host's `MONGO_URI`. | `internal/admin/ssh_test.go` |
| Remove-node tests dialed the campus MetaServer | With no address configured, Gist discovery resolved the production host from CI. | Test servers have no resolver. | `internal/admin/remove_node_test.go` |

## 11. Explicitly not changed

- ACLs, the inode index, quotas and the admin's telemetry files stay on local disk.
- Two pre-existing data races (admin orchestrator, client callback wiring) mean `go test -race` still fails; the CI race step stays off.
- The inode index rewrite-per-create and in-memory trash restore metadata are known and separate.
- There is no importer from `metaserver_state.json`. A cluster starts with an empty database and rebuilds itself as fileservers re-register and users log in.
