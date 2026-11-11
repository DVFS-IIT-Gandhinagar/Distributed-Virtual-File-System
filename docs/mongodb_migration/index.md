# MongoDB Migration (PR #16): Overview

Pull request [#16](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/pull/16) (`shardul-mongo-integration` into `main`) moves the MetaServer's cluster routing state out of the on-disk `metaserver_state.json` file and into MongoDB, behind a small `storage.MetaStore` interface. The Admin Console is pointed at the same store, so it no longer has to run on the MetaServer's host.

This section documents the system as it was before the PR, what the PR changed, and the system as it is after, with side-by-side flow diagrams for every flow that changed.

| Page | What it covers |
|---|---|
| [Previous System](before.md) | How the JSON-file-backed MetaServer and Admin Console worked, flow by flow, and where that design was weak. |
| [What Changed](changes.md) | Commit list, file map, the new storage seam and MongoDB schema, protocol and flag changes, bugs fixed, deployment and CI changes. |
| [New System](after.md) | How the MongoDB-backed MetaServer and Admin Console work, flow by flow, including failure policy, lock ordering, orphaned users and legacy compatibility. |
| [Flow Diagrams](diagrams.md) | Before and after Mermaid diagrams: topology, internal structure, startup, registration, heartbeat, login, navigation, sharing, deregistration, admin refresh, node removal, SSH restart, outage behaviour, user lifecycle, lock ordering. |

## Scope of the PR

| Item | Value |
|---|---|
| Base | `main` at `24bbfb5` |
| Head | `shardul-mongo-integration` at `081b48f` |
| Commits | 18 |
| Files | 74 changed, 5026 insertions, 1553 deletions |
| New packages | `internal/storage` (interface), `internal/storage/mongo`, `internal/storage/memory`, `internal/storage/storagetest` |
| Removed | `internal/admin/state.go` and its tests, the `-state_file` flag on both binaries, the checked-in `metaserver_state.json` |

### What moved

Only the MetaServer's routing data: fileserver membership and liveness, user-to-node placement, and the shared-root visibility index. In MongoDB these are the collections `fileservers`, `users`, `shares` and `counters` in database `dvfs`.

### What deliberately did not move

| State | Stays where | Reason given in the PR |
|---|---|---|
| ACLs (`.acl` files) | FileServer local disk | The permission check must not take a network hop. |
| Inode index (`.dvfs_inodes_index.json`) | FileServer local disk | Belongs to the authoritative storage node. |
| Quotas (`quota_config.json`) | FileServer local disk | Same. |
| Admin telemetry snapshot, alerts, command history, removal tombstones | Admin Console local disk | Console-private operational data. |

## Before and after at a glance

| Aspect | Before (JSON file) | After (MongoDB MetaStore) |
|---|---|---|
| Durable store | `metaserver_state.json` next to the binary | MongoDB 7, database `dvfs`, four collections |
| Read path | In-memory maps | In-memory maps, hydrated once at boot by `LoadSnapshot` |
| Write granularity | Whole state re-serialised on every mutation and every heartbeat | One targeted, idempotent single-record (or bulk) write per change |
| Lock held during persistence | Yes, the `ms.mu` write lock was held across the file write | No, store writes run after `ms.mu` is released |
| Node identity | Network address | Stable `-id` (`fs1`) sent as `fs_id`; address is a fallback for old builds |
| Numeric id allocation | `next_fs_id` counter in the file | `counters` collection, `$inc` on `numeric_fs_id`, never recycled |
| Unreadable state at boot | Warning, start with empty state | Fatal, process exits |
| User whose home node has no record | Not representable | Retained as orphaned, refused at login, routable when the node returns |
| Heartbeat persistence | Whole-file rewrite; failure fails the RPC | `w:1` single-field update; failure only logged |
| Assignment and share write failure | RPC error, memory already mutated | RPC error and in-memory rollback |
| Share path form | Unnormalised; three call sites disagreed | `NormalizeSharePath`, one canonical form |
| Share dedupe | By owner only | By (owner, path) |
| Unlisted users at registration | Always purged | Retained with a warning unless `-allow_user_purge` |
| Admin Console state source | Reads the JSON file from local disk | `LoadSnapshot` from the same MongoDB |
| Admin node removal | Edits the JSON file directly, then calls the MetaServer | Only calls `DeregisterFileServer`; hides the node after success |
| Admin SSH binary restart | Passes `-state_file` | Requires `MONGO_URI` in the remote login environment; relaunches fileservers under their real `-id` |
| Binary flags | `-state_file` | `-mongo_uri`, `-mongo_db`, `-allow_user_purge` (MetaServer only) |
| systemd units | `STATE_FILE` env; admin ordered after metaserver | `MONGO_URI` env; both ordered after `mongod.service` |
| CI | Go 1.24, no database | Go 1.26, `mongo:7` service, both auth-tag builds tested |
