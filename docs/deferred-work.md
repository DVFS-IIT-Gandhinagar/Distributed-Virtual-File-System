# Deferred Work

Known defects and improvements that are **out of scope for the MongoDB migration** but were
found while doing it. Recorded here so the findings are not lost when unrelated changes are
reverted to keep the migration diff focused.

Nothing in this file is a regression introduced by the migration. Each item either predates it
or is an independent improvement.

---

## 1. Data race in the admin orchestrator

`internal/admin/actions.go:505` and `:507` write the shared `overallFailed` flag from every
per-node goroutine spawned at `:432`, with no synchronisation. Confirmed with the race
detector:

```
WARNING: DATA RACE
Write at 0x00c00011de2c by goroutine 12:
  (*Orchestrator).Execute.func2()  internal/admin/actions.go:505
Previous write at 0x00c00011de2c by goroutine 11:
  (*Orchestrator).Execute.func2()  internal/admin/actions.go:507
```

Reproduce:

```bash
go test -race ./internal/admin/ -run TestOrchestratorMultiNodePartialFailure -count=1
```

This fails today on `main`. Since every goroutine only ever writes `true`, the practical
impact is low, but it is a genuine race and it makes the whole package unrunnable under
`-race`. Fix with a mutex, an atomic, or by collecting per-node results and reducing after
the `WaitGroup`.

## 2. Data race on client callback wiring

`internal/client/client.go:121` (`AttachCacheHandler`) and `:127` (`SetNotifyWriter`) mutate
`c.cacheHandler` and `c.notifyWriter` while the client's gRPC callback server reads them from
another goroutine. Unsynchronised.

The window is narrow in practice because both setters are normally called during startup
before callbacks arrive, but nothing enforces that ordering.

## 3. CI race step is written but disabled

Because of items 1 and 2, `go test -race ./...` does not pass, so the CI race step cannot be
enabled yet. Turn it on once both are fixed — that is the real value of fixing them.

## 4. Inode index is rewritten in full on every file creation

`internal/fileserver/inodestore.go` marshals the entire `relPath → inodeID` map and rewrites
`.dvfs_inodes_index.json` on every `CreateFile`, plus six other call sites. At large inode
counts this is a far bigger write-amplification problem than the metaserver state file the
MongoDB migration removed.

Deliberately **not** bundled into the MongoDB work: this state is node-local and belongs to
the authoritative fileserver, so moving it to MongoDB would put a network hop in the file
creation path and stop the data directory being self-describing. The fix is incremental
persistence (append-only journal or batched writes), not a backend swap.

All seven call sites also discard the returned error (`_ =`), and a corrupt index is silently
treated as a fresh start — which would reassign every FID in the cluster.

## 5. Trash restore metadata is in-memory only

`fs.trashMeta` (`internal/fileserver/fileserver.go`) holds the original parent, name, and path
needed to restore a trashed item. It is never persisted, so after a fileserver restart
`restore` fails with "restore metadata not available" or drops the item at the user root. The
files themselves survive; only the restore mapping is lost.

## 6. No fsync before rename in any on-disk store

Every durable JSON store writes to a temp file and then `os.Rename`s it, which is atomic
against a crashed *process* but not against power loss, since neither the temp file nor the
parent directory is fsynced. This applies to `.acl`, `fileserver_shares.json`,
`quota_config.json`, `.dvfs_inodes_index.json`, and the admin snapshot/history/alerts files.

Worth addressing given the deployment target is Raspberry Pis on SD cards.

`~/.dvfs/machines_cache.json` (`internal/client/discovery.go`) is weaker still: it writes in
place with no temp file at all, so an interrupted write leaves a truncated cache. It is a
disposable cache with a network fallback, so impact is low.

## 7. CI Go version was already behind go.mod

`go.mod` on `main` requires `go 1.26.0`, but both CI workflows pinned `1.24.0`. CI was behind
its own toolchain directive before this migration started.

`.github/workflows/ci.yml` was bumped to `1.26.0` as part of the migration because the
MongoDB driver needs Go 1.25+ and CI cannot build without it. `.github/workflows/release.yml`
has the **same latent problem** and was left alone to keep this diff focused — bump it
separately.

## 8. Unrelated changes reverted from the migration branch

These were correct but had nothing to do with MongoDB, so they were reverted to keep the
migration reviewable. Re-apply them as their own commits:

- **A `make test-google-auth` CI step.** Valuable, because `go test ./...` does not compile
  the auth code that `make build` actually ships, so auth regressions can pass CI today.
- **gofmt realignment and blank-line cleanup** across roughly 20 files. Correct gofmt output,
  but pure noise in a migration diff.
