# MDS Crash Recovery and Heartbeat Runbook

## Implementation Status

- Crash recovery for MDS persistent routing state is implemented.
  - MDS persists: registered file servers, user-to-fileserver mapping, next file-server ID.
  - MDS restores this state on restart from a snapshot file.
- Heartbeat and liveness tracking are implemented.
  - File servers send lightweight heartbeat RPCs.
  - MDS marks servers stale when heartbeat timeout is exceeded.
  - MDS routes users only to healthy servers.
- Auto re-attach for running file servers is implemented.
  - File servers keep registration retry + heartbeat loops.

## Prerequisites

From repo root:

```bash
make certs
make build
```

## Terminal Layout

Use 4 terminals:
- T1: MetaServer (MDS)
- T2: FileServer (FS)
- T3: Client A
- T4: Client B (optional, for verification)

## 1) Start MetaServer (MDS)

```bash
./bin/metaserver \
  -port=50051 \
  -state_file=./metaserver_state.json \
  -heartbeat_timeout=30s \
  -heartbeat_check_interval=5s
```

Notes:
- `-state_file` controls crash-recovery snapshot location.
- Keep the same `-state_file` path across restarts.
- `-heartbeat_timeout` controls when a fileserver becomes stale.
- `-heartbeat_check_interval` controls stale checks frequency.

## 2) Start FileServer with MDS sync enabled

```bash
./bin/fileserver \
  -id=fs1 \
  -port=50052 \
  -data=./fileserver_data/fs1 \
  -meta_addr=127.0.0.1:50051 \
  -own_ip=127.0.0.1 \
  -meta_retry_interval=1s \
  -meta_heartbeat_interval=2s
```

Notes:
- `-meta_addr` points to MDS.
- `-own_ip` is the IP advertised to MDS, so MDS stores `127.0.0.1:50052`.
- `-meta_heartbeat_interval` controls heartbeat RPC frequency.
- Short intervals above are for quick local testing. Increase for normal runs.

## 3) Start Client (via MDS)

```bash
./bin/client \
  -username=alice \
  -ip_addr=127.0.0.1 \
  -port=50051 \
  -meta=true
```

This asks MDS to route the user and then connects to the assigned file server.

## 4) Crash-Recovery Test (MDS restart)

### Step A: Establish state

1. Keep MDS and FS running.
2. Start client as `alice` (command above).
3. Optional: start another client as `bob`:

```bash
./bin/client -username=bob -ip_addr=127.0.0.1 -port=50051 -meta=true
```

4. Confirm snapshot exists:

```bash
ls -l ./metaserver_state.json
cat ./metaserver_state.json
```

### Step B: Crash/stop MDS only

In T1, stop MDS with Ctrl+C.

### Step C: Keep FS running, restart MDS

Restart MDS using the same state file:

```bash
./bin/metaserver -port=50051 -state_file=./metaserver_state.json
```

Expected in MDS logs:
- Recovery log showing restored counts (fileservers/users/nextFsID).

Expected in FS logs (within retry/heartbeat window):
- Sync success logs after MDS returns.
- Heartbeat success logs after MDS returns.

### Step D: Validate behavior

1. Start a new client for existing user:

```bash
./bin/client -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```

2. Client should route successfully without restarting FS.

## 5) Heartbeat and Stale Transition Test

This test verifies true heartbeat behavior (with retry-based recovery):

1. Run FS with short intervals:

```bash
./bin/metaserver -port=50051 -state_file=./metaserver_state.json -heartbeat_timeout=6s -heartbeat_check_interval=1s
```

Run fileserver with frequent heartbeat:

```bash
./bin/fileserver -id=fs1 -port=50052 -data=./fileserver_data/fs1 -meta_addr=127.0.0.1:50051 -own_ip=127.0.0.1 -meta_retry_interval=1s -meta_heartbeat_interval=2s
```

2. Watch FS logs:
- You should see periodic heartbeat success messages.

3. Watch MDS logs:
- You should see heartbeat RPC activity and no stale transitions while FS is alive.

4. Stop the fileserver process and wait > `heartbeat_timeout`.

Expected on MDS:
- Fileserver status transitions to stale.
- New navigate requests are not routed to that stale fileserver.

5. Restart fileserver and verify it becomes healthy again via registration + heartbeat.

## 6) Regression Test Suite

Run all tests:

```bash
go test ./...
```

Run only metaserver recovery tests:

```bash
go test ./internal/metaserver -v
```
