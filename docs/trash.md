# Trash / Restore — Run & Test Guide

This repo supports:

- `rm` = **permanent delete** (server-side DFS post-order delete + inode map cleanup)
- `trash` = **soft delete** (moves item into `.trash`)
- `restore` = **restore** from `.trash` back to original location (best-effort)

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
cd .trash
ls
```

You should see `a.txt` (or a collision-safe name like `a.txt__<inodeID>`).

Restore it (you can run `restore` from any directory):

```text
restore a.txt
```

Then confirm it’s back:

```text
cd ..
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

### 3) `rm` is permanent (does NOT go to trash)

```text
mkdir p
cd p
create x
cd ..
rm -r p
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
cd .trash
ls
```

Expected:
- One might be stored as `same__<inodeID>`.

### 5) You cannot create inside `.trash`

```text
cd .trash
mkdir nope
create nope.txt
```

Expected:
- Both operations fail (trash is treated as a protected area).

### 6) Important limitation (current implementation)

Restore requires server-side metadata that is stored **in-memory**.

That means:
- If you restart the file server after trashing something, `restore` may fail with a message like “restore metadata not available”.
- Workaround for now: restore before restarting the server.

## Quick automated check

Run:

```bash
go test ./...
```

This includes `internal/fileserver/trash_restore_test.go` which validates:
- trash + restore path updates
- recursive requirement for non-empty directories
- you cannot delete the `.trash` directory