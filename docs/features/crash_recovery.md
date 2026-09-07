# Crash Recovery & Inode Persistence

This document details the crash recovery and state persistence mechanisms of DVFS, covering the theoretical durability model (**In Essence**) and the engineering solutions for restart survival (**Implementation Quirks**).

---

## 1. In Essence: Durability & High Availability

A distributed filesystem must survive unexpected node crashes, power outages, and service restarts without data corruption, ghost entries, or desynchronized client sessions.

DVFS achieves durability and fault isolation through three core invariants:
1. **Authoritative FileServer State Durability**: Physical data, directory hierarchies, permissions, quotas, and inode identifiers persist on physical disk. A restarted FileServer reconstructs its exact pre-crash state without relying on the MetaServer.
2. **Deterministic Identity Persistence**: File Identifiers (FIDs) must be stable across reboots. If a client holds a reference to `fs1_42_1`, the FileServer must resolve `fs1_42_1` to the exact same file after a restart.
3. **Heartbeat Monitoring & Stale Isolation**: The MetaServer continuously monitors storage nodes via heartbeats. If a node goes offline, the MetaServer immediately isolates it from new client routing requests while preserving existing ownership mappings for when the node returns.

```
[Running Cluster] ---> [FileServer Node Crashes] ---> [MetaServer Ticker Exceeds 30s]
                                                             |
                                                             v
[Client Receives Error] <--- [Routing Excludes Node] <--- [Marked Stale]
         |
         v
[Node Reboots & Loads State] ---> [Registration & Heartbeat] ---> [Marked Healthy]
                                                                        |
                                                                        v
                                                            [Client Resumes Ops]
```

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Persistent InodeStore (`.dvfs_inodes_index.json`)

#### The Problem: Volatile Inode IDs
In early DVFS prototypes, FileServer startup walked the directory tree in parallel goroutines and assigned Inode IDs sequentially from `0`. This created severe bugs:
- Depending on thread scheduling, on one boot a folder was assigned ID `2`, and on the next boot it received ID `5`.
- If a client was connected during a fileserver restart, running `refresh` sent `ListDir` using the old FID (`fs1_2_1`), which now pointed to an entirely different or empty folder.
- The client's cache handler wiped out its local cached directory tree because the server returned an empty directory listing.

#### The Solution: InodeStore Architecture
In `internal/fileserver/inodestore.go`, every FileServer root maintains a persistent mapping file named `.dvfs_inodes_index.json`:

```json
{
  "next_inode_id": 43,
  "path_to_id": {
    "alice/documents/report.pdf": 42,
    "alice/photos": 17
  }
}
```

- **Path Normalization**: Before lookup or storage, paths are converted to forward slashes (`/`) and cleaned via `filepath.Clean` to guarantee cross-platform consistency between Linux and Windows hosts.
- **Deterministic ID Allocation**: If a path exists in `.dvfs_inodes_index.json`, its Inode ID is preserved across restarts. Only new files allocate a fresh ID from `next_inode_id`.
- **Thread Safety**: All InodeStore operations are synchronized with a dedicated `sync.Mutex`.
- **Atomic Commits**: Saved via write-to-temp and atomic `os.Rename`.

#### Client Session Restoration (`ReRegister`)
When a FileServer crashes and restarts, its volatile in-memory client registrations and callback channels are erased. Rather than forcing clients to terminate and relaunch:
1. When a client executes `refresh` (or on subsequent directory access), `CacheHandler.Refresh()` invokes `Client.ReRegister()`.
2. The client re-sends `RegisterClient` RPC with its active `ClientID`, `CallbackAddress`, `Username`, `RootUser`, and `RootPath`.
3. The restarted FileServer re-establishes the callback session and returns its root FID.
4. The client validates that the root FID returned matches its cached root FID (ensured by `.dvfs_inodes_index.json`), seamlessly restoring push invalidations.

### 2.2 MetaServer State Recovery (`metaserver_state.json`)
The MetaServer coordinator persists its complete operational state in `metaserver_state.json`:

```json
{
  "fileservers": {
    "0": {
      "address": "10.7.52.85:50052",
      "user_count": 2,
      "last_heartbeat_unix": 1773062400,
      "status": "healthy"
    }
  },
  "users": {
    "alice": 0,
    "bob": 0
  },
  "shared": {
    "bob": [
      {
        "Owner": "alice",
        "Path": "alice/shared_proj",
        "DisplayName": "shared_proj"
      }
    ]
  },
  "next_fs_id": 1
}
```

- **Startup Reconstitution**: On launch, `NewMetaServer(*stateFile)` parses the JSON snapshot, immediately restoring known fileservers, user-to-fileserver assignments, and shared directory registries.
- **Heartbeat Evaluation**: Heartbeat timestamps are evaluated against the current Unix time. If a node has not checked in within the timeout window, it transitions to `stale`.

### 2.3 Heartbeat & Stale Transitions
- **FileServer Heartbeat Loop**: A background goroutine in `internal/fileserver/msclient.go` executes `Heartbeat(address)` to the MetaServer every 5 seconds (configurable via `-meta_heartbeat_interval`).
- **MetaServer Monitor Ticker**: A background goroutine runs every 5 seconds (`-heartbeat_check_interval`):
  - Checks each registered server's `now - LastHeartbeatUnix`.
  - If delta > 30 seconds (`-heartbeat_timeout`), the node status transitions from `healthy` to `stale`.
- **Routing Exclusion**: When a client requests `Navigate(username, rootUser)`, the MetaServer checks the hosting node's status. Stale nodes are rejected with:
  `"root user 'alice' is on unavailable file server"`.
- **Automatic Re-Attachment**: When an offline FileServer comes back online, its registration loop automatically reconnects to the MetaServer, sends a `Heartbeat`, and the MetaServer restores its status to `healthy`.

### 2.4 Admin Console Cold-Start Recovery
The Admin Console maintains historical monitoring graphs and alerts that survive restarts:
1. **Ring-Buffer Snapshot (`admin_metrics_snapshot.json`)**:
   - Every 60 seconds and during graceful shutdown (`Stop()`), `SaveMetricsSnapshot` flushes the 60-minute ring buffers (720 data points per node) to disk.
   - On startup, `LoadMetricsSnapshot` reloads historical telemetry so graphs do not reset to zero.
2. **Alert Engine Persistence (`admin_alerts.json`)**:
   - Active and resolved alerts are persisted via atomic file writes, maintaining an audit trail across console restarts.

---

## 3. Crash Recovery Verification Runbooks

### 3.1 Four-Terminal MDS Crash Recovery Test

This test verifies that the MetaServer coordinator persists its routing table, restores state on cold reboot, and allows client operations to continue without FileServer restarts.

#### Terminal Layout
- **Terminal 1 (MDS)**: MetaServer
- **Terminal 2 (FS)**: FileServer `fs1`
- **Terminal 3 (Client A)**: Client `alice`
- **Terminal 4 (Client B)**: Client `bob` (verification)

#### Step 1: Start MetaServer
```bash
./bin/metaserver \
  -port=50051 \
  -state_file=./metaserver_state.json \
  -heartbeat_timeout=30s \
  -heartbeat_check_interval=5s
```

#### Step 2: Start FileServer
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

#### Step 3: Establish Client State & Snapshot
In Terminal 3, launch Client A:
```bash
./bin/client -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```
Inside the client REPL, upload a test file:
```bash
upload ./test.txt
```
In Terminal 4 (optional), launch Client B to populate another user:
```bash
./bin/client -username=bob -ip_addr=127.0.0.1 -port=50051 -meta=true
```
Confirm the state snapshot is written:
```bash
cat ./metaserver_state.json
```

#### Step 4: Crash and Restart MetaServer
1. In Terminal 1, stop the MetaServer with `Ctrl+C` (or `kill -9`).
2. Keep the FileServer running in Terminal 2. Note in FS logs that retry/heartbeat attempts temporarily fail.
3. Restart the MetaServer using the exact same state file:
```bash
./bin/metaserver -port=50051 -state_file=./metaserver_state.json
```
4. Observe the logs:
   - **MDS Logs**: State recovery log reporting restored counts (`fileservers`, `users`, `next_fs_id`).
   - **FS Logs**: Re-connection and heartbeat success logs resume within the retry interval.

#### Step 5: Validate Resumption
Start a new client session for `alice`:
```bash
./bin/client -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```
The client routes successfully and can immediately access `test.txt` without restarting the FileServer.

---

### 3.2 Heartbeat & Stale Transition Test (Fast-Test Flags)

To verify the MetaServer's liveness tracker and stale node isolation without waiting 30+ seconds, run with accelerated heartbeat flags:

#### Step 1: Start MetaServer with Fast Heartbeat Checks
```bash
./bin/metaserver \
  -port=50051 \
  -state_file=./metaserver_state.json \
  -heartbeat_timeout=6s \
  -heartbeat_check_interval=1s
```

#### Step 2: Start FileServer with High-Frequency Heartbeats
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
Verify that MDS logs show periodic heartbeat RPC activity and the node remains `healthy`.

#### Step 3: Simulate Crash & Observe Stale Transition
1. Kill the FileServer process (`Ctrl+C` or `kill -9`).
2. Wait 6 seconds (`-heartbeat_timeout=6s`).
3. Observe MDS logs: `node fs1 heartbeat timed out; status transitioned to stale`.
4. Try connecting a new client:
```bash
./bin/client -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```
The client receives an immediate failure: `root user 'alice' is on unavailable file server`.

#### Step 4: Restart FileServer & Verify Auto Re-Attachment
1. Restart the FileServer with the same `-data` path.
2. FileServer loads `.dvfs_inodes_index.json`, registers with MDS, and sends a Heartbeat.
3. MDS logs confirm status transitioned back to `healthy`.
4. Existing client runs `refresh` (triggering `ReRegister()`) or new clients connect seamlessly.

### 3.3 FileServer Restart & Client Session Re-Registration Test

This test verifies that persistent inode allocation (`.dvfs_inodes_index.json`) and client `ReRegister()` prevent cache wipeouts when a storage node restarts.

#### Step 1: Populate Client Session
1. Launch Client as `alice` and upload test files:
   ```text
   dvfs> upload ./report.pdf
   dvfs> mkdir projects
   dvfs> ls
   Name                 Type             Size
   ----                 ----             ----
   .trash               dir                 0
   projects             dir                 0
   report.pdf           file          4194304
   ```

#### Step 2: Restart Storage Node
Restart the FileServer process while leaving the client session active:
```bash
# Via systemd
sudo systemctl restart dvfs-fileserver
# Or kill and relaunch binary with the exact same -data directory
```

#### Step 3: Trigger Client Refresh
In the active `dvfs>` client prompt, execute:
```text
dvfs> refresh
Current directory cache refreshed.
dvfs> ls
```

#### Expected Verification Results
1. **No Stale FID Cache Wipeout**: `ls` displays `projects` and `report.pdf` immediately without returning an empty directory.
2. **Session Re-Registration**: The client calls `ReRegister()` behind the scenes, restoring push invalidation callbacks on the restarted FileServer.
3. **Index Stability**: Verify `.dvfs_inodes_index.json` in the FileServer `-data` root retains the exact same inode IDs for existing paths without reallocation.

---

### 3.4 Automated Regression Tests
To run the automated test suite for MetaServer crash recovery and state persistence:
```bash
go test ./internal/metaserver -v
go test ./internal/fileserver -v -run TestInodeStore
```

