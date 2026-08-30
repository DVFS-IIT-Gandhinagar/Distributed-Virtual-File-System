# Trash / Restore — Run & Test Guide

This repo supports:

- `delete` = **permanent delete** (server-side DFS post-order delete + inode map cleanup)
- `trash` = **soft delete** (moves item into `.trash`)
- `restore` = **restore** from `.trash` back to original location (best-effort)
- `show_trash` = **safe listing** of `.trash` contents without `cd`
- `clear_trash` = **empty trash** permanently
- `delete -t <name>` = **permanently delete one item from trash** (recursive implied)
- Shared users only see/restore trash entries they had ACL access to

## Prerequisites (one-time)

From repo root:

```bash
make certs
make proto
make build
```

If `make proto` fails with `protoc-gen-go: program not found`, install plugins:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Start the system

Open 1–2 terminals.

### Option A (direct connect; simplest)

Terminal 1 (file server):

```bash
make run-server
```

Terminal 2 (client):

```bash
make run-client USER=romit IP_ADDR=127.0.0.1
```

### Option B (via meta server)

Terminal 1 (meta server):

```bash
make run-metaserver
```

Terminal 2 (file server registers with meta server):

```bash
./bin/fileserver -id=fs1 -port=50051 -data=./fileserver_data -meta_addr=127.0.0.1:50052 -own_ip=127.0.0.1
```

Terminal 3 (client navigates via meta server):

```bash
./bin/client -username=romit -ip_addr=127.0.0.1 -port=50051 -meta=true -meta_addr=127.0.0.1:50052
```

## Manual test cases (run inside the client)

### 1) Basic `trash` + `restore` for a file

```text
mkdir t
cd t
create a.txt
ls
trash a.txt
ls
```

Expected:
- `a.txt` disappears from the current directory.

Now verify it exists in `.trash`:

```text
cd ..
show_trash
```

You should see `a.txt` (or a collision-safe name like `a.txt__<inodeID>`).

Restore it (you can run `restore` from any directory):

```text
restore a.txt
```

Then confirm it’s back:

```text
cd t
ls
```

### 2) Directory trash requires `-r` if non-empty

```text
cd ..
mkdir d
cd d
create f
cd ..
trash d
```

Expected:
- Error complaining the directory is not empty / needs recursive.

Now:

```text
trash -r d
```

Expected:
- Directory is moved to `.trash`.

### 3) `delete` is permanent (does NOT go to trash)

```text
mkdir p
cd p
create x
cd ..
delete -r p
```

Expected:
- `p` is removed from the filesystem and the inode map (it will NOT appear in `.trash`).

### 4) Collision behavior in `.trash`

If you trash two files with the same name (from different folders), trash may rename one:

```text
mkdir c1
mkdir c2
cd c1
create same
cd ..
cd c2
create same
cd ..
cd c1
trash same
cd ..
cd c2
trash same
cd ..
show_trash
```

Expected:
- One might be stored as `same__<inodeID>`.

### 5) You cannot create inside `.trash`

```text
mkdir nope
trash nope
show_trash
```

Expected:
- `show_trash` lists trashed entries.
- Direct navigation into `.trash` and its subfolders is rejected.
- Direct file creation inside `.trash` remains blocked.

### 6) Empty trash with `clear_trash`

```text
show_trash
clear_trash
show_trash
```

Expected:
- `clear_trash` permanently deletes all entries currently listed in trash.
- A follow-up `show_trash` prints `(trash is empty)`.

### 7) Permanently delete one trash item with `delete -t`

```text
show_trash
delete -t same__123
show_trash
```

Expected:
- `delete -t <name>` removes that specific item from trash permanently.
- For directories in trash, `-r` is not required when `-t` is used.

### 8) Important limitation (current implementation)

Restore requires server-side metadata that is stored **in-memory**.

That means:
- If you restart the file server after trashing something, `restore` may fail with a message like “restore metadata not available”.
- Workaround for now: restore before restarting the server.

### 9) Shared-user trash visibility and restore rules

- If `alice` shares a project with `bob`, `bob` does not get full visibility into `alice/.trash`.
- `show_trash` for `bob` is ACL-filtered and only includes trashed items `bob` had access to.
- `restore <name>` is also ACL-checked; `bob` cannot restore trashed items that were never shared with `bob`.

## Quick automated check

Run:

```bash
go test ./...
```

This includes `internal/fileserver/trash_restore_test.go` which validates:
- trash + restore path updates
- recursive requirement for non-empty directories
- you cannot delete the `.trash` directory




# doubt - heirarcchal trash or independent trash
trash folder B from inside folder A and then trash folder A. 
- When we restore folder B, should it be restored to its original location, or in trash inside A or smwhere else?
- On removing folder A from trash, should folder B also be removed from trash or not?
- On restoring folder A, should folder B also be restored or not?