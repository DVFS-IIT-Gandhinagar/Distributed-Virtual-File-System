# MongoDB Migration — MetaServer Cluster Metadata

**Status: delivered.** This document describes what was built and why, and records the
decisions taken along the way. It is written after the fact, so it matches the code rather
than proposing something.

Scope: metaserver routing state only. Fileserver and admin on-disk state deliberately stayed
where it was — see *What did not move* below.

---

## 1. The problem

The metaserver kept its entire state in `metaserver_state.json`: the node table, every
user→node assignment, and every share. Three properties of that design were the motivation:

1. **Write amplification.** Every heartbeat rewrote the *whole* file. With one heartbeat per
   fileserver per 5 seconds, a cluster of ten nodes re-serialised all users and all shares
   every half second — and the heartbeat write was pure waste, because the loader re-derived
   the heartbeat timestamp at boot anyway.
2. **The write happened under the global lock.** The metaserver holds one `ms.mu`, so every
   snapshot serialise-and-write sat in front of every `GetRoots` and `Navigate`.
3. **The admin console read the same file off local disk**, with a fallback search across
   three paths. That is *why* `bin/admin` had to run on the metaserver's host.

## 2. Design rules

Four rules govern the implementation. Each exists to preserve a property the JSON version had,
or to avoid a failure the JSON version was immune to.

1. **In-memory maps stay the read path.** The store is hydrated once, at boot, by
   `LoadSnapshot`. `GetRoots` and `Navigate` never touch MongoDB, so a backend outage is
   invisible to clients already in the index. The JSON version served reads from memory too;
   routing must not become *less* available by gaining a database.
2. **Store calls happen only after `ms.mu` is released.** A store call is network I/O.
   Issuing it under the global mutex would put a round-trip — and, during a replica-set
   election, a multi-second stall — in front of every request. Handlers collect mutations in a
   `deferredWrites` slice and run them after unlocking. This is the same discipline the
   fileserver already applies to its own metaserver RPCs.
3. **Targeted operators, never whole-document replacement.** `$set` one heartbeat field,
   `$addToSet`/`$pull` one share. This is what removes the write amplification, and it makes
   out-of-order application idempotent — which matters precisely *because* of rule 2: writes
   are issued after the lock is dropped, so they can arrive in any order.
4. **Failure policy is split by consequence.** Heartbeats are best-effort: memory already has
   the update and the next interval corrects it, so failing the RPC would take routing down
   for a transient blip. Share and user-assignment writes fail closed and roll the in-memory
   change back, because a silently lost grant is a correctness bug.

## 3. The seam

`internal/storage` defines `MetaStore`, the only persistence interface in the tree apart from
`SSHExecutor`, whose precedent it follows. Two backends implement it:

- `storage/mongo` — production.
- `storage/memory` — the test double, playing the `MockSSHExecutor` role.

Both run the same `storage/storagetest.RunMetaStoreConformance` suite, so the double cannot
drift from the real backend. CI runs a `mongo:7` service container and sets
`DVFS_MONGO_TEST_URI`; without it the Mongo conformance tests skip and only the double runs.

## 4. Schema

Database `dvfs`.

| Collection | `_id` | Notes |
|---|---|---|
| `fileservers` | node id (`"fs1"`) | Carries `numeric_id` for the admin UI's display derivation, plus address, user count, heartbeat, status. |
| `users` | username | `home_node_id`. |
| `shares` | compound `(grantee, owner, path)` | Uniqueness for free; revocation is a keyed delete. |
| `counters` | counter name | Numeric-id sequence via `findOneAndUpdate($inc)`. |

Indexes: `numeric_id` (unique, sparse), `address`, `(status, last_heartbeat_unix)` on
fileservers; `home_node_id` on users; `_id.grantee` and `_id.owner` on shares.

Write concern is `majority`, with a separate `w:1` handle for heartbeats so liveness churn
does not pay a majority ack.

### Two behaviours verified against a live MongoDB, not assumed

- **A compound subdocument `_id` is compared by exact binary equality, including key order.**
  `{grantee,owner,path}` and `{owner,grantee,path}` are different keys: a reordered lookup
  matches nothing, and a reordered insert creates a *duplicate* grant instead of being
  rejected. The order is pinned by always marshalling the `shareKeyDoc` struct, never a
  `bson.M`. This is documented at the struct because it is the most likely way a future edit
  silently breaks revocation.
- **A unique index treats a missing field as null**, so two documents lacking `numeric_id`
  would collide. The index is sparse.

## 5. Node identity

Nodes are keyed on the stable `-id` (`fs1`), sent in the `fs_id` field added to
`RegisterFileServerRequest` and `HeartbeatRequest`. Keying on address is what let a DHCP lease
change register the same machine twice, with the phantom entry then attracting new users.

Pre-upgrade fileservers that omit `fs_id` fall back to an address-derived identity
(`addr:<address>`), and an existing address-keyed entry is adopted on first contact so
upgrading a node does not orphan its users.

All share paths go through `storage.NormalizeSharePath`. Registration used to store
`/alice/proj` while unshare compared `alice/proj`, so revocation silently no-opped for any
share that had survived a fileserver restart.

## 6. What did not move, and why

Moving everything would have been the wrong call. The split follows authority and locality:

| State | Decision |
|---|---|
| Metaserver routing, users, shares, heartbeats | **Moved.** Shared across nodes, small, and the file coupling forced admin co-location. |
| Per-directory `.acl` | **Stayed.** ACLs are enforced only on the fileserver. Moving them puts a network hop in the permission path and stops the data directory being self-describing. |
| `.dvfs_inodes_index.json` | **Stayed.** Node-local. Its full-rewrite-per-create cost is real but a *different* problem; a backend swap is not the fix. See `docs/deferred-work.md`. |
| `fileserver_shares.json`, `quota_config.json` | **Stayed.** Node-local. Quota is logically cluster-wide and is a candidate later. |
| Admin snapshot / history / alerts | **Stayed.** Node-local telemetry, already bounded ring buffers. |
| Client cache, `machines_cache.json` | **Never.** Per-client and disposable. |

## 7. Cutover, rollback, compatibility

**No data import.** The cluster starts on an empty MongoDB. This was a deliberate choice made
with the operator: no real users were on the system, so there was no placement data worth
preserving, and the leftover `metaserver_state.json` was stale local test state. The file has
been deleted; nothing reads it.

Had there been live users, an importer would have been required. Metaserver state is
*derived* in most respects — fileservers republish their users and shares from their own
on-disk ACLs on every registration — but user→node **placement** is not derivable, and losing
it silently reassigns users away from their data.

**Rollback** is `mongodump` plus the previous binary. Because the metaserver is advisory and
the fileserver is authoritative, the worst case is re-registration: bring the fileservers up
and let them republish. Take a `mongodump` before any schema change.

**Backward compatibility** is at the wire, not the file: `fs_id` was added as a new proto
field, so old fileservers still register via the address-derived fallback. There is no
JSON fallback path — `bin/metaserver` and `bin/admin` now refuse to start without a
`-mongo_uri`, deliberately, since starting with empty routing state would strand every user.

## 8. Defects fixed during the migration

Each is covered by a regression test that was verified to fail without its fix.

1. **Users orphaned on restart.** A user whose home node was not in the snapshot was dropped
   at hydration, so the next `GetRoots` treated them as new and assigned them a different
   node while their data stayed on the original one. They are now retained as orphaned:
   never reassigned, routable again as soon as their node returns.
2. **DHCP address change zeroed the persisted user count.** The heartbeat address-change path
   wrote a whole node record whose `UserCount` defaulted to zero. Invisible until restart,
   when the node looked empty and attracted every new user. Now a targeted update.
3. **Partial state escaped a failed registration.** A registration naming an already-assigned
   user returned an error *after* mutating the node record and reassigning an arbitrary
   prefix of the user list, with no rollback and no store write. Conflicts are now detected
   before anything is mutated.
4. **Admin-driven restarts left services down.** The SSH restart commands still passed the
   removed `-state_file` flag; Go's flag package exits non-zero on an unknown flag, so the
   metaserver and admin console never came back. They now pass `-mongo_uri`.

Two pre-existing data races, unrelated to this work, are recorded in `docs/deferred-work.md`
rather than fixed here.

## 9. Verification

```bash
make fmt && make vet
make test && make test-google-auth     # both tags: go test ./... does not compile the auth code

docker run -d -p 27017:27017 --name dvfs-mongo mongo:7
DVFS_MONGO_TEST_URI="mongodb://127.0.0.1:27017" go test ./internal/storage/... -count=1
go test -race ./internal/metaserver/... ./internal/storage/... -count=1
```

End-to-end against a live cluster:

1. Start MDS + FS, log a user in, confirm the `users` document appears.
2. Point a user's `home_node_id` at an absent node, restart the MDS, and confirm the user is
   retained and **not** reassigned on next login.
3. Change a node's address, restart, and confirm `user_count` survives.
4. Start against an empty database and confirm it repopulates as fileservers register.
5. Run the admin console on a **different host** from the metaserver, and confirm an
   admin-driven restart brings the services back up.
6. Stop MongoDB and confirm `GetRoots`/`Navigate` still serve users already in the index.
