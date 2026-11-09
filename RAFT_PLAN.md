# Architectural Migration Plan: DVFS High Availability & Quorum Consensus

> **Status:** Design Phase — Not yet implemented  
> **Decisions recorded:** 30 September 2026  
> **Target Nodes:** dvfs1, dvfs8, dvfs9 as Control Plane; dvfs2–dvfs7 as pure FileServers

---

## 1. Executive Summary and Goals

The Distributed Virtual File System (DVFS) currently operates with a **Single Point of Failure (SPOF)**. The entire control plane — MetaServer, Admin Server, and the GitHub Gist updater — runs on **dvfs1** exclusively. A hardware failure on dvfs1 paralyzes the entire cluster.

**Goal:** Transition the DVFS control plane to a **highly available, fault-tolerant architecture** by clustering the MetaServer across three dedicated control nodes (dvfs1, dvfs8, dvfs9) using the **Raft consensus algorithm**. The system will continue operating correctly if any one control node crashes, until a human technician can diagnose and repair the failed machine.

**HA in this context means:** if the Raft Leader (e.g. dvfs1) crashes, the remaining two Followers automatically elect a new Leader (in under ~10–30 seconds depending on heartbeat timings), and the cluster keeps serving file operations without manual intervention.

**Non-goals:**
- This is NOT about maximizing write throughput or minimizing latency
- This is NOT about strong consistency on every individual Heartbeat call (see Section 3.3)
- Fileserver data is NOT replicated — data availability depends on fileserver uptime, not Raft

---

## 2. Target Architecture: The 3-Node Control Plane

### 2.1 Physical Node Assignment

| Node  | IP (Dynamic)   | Role                                     |
|-------|----------------|------------------------------------------|
| dvfs1 | 10.0.171.40    | Control Node (MetaServer + Admin Server + FileServer) |
| dvfs8 | 10.0.172.48    | Control Node (MetaServer + Admin Server + FileServer) |
| dvfs9 | *TBD via Gist* | Control Node (MetaServer + Admin Server + FileServer) |
| dvfs2–dvfs7 | Dynamic | Pure FileServer (unchanged) |

> **Note:** Each control node also runs its own FileServer process so no storage capacity is wasted. The MetaServer and FileServer processes are independent; the FileServer on a control node registers with the MetaServer cluster like any other FileServer.

### 2.2 Control Plane Services Per Control Node

```
[dvfs1 / dvfs8 / dvfs9]
├── dvfs-metaserver.service   ← Raft participant (Leader OR Follower)
├── dvfs-admin.service        ← Stateless; reads state via gRPC from the Raft Leader
├── dvfs-fileserver.service   ← Regular fileserver (registers into the cluster)
└── dvfs-gist.timer           ← Runs every 60 min; exits immediately if not the Raft Leader
```

### 2.3 Raft Quorum Properties

- **Quorum size:** 3 nodes — tolerates **1 node failure** while continuing to operate
- **Leader election:** automatic via Raft; completes within `2 × electionTimeout` (target: ~5–15 seconds)
- **State machine:** The MetaServer in-memory state (`fileservers`, `users`, `shared`, `nextFsID`) becomes the **Raft FSM** — replicated identically on all 3 nodes

---

## 3. MetaServer Raft Integration

### 3.1 Library Choice

Use **`hashicorp/raft`** v1.x, the same library used by Consul and Nomad. This is the most battle-tested Go Raft implementation.

Supporting dependencies:
- **`hashicorp/raft-boltdb/v2`** — BoltDB-backed Raft log store and stable store (durable across restarts, low RAM footprint, well-supported on ARM64 Raspberry Pi)
- **`boltdb/bbolt`** — embedded KV store underlying raft-boltdb

> **Why BoltDB (not in-memory)?** BoltDB is durable — if a control node crashes and restarts, it rejoins the Raft cluster by replaying its log from disk without needing a full snapshot re-transfer in most cases. In-memory log stores lose all log entries on crash, which defeats the purpose of HA.

### 3.2 New Files to Create

```
internal/metaserver/
├── raft.go          ← Raft initialization, transport, snapshot store setup
├── fsm.go           ← MetaServer implements raft.FSM: Apply(), Snapshot(), Restore()
├── raft_rpc.go      ← New gRPC handlers: JoinCluster, LeaderInfo, ClusterStatus
└── snapshotter.go   ← FSM snapshot encoding/decoding (reuse persistedState struct)
```

### 3.3 FSM Design: What Goes Through Raft, What Doesn't

**Key architectural insight — the MetaServer is a session broker, not a data path:**

```
Client ──► MetaServer  (Navigate / GetRoots)   ← contacted ONCE at session setup
                │
                └─ returns FileServer address
                          │
Client ──────────────────► FileServer           ← ALL actual file I/O goes here directly
                                                   MetaServer is never contacted again
```

The MetaServer is only ever called:
1. **Once at login** — `GetRoots` to list the user's available roots
2. **Once per session** — `Navigate` to get the target FileServer's address
3. **Occasionally** — `RootShare` / `RootUnshare` when sharing a directory

All reads, writes, mkdir, delete, rename, and streaming operations go **directly between the client and the FileServer** — the MetaServer is completely out of the hot path. This means even if Raft commit adds 10–20ms of latency to write operations, users will never feel it: it only affects the brief one-time session setup, not ongoing file I/O.

This also justifies **tuning Raft election timeouts more conservatively** (e.g. 1–2 seconds instead of the typical 150–500ms) on the Raspberry Pi hardware — a longer election window is perfectly acceptable since the MetaServer is only needed at session start, not during active file operations.

**Critical Design Decision:** Not every MetaServer operation needs to go through the Raft log. The Raft log is for operations that must be **durably replicated** (i.e., if the leader crashes mid-operation, the committed state must be recoverable on a new leader). Heartbeat frequency is high (every 5s per fileserver) — routing all heartbeats through Raft would add unnecessary latency.

| Operation | Raft Log? | Reasoning |
|-----------|-----------|-----------|
| `RegisterFileServer` | ✅ YES | Persists new FS mapping — must be replicated |
| `DeregisterFileServer` | ✅ YES | Removes FS mapping — must be replicated |
| `GetRoots` / user auto-provisioning | ✅ YES | Creates new `users` entry — must be replicated |
| `RootShare` / `RootUnshare` | ✅ YES | Modifies ACL entries — must be replicated |
| `Navigate` (lookup only) | ❌ NO | Read-only lookup, no state change; Leader serves directly |
| `Heartbeat` | ❌ NO | High-frequency; Leader updates in-memory `LastHeartbeatUnix` only; stale-marking is a local Leader decision |

**Heartbeat handling:** The Leader processes heartbeats locally (in memory + disk on the Leader only, exactly as today). Heartbeat state does **not** need to survive Leader failover — when a new Leader is elected, FileServers will send new heartbeats within 5 seconds and re-establish liveness. During the ~5–15s leader election window, no new user connections will be routed (Navigate returns "leader unavailable"), but existing FileServer sessions continue serving files uninterrupted.

### 3.4 Raft Log Commands

Define a `RaftCommand` struct in `internal/metaserver/raft.go`:

```go
type CommandType uint8

const (
    CmdRegisterFileServer   CommandType = 1
    CmdDeregisterFileServer CommandType = 2
    CmdProvisionUser        CommandType = 3  // GetRoots auto-provision
    CmdRootShare            CommandType = 4
    CmdRootUnshare          CommandType = 5
)

type RaftCommand struct {
    Type    CommandType     `json:"type"`
    Payload json.RawMessage `json:"payload"`
}
```

Each write RPC handler on the Leader serializes the command as JSON, calls `raft.Apply(encodedCmd, timeout)`, and waits for the commit. On commit, the FSM's `Apply()` method deserializes and mutates the in-memory state.

### 3.5 FSM Interface Implementation

`internal/metaserver/fsm.go` — `MetaServer` implements `raft.FSM`:

```go
// Apply is called once a log entry is committed to a quorum.
func (ms *MetaServer) Apply(log *raft.Log) interface{} { ... }

// Snapshot is called to create a point-in-time snapshot of the state machine.
// Reuse existing persistedState struct with JSON encoding.
func (ms *MetaServer) Snapshot() (raft.FSMSnapshot, error) { ... }

// Restore replaces the FSM state with the contents of a snapshot.
func (ms *MetaServer) Restore(rc io.ReadCloser) error { ... }
```

Snapshots use the **existing `persistedState` JSON schema** — zero schema changes needed.

### 3.6 Raft Transport

Use `hashicorp/raft`'s **TCP transport** (`raft.NewTCPTransport`). Each MetaServer node listens on a dedicated Raft port: **`port+1`** (default: `50053`). This keeps Raft traffic separate from client gRPC traffic on port `50051`.

> Do NOT reuse the existing gRPC port for Raft. Raft's TCP transport is binary-framed and incompatible with gRPC/HTTP2.

### 3.7 Follower RPC Handling

Followers **do not serve write RPCs**. When a Follower receives a write RPC (e.g. `RegisterFileServer`, `RootShare`), it returns:

```
gRPC Status: FAILED_PRECONDITION
Message: "not the leader; redirect to <leader_address>:50051"
```

The `leader_address` is obtained from `raft.Leader()` on the Follower node.

**Read RPCs** (`Navigate`, `GetRoots` when user already exists) MAY be served by Followers. Since heartbeats don't go through Raft, the Follower may have a slightly stale view of which FileServers are currently healthy — but this is acceptable (user was recently navigated to a FS; it's almost certainly still healthy).

### 3.8 New Proto RPCs

Add to `api/metaserver/metaserver.proto`:

```protobuf
// Internal cluster management RPCs (cluster-auth only)
message JoinClusterRequest {
  string node_id  = 1;  // e.g. "dvfs8"
  string raft_addr = 2; // e.g. "10.0.172.48:50053"
}
message JoinClusterResponse {
  bool   success = 1;
  string error   = 2;
}

message LeaderInfoRequest {}
message LeaderInfoResponse {
  bool   is_leader    = 1;
  string leader_id    = 2;  // e.g. "dvfs1"
  string leader_addr  = 3;  // e.g. "10.0.171.40:50051"
  string raft_state   = 4;  // "Leader", "Follower", "Candidate"
  repeated string peers = 5;
}

rpc JoinCluster(JoinClusterRequest) returns (JoinClusterResponse);
rpc LeaderInfo(LeaderInfoRequest) returns (LeaderInfoResponse);
```

### 3.9 New Startup Flags for MetaServer

```
-raft_enabled          bool    (default: false)
-raft_node_id          string  (e.g. "dvfs1")
-raft_bind_addr        string  (e.g. "10.0.171.40:50053")
-raft_peers            string  (comma-separated "dvfs8=10.0.172.48:50053,dvfs9=...")
-raft_data_dir         string  (default: "./bin/raft/")
-raft_bootstrap        bool    (only set on first-ever cluster startup)
-raft_election_timeout duration (default: 500ms)
-raft_heartbeat_timeout duration (default: 150ms)
```

### 3.10 Bootstrapping the Cluster (First Startup)

1. Start dvfs1 with `-raft_enabled -raft_node_id=dvfs1 -raft_bootstrap`.
2. dvfs1 bootstraps the Raft cluster as the sole voter and immediately becomes Leader.
3. Start dvfs8 with `-raft_enabled -raft_node_id=dvfs8 -raft_peers=dvfs1=10.0.171.40:50053`.
4. dvfs8 sends `JoinCluster` RPC to dvfs1; dvfs1 calls `raft.AddVoter("dvfs8", "10.0.172.48:50053", 0, timeout)`.
5. dvfs8 receives the full FSM snapshot from dvfs1 and becomes a Follower.
6. Repeat for dvfs9.
7. **Remove `-raft_bootstrap` from dvfs1's startup flags** after initial cluster formation.

> **Important:** `-raft_bootstrap` must be set on exactly ONE node exactly ONCE. On subsequent restarts, nodes rejoin automatically using the persisted Raft log (BoltDB).

### 3.11 Dynamic Membership (Hardware Replacement)

To replace a failed control node (e.g. dvfs8) with a new machine (dvfs10) without downtime:

1. Start dvfs10 with `-raft_enabled -raft_node_id=dvfs10`.
2. Call `JoinCluster` on the current Leader with dvfs10's Raft address.
3. Wait for dvfs10 to sync (`raft.VerifyLeader()` and watch logs).
4. Send `RemoveVoter("dvfs8")` to the Leader (via admin CLI or API call).
5. dvfs8 is safely evicted. dvfs10 is now a full voter.

### 3.12 Cluster Restart & Reboot Behavior

This section covers what happens when you run `sudo reboot` on the machines — the most common operational scenario.

#### The Golden Rule: `-raft_bootstrap` is NEVER in the systemd service file after initial setup

The `-raft_bootstrap` flag must be **removed** from dvfs1's service/env after the first cluster formation. If a node restarts with `-raft_bootstrap`, it will try to form a **brand new single-node cluster**, throwing away all Raft history. This is catastrophic. Normal restarts work purely from the BoltDB log on disk — no bootstrap flag needed.

---

#### Scenario A: Full cluster reboot (`sudo reboot` on all 3 nodes simultaneously)

This is your most common case. Here's exactly what happens:

```
All 3 nodes go down
        ↓
All 3 nodes come back up (systemd starts dvfs-metaserver.service on each)
        ↓
Each node loads its Raft log from BoltDB — knows its last term, last vote, and peer list
        ↓
All nodes start as Followers, begin election timeout countdown
        ↓
First node to timeout starts an election, votes for itself, requests votes from peers
        ↓
First node to get 2/3 votes becomes the new Leader (~1–3 seconds)
        ↓
FileServers re-register within ~3s (their own systemd restart fires simultaneously)
```

✅ **Works automatically. No manual intervention needed.** The node that wins the election is not necessarily the old Leader — whichever boots fastest wins the race. That's fine; all nodes have identical committed state in BoltDB.

---

#### Scenario B: Two nodes come up, one is slow / still booting

```
dvfs1 + dvfs8 both up → 2/3 votes → quorum → Leader elected, serving traffic
dvfs9 still booting...
        ↓
dvfs9 boots, discovers peers, receives missing log entries (or full snapshot)
dvfs9 becomes a Follower, joins seamlessly
```

✅ **Works automatically.** Cluster serves traffic from the moment 2 nodes are up — no waiting for the third.

---

#### Scenario C: Only one node comes up (other two are stuck / slow)

```
dvfs1 alone → only 1/3 votes → NO quorum → cannot elect a Leader
dvfs1 keeps holding elections (increasing backoff), stays in Candidate state
MetaServer process is running but refuses all write RPCs (returns "no quorum")
        ↓
When dvfs8 or dvfs9 comes back → 2/3 votes → quorum → Leader elected in ~1–3s
```

⚠️ During the single-node window, the MetaServer is unavailable for **new session setup only** (Navigate, GetRoots for new users, RootShare). **Existing FileServer sessions are completely unaffected** — all file I/O goes directly to FileServers and never touches the MetaServer.

---

#### Scenario D: Staggered reboot (rebooting one machine at a time)

This is the **safest** way to do planned maintenance reboots:

```
Reboot dvfs1 (Leader) → dvfs8 or dvfs9 immediately wins election (2/3 quorum stays)
Cluster keeps serving during dvfs1's ~30s reboot
dvfs1 comes back → rejoins as Follower, syncs from new Leader
        ↓
Reboot dvfs8 → same thing, cluster stays up
        ↓
Reboot dvfs9 → same thing
```

✅ **Zero downtime** if you reboot one at a time. Recommended for any planned maintenance window.

---

#### Scenario E: Dynamic IPs change after reboot (the tricky one for your cluster)

Your cluster uses **dynamic DHCP IPs**. A node may reboot and get a different IP. The Raft TCP transport (`RAFT_BIND_ADDR`) is set at startup — if a peer's IP changed, the other nodes' stored address for that peer is now stale.

**Problem:** dvfs8 reboots, gets a new IP. dvfs1 and dvfs9 still have dvfs8's old IP in their Raft peer config and can't reach it.

**Solution:** The `start-metaserver.sh` startup script resolves both its own IP and all peer IPs **dynamically at startup** from the Gist (same pattern `start-fileserver.sh` already uses):

```bash
# Detect own LAN IP dynamically
OWN_IP=$(ip -4 route get 8.8.8.8 | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')
RAFT_BIND_ADDR="${OWN_IP}:50053"

# Resolve peer Raft addresses from Gist (all nodes tagged "metaserver" except self)
RAFT_PEERS=$(curl -sSL "$GIST_MACHINES_URL" | python3 -c "
import sys, json
data = json.load(sys.stdin)
peers = [m['username']+'='+m['ip']+':50053'
         for m in data
         if 'metaserver' in m.get('services', []) and m['username'] != '$RAFT_NODE_ID']
print(','.join(peers))
")
```

Every restart auto-resolves current IPs from the Gist so Raft peers are always up-to-date.

> **Edge case:** If IPs change AND the Gist hasn't refreshed yet (within the 60-min window), peers may have stale Raft addresses. In that worst case, run `sudo systemctl start dvfs-gist` manually on any control node to trigger an immediate Gist push, then restart the MetaServer services.

---

#### Systemd: Change `Restart=on-failure` → `Restart=always`

Update `scripts/dvfs-metaserver.service`:

```ini
Restart=always
RestartSec=5s
```

`Restart=on-failure` won't restart the service if it exits cleanly (e.g. after a graceful shutdown before reboot). `Restart=always` ensures the MetaServer always comes back up and rejoins the Raft cluster, which is what you want for HA.

---

## 4. MetaServer HTTP `/leader` Endpoint

The MetaServer exposes a new unauthenticated HTTP endpoint (on the same port as the gRPC server via `grpc.ServeHTTP` mux, or a separate HTTP port like `8090`):

```
GET /leader
→ { "is_leader": true,  "leader_id": "dvfs1", "leader_addr": "10.0.171.40:50051" }
→ { "is_leader": false, "leader_id": "dvfs8", "leader_addr": "10.0.172.48:50051" }
```

This endpoint is used by:
- **`update_gist.py`**: Polls `/leader` before running; exits immediately if `is_leader == false`
- **Admin UI**: Displays which node is the current Raft Leader
- **Fileserver startup script**: Can use this to find the Leader's address dynamically

---

## 5. GitHub Gist & Service Registry

### 5.1 Updated `machines.json` Schema

Add a `services` field to each node entry (fully backward-compatible — old clients ignore unknown fields):

```json
[
  {
    "username": "dvfs1",
    "mac": "28:cd:c1:0e:25:70",
    "ip": "10.0.171.40",
    "last_seen": "2026-09-30T17:00:00Z",
    "services": ["metaserver", "admin", "fileserver"]
  },
  {
    "username": "dvfs2",
    "mac": "28:cd:c1:0e:25:71",
    "ip": "10.0.171.41",
    "last_seen": "2026-09-30T17:00:00Z",
    "services": ["fileserver"]
  },
  ...
  {
    "username": "dvfs8",
    "mac": "...",
    "ip": "10.0.172.48",
    "last_seen": "2026-09-30T17:00:00Z",
    "services": ["metaserver", "admin", "fileserver"]
  },
  {
    "username": "dvfs9",
    "mac": "...",
    "ip": "...",
    "last_seen": "2026-09-30T17:00:00Z",
    "services": ["metaserver", "admin", "fileserver"]
  }
]
```

### 5.2 Updated `GistNode` Go Struct

```go
type GistNode struct {
    Username string   `json:"username"`
    MAC      string   `json:"mac"`
    IP       string   `json:"ip"`
    LastSeen string   `json:"last_seen"`
    Services []string `json:"services,omitempty"`  // NEW: e.g. ["metaserver","admin","fileserver"]
}
```

### 5.3 Updated `update_gist.py` — Leader-Only Execution

Add a leader check at the start of `main()` before running any Tailscale or SSH queries:

```python
import requests

def is_local_metaserver_leader(port=8090, timeout=2):
    """Return True only if the locally running MetaServer reports itself as Leader."""
    try:
        resp = requests.get(f"http://127.0.0.1:{port}/leader", timeout=timeout)
        resp.raise_for_status()
        data = resp.json()
        return data.get("is_leader", False)
    except Exception as e:
        print(f"Leader check failed: {e}")
        return False  # Fail safe: don't push if unsure

def main():
    if not is_local_metaserver_leader():
        print("Not the Raft Leader — skipping Gist update.")
        return
    # ... existing Tailscale discovery + Gist push code ...
```

Deploy `dvfs-gist.service` + `dvfs-gist.timer` to all 3 control nodes (dvfs1, dvfs8, dvfs9). They all run on a 60-minute timer, but only the Leader actually pushes.

---

## 6. Client Discovery: Multi-MetaServer Support

### 6.1 Updated `GistNode` Parsing in `discovery.go`

Add a helper that returns all IPs tagged with a specific service:

```go
// MetaServerAddresses returns all node IPs in the Gist tagged with "metaserver".
func (d *DiscoveryResolver) MetaServerAddresses(port string) []string {
    d.mu.RLock()
    defer d.mu.RUnlock()
    // ... filter d.nodes by Services containing "metaserver" ...
}
```

The `DiscoveryResolver` must store full `[]GistNode` in addition to the `hostToIP`/`ipToHost` maps. Add:

```go
type DiscoveryResolver struct {
    // ... existing fields ...
    nodes    []GistNode  // NEW: raw nodes for service-tag filtering
}
```

### 6.2 Updated `ResolveMetaAddress` — Probing Multiple MetaServers

**Remove** the hardcoded `dvfs1` lookup. Replace with:

```go
// ResolveMetaAddress probes all MetaServer nodes in parallel and returns the Leader's address.
// Falls back to the first responding node if none advertises leadership.
func (d *DiscoveryResolver) ResolveMetaAddress(port string) (string, error) {
    addrs := d.MetaServerAddresses(port)
    if len(addrs) == 0 {
        // Backward compatibility: fall back to dvfs1
        ip, err := d.Lookup("dvfs1")
        if err != nil { return "", err }
        return ip + ":" + port, nil
    }
    return probeForLeader(addrs, port) // parallel probe, returns leader
}
```

`probeForLeader` sends concurrent `LeaderInfo` gRPC calls to all known MetaServer addresses and returns the address of whichever replies `is_leader: true` first. Timeout: 3 seconds.

### 6.3 Client "Not Leader" Redirect Handling

When a client receives a `FAILED_PRECONDITION` error with the Leader's address embedded:

```go
// In internal/client/msclient.go, wrap every write RPC with:
func (c *MSClient) callWithLeaderRedirect(ctx context.Context, fn func(addr string) error) error {
    addr := c.currentAddr
    for attempts := 0; attempts < 3; attempts++ {
        err := fn(addr)
        if err == nil { return nil }
        // Check if it's a "not leader" error
        if leaderAddr, ok := extractLeaderAddr(err); ok {
            addr = leaderAddr
            continue
        }
        return err
    }
    return fmt.Errorf("exceeded redirect limit")
}
```

`Navigate` and `GetRoots` are read operations — they can be served by any MetaServer node (Followers may have slightly stale heartbeat state but not stale user/share mappings). Write operations (`RootShare`, `RootUnshare`) must be retried on the Leader.

---

## 7. FileServer: Multi-MetaServer Heartbeat Support

### 7.1 Problem

`internal/fileserver/msclient.go` currently dials a single `-meta_addr` (hardcoded at startup). If the Leader changes, the FileServer's heartbeat connection breaks.

### 7.2 Solution: Leader-Aware MetaServer Client

Modify `StartMetaServerSync` to:

1. Accept a **list** of MetaServer addresses (resolved from the Gist at startup).
2. On heartbeat failure or registration failure, probe the list for the current Leader using `LeaderInfo` gRPC call.
3. Reconnect to the new Leader address.

```go
// internal/fileserver/msclient.go
type MetaServerSync struct {
    msAddrs     []string  // all known MetaServer addresses
    currentAddr string    // current leader address
    // ...
}

func (s *MetaServerSync) findLeader(ctx context.Context) (string, error) {
    for _, addr := range s.msAddrs {
        // Call LeaderInfo; return addr of node reporting is_leader=true
    }
    return "", fmt.Errorf("no leader found")
}
```

### 7.3 Updated `start-fileserver.sh`

The startup script currently resolves `dvfs1`'s IP and uses it as the sole `META_ADDR`. Update to resolve all MetaServer node IPs:

```bash
# Resolve all MetaServer IPs from the Gist
META_ADDRS=$(curl -sSL --connect-timeout 5 "$GIST_MACHINES_URL" | python3 -c '
import sys, json
data = json.load(sys.stdin)
addrs = [m["ip"] + ":50051" for m in data if "metaserver" in m.get("services", [])]
print(",".join(addrs))
')

# Pass as comma-separated list
exec ./bin/fileserver -meta_addr="${META_ADDRS}" ...
```

FileServer binary accepts `-meta_addr` as a comma-separated list of addresses.

---

## 8. Admin Server: gRPC-Based State Access

### 8.1 Current Problem

`internal/admin/poller.go`'s `refreshNodes()` reads `metaserver_state.json` **directly off the local filesystem**. In a Raft cluster, the local node may be a Follower — its state file is valid but may be slightly behind if it hasn't applied the latest committed log entries yet.

### 8.2 Solution

Replace the direct file read with a gRPC call to the MetaServer (`LeaderInfo` + new `GetClusterState` RPC). The Admin Server always queries the **current Leader** for authoritative state.

Add to `api/metaserver/metaserver.proto`:

```protobuf
message ClusterStateRequest {}
message ClusterStateResponse {
  repeated FileServerEntry fileservers = 1;
  map<string, uint64> users = 2;
  // ... mirrors persistedState
}

rpc GetClusterState(ClusterStateRequest) returns (ClusterStateResponse);
```

The Admin Server's `refreshNodes()` calls `GetClusterState` on the Leader (using the `msAddr` field + redirect logic). This eliminates the direct file dependency entirely.

> The Admin Server can still fall back to reading the local file if the gRPC call fails (e.g. entire control plane is down during a major outage) — but gRPC is the primary source of truth.

### 8.3 Admin UI Updates

`cmd/admin/ui/src/pages/Nodes.tsx` (and related pages) should display:

- **Raft cluster status panel**: a new section showing all 3 MetaServer nodes, their Raft state (Leader/Follower/Offline), and current term number.
- The current **Leader** is visually highlighted (e.g. gold star badge).
- If `raft_enabled=false` on the MetaServer (single-node mode), the panel is hidden.

Data sourced from the `LeaderInfo` gRPC endpoint, polled every 10 seconds.

---

## 9. SSH Executor — Already Solved

The plan's Section 4 (SSH StrictHostKeyChecking) is **already implemented**. `internal/admin/ssh_executor.go` uses Go's native `golang.org/x/crypto/ssh` library with `HostKeyCallback: ssh.InsecureIgnoreHostKey()` — the programmatic equivalent of `StrictHostKeyChecking=no`. No changes needed here.

---

## 10. mTLS and Certificate Considerations

The existing mTLS system already generates certificates for `dvfs1`–`dvfs9`. Control nodes dvfs8 and dvfs9 already have valid certs in `deploy_certs/dvfs8/` and `deploy_certs/dvfs9/`. The cluster auth interceptor already accepts CN/SAN matching `dvfs*`.

**New certs needed:** None — existing cert infrastructure covers all 9 nodes.

**Raft TCP transport TLS:** The Raft transport (port 50053) should also be TLS-secured between control nodes. `hashicorp/raft` supports custom transport wrappers. Use the existing `ca.crt` + per-node certs.

---

## 11. Systemd Service Updates

### 11.1 New MetaServer Service Flags

`scripts/start-metaserver.sh` — add Raft flags:

```bash
RAFT_ENABLED="${RAFT_ENABLED:-false}"
RAFT_NODE_ID="${RAFT_NODE_ID:-}"
RAFT_BIND_ADDR="${RAFT_BIND_ADDR:-}"
RAFT_PEERS="${RAFT_PEERS:-}"
RAFT_DATA_DIR="${RAFT_DATA_DIR:-./bin/raft/}"
RAFT_BOOTSTRAP="${RAFT_BOOTSTRAP:-false}"

exec ./bin/metaserver \
  -port="${META_PORT}" \
  -state_file="${STATE_FILE}" \
  ...existing flags... \
  -raft_enabled="${RAFT_ENABLED}" \
  -raft_node_id="${RAFT_NODE_ID}" \
  -raft_bind_addr="${RAFT_BIND_ADDR}" \
  -raft_peers="${RAFT_PEERS}" \
  -raft_data_dir="${RAFT_DATA_DIR}" \
  -raft_bootstrap="${RAFT_BOOTSTRAP}"
```

### 11.2 Environment Overrides in `.service` Files

Each control node's systemd drop-in (`/etc/systemd/system/dvfs-metaserver.service.d/override.conf`) sets the node-specific Raft config:

**dvfs1:**
```ini
[Service]
Environment=RAFT_ENABLED=true
Environment=RAFT_NODE_ID=dvfs1
Environment=RAFT_BIND_ADDR=10.0.171.40:50053
Environment=RAFT_PEERS=dvfs8=10.0.172.48:50053,dvfs9=<dvfs9_ip>:50053
```

**dvfs8:**
```ini
[Service]
Environment=RAFT_ENABLED=true
Environment=RAFT_NODE_ID=dvfs8
Environment=RAFT_BIND_ADDR=10.0.172.48:50053
Environment=RAFT_PEERS=dvfs1=10.0.171.40:50053,dvfs9=<dvfs9_ip>:50053
```

### 11.3 Gist Timer Deployment

Deploy `scripts/rp_115/dvfs-gist.service` and `scripts/rp_115/dvfs-gist.timer` to dvfs1, dvfs8, and dvfs9. The updated `update_gist.py` polls `/leader` before running, so all three can have the timer running — only the Leader actually pushes.

> **Current state:** The gist updater ran from a separate Pi (RP-115). After this migration, it runs from the MetaServer control nodes directly, since Tailscale is already on all nodes tagged `tag:dvfsmachines`.

---

## 12. Implementation Phases

### Phase 1: Foundation (No Raft Yet)
**Goal:** Prepare the codebase so Raft can be added without breakage. Cluster keeps working exactly as today throughout this phase.

**Proto changes:**
- [ ] Add new proto messages to `api/metaserver/metaserver.proto`: `LeaderInfoRequest/Response`, `JoinClusterRequest/Response`, `ClusterStateRequest/Response`
- [ ] **Regenerate Go proto files:** `protoc --go_out=. --go-grpc_out=. api/metaserver/metaserver.proto` (missing this step will cause compile errors everywhere that imports the pb package)

**MetaServer stubs (backward-compatible, Raft not active):**
- [ ] Add `LeaderInfo` stub gRPC handler to `internal/metaserver/handler.go` — returns `{is_leader: true, state: "Leader", leader_id: ""}` always
- [ ] Add `/leader` HTTP endpoint to MetaServer returning `{"is_leader": true}` always (stub for single-node mode)
- [ ] Add all new Raft startup flags (`-raft_enabled`, `-raft_node_id`, etc.) to `cmd/metaserver/main.go` — **gated behind `-raft_enabled=false` default**, no behavior change yet
- [ ] Add `DeregisterFileServer` to the auth whitelist for the new `LeaderInfo`/`JoinCluster` RPCs in `auth_google.go` (cluster-auth only)

**Discovery & Gist schema:**
- [ ] Add `Services []string` to `GistNode` struct in `internal/client/discovery.go`
- [ ] Add `MetaServerAddresses(port string) []string` helper to `DiscoveryResolver` — filters `nodes` by `"metaserver" in Services`; **falls back to returning `["dvfs1_ip:port"]` if no node has the services tag** (backward-compat — Gist may not have the field yet)
- [ ] Store raw `[]GistNode` on `DiscoveryResolver` (add `nodes []GistNode` field) so `MetaServerAddresses` can filter by service tag
- [ ] Update `update_gist.py` to emit `"services": ["metaserver", "admin", "fileserver"]` for control nodes and `["fileserver"]` for others — **keep existing fields unchanged** so old clients still work

**Startup scripts:**
- [ ] Update `scripts/start-fileserver.sh` to resolve `META_ADDRS` from Gist (all nodes tagged `metaserver`). **Must include fallback:** if the curl/parse returns empty (Gist not yet updated), fall back to the existing `dvfs1` resolution logic — do NOT break existing fileservers
- [ ] Update `scripts/start-metaserver.sh` to: (a) detect own LAN IP dynamically for `RAFT_BIND_ADDR`, (b) resolve `RAFT_PEERS` from the Gist at startup — so IPs are always fresh after a reboot (addresses §3.12 Scenario E)
- [ ] Change `Restart=on-failure` → `Restart=always` and `RestartSec=3s` → `RestartSec=5s` in `scripts/dvfs-metaserver.service`

**Fileserver flag interface:**
- [ ] Change `cmd/fileserver/main.go` `-meta_addr` flag from `flag.String` to accept a comma-separated list. Parse into `[]string` and pass to `StartMetaServerSync`. **Backward-compatible:** a single address (existing usage) is just a one-element list.

**Gist updater:**
- [ ] Add `is_local_metaserver_leader()` check to `update_gist.py` (polls `http://127.0.0.1:8090/leader`). In stub mode, MetaServer always responds `is_leader: true` — so behavior is unchanged on the single-node cluster during Phase 1.

**Testable result:** Cluster works exactly as before. All existing tests pass. Multi-MetaServer code paths exist but produce identical behavior to today (single-node stubs everywhere).

---

### Phase 2: Raft Integration
**Goal:** MetaServer forms a Raft cluster; writes go through Raft FSM.

**Dependencies:**
- [ ] `go get github.com/hashicorp/raft@latest`
- [ ] `go get github.com/hashicorp/raft-boltdb/v2@latest`
- [ ] `go get go.etcd.io/bbolt@latest`
- [ ] Run `go mod tidy` and verify `go.sum` is updated

**Core Raft implementation:**
- [ ] Implement `internal/metaserver/fsm.go` — `MetaServer` implements `raft.FSM`:
  - `Apply(*raft.Log) interface{}` — deserializes `RaftCommand`, dispatches to the appropriate locked mutation method
  - `Snapshot() (raft.FSMSnapshot, error)` — encodes current state as `persistedState` JSON
  - `Restore(io.ReadCloser) error` — decodes snapshot JSON and replaces in-memory state
- [ ] Implement `internal/metaserver/raft.go` — `InitRaft(config RaftConfig) (*raft.Raft, error)`:
  - BoltDB log store + stable store in `raft_data_dir/`
  - File snapshot store in `raft_data_dir/snapshots/`
  - TCP transport on `raft_bind_addr` (port 50053) — **wrap with TLS using existing `ca.crt` + node cert**
  - If `-raft_bootstrap=true`, call `raft.BootstrapCluster` (single-voter config)
- [ ] Implement `internal/metaserver/raft_rpc.go`:
  - Real `JoinCluster` handler — calls `raft.AddVoter(nodeID, raftAddr, 0, timeout)`; **only works on Leader, returns FAILED_PRECONDITION with leader addr otherwise**
  - Real `LeaderInfo` handler — returns `raft.State()`, `raft.Leader()`, current term

**Handler refactoring (the most critical part):**
- [ ] Wrap `RegisterFileServer` handler: serialize as `RaftCommand{CmdRegisterFileServer}`, call `raft.Apply()`, block until committed, then return result
- [ ] Wrap `DeregisterFileServer` handler: same pattern with `CmdDeregisterFileServer`
- [ ] Wrap `RootShare` / `RootUnshare` handlers: same pattern
- [ ] Refactor `GetRoots` handler carefully — **split into two paths:**
  - If user already exists in `ms.users` → **local read only**, no Raft (serve from current state)
  - If user is NEW → go through Raft with `CmdProvisionUser` to atomically assign them a FileServer
- [ ] Keep `Heartbeat` as a Leader-local operation (update in-memory + disk, no Raft log)
- [ ] Keep `Navigate` as a Leader-local operation — **note: Navigate calls `markStaleFileServersLocked()` which may write stale status to disk; this is intentionally Leader-only and NOT replicated via Raft** (Followers reconnecting FileServers will re-establish liveness within seconds anyway)
- [ ] Add Follower redirect: any write RPC received by a Follower returns `codes.FailedPrecondition` with message `"not leader; redirect to <leaderAddr>:50051"`

**Test suite fixes (REQUIRED — existing tests will break without this):**
- [ ] Add a `newTestMetaServerWithRaft(t)` helper that initializes a single-node in-memory Raft cluster (`raft_bootstrap=true`, in-memory transport, in-memory log store) — used by all existing handler tests so they continue to work
- [ ] Update `internal/metaserver/metaserver_test.go` and `metaserver_edge_test.go` to use the new test helper — all `handler.RegisterFileServer(...)` etc. calls now go through the Raft FSM path
- [ ] Add new `internal/metaserver/raft_test.go` for three-node cluster tests:
  - Leader election after one node killed
  - Write on Leader, read on Follower shows replicated state
  - Write on Follower returns redirect error with correct Leader address
  - Snapshot + restore round-trip preserves all state fields
- [ ] Run full test suite: `go test -tags use_google_auth ./internal/metaserver/...`

**Testable milestones:**
- [ ] Single-node Raft (`-raft_bootstrap`, no peers): all existing tests pass
- [ ] Three-node local cluster (separate ports on localhost): kill Leader, verify new election in <5s, writes resume on new Leader

---

### Phase 3: Client & FileServer Updates
**Goal:** Clients and FileServers transparently handle Leader changes without manual reconfiguration.

**Client — multi-metaserver discovery:**
- [ ] Implement `probeForLeader(addrs []string, port string, tlsCreds credentials.TransportCredentials) (string, error)` in `internal/client/discovery.go` — sends concurrent `LeaderInfo` gRPC calls (with mTLS credentials, 3s timeout), returns addr of whichever replies `is_leader: true` first. **Must use the same TLS cert pool** (`NewDVFSUniversalCertPool()`) as other client gRPC calls — plaintext probe will fail on the cluster.
- [ ] Replace `ResolveMetaAddress` hardcoded `dvfs1` lookup with `probeForLeader(MetaServerAddresses(port))`
- [ ] Update `cmd/client/main.go` to call `resolver.MetaServerAddresses(port)` instead of `nodes["dvfs1"]`
- [ ] Implement `callWithLeaderRedirect` wrapper in `internal/client/msclient.go` — applies to `RootShare` and `RootUnshare` (write RPCs); `GetRoots` and `Navigate` can be served by any node but retry on redirect too
- [ ] Cache the resolved Leader address on `MSClient` and reuse it (don't re-probe on every call — only on redirect or connection error)

**FileServer — leader-aware MetaServer sync:**
- [ ] Update `internal/fileserver/msclient.go`'s `dialMetaServer()` to accept `[]string` addresses
- [ ] Update `StartMetaServerSync` to accept `[]string` MetaServer addresses and track `currentAddr string`
- [ ] Implement `findLeader(ctx, addrs []string) (string, error)` — same parallel probe logic, using mTLS creds (SNI must be resolved per-address via `ResolveServerName`)
- [ ] On heartbeat failure: call `findLeader`, update `currentAddr`, reconnect — rather than the current behavior of just retrying the same dead address
- [ ] Update `cmd/fileserver/main.go` to split `-meta_addr` on commas → `[]string`, pass to `StartMetaServerSync`

**Tests:**
- [ ] `internal/fileserver/msclient_test.go` — test that `findLeader` correctly skips non-leader nodes and connects to the leader
- [ ] Integration test: run client + 3-node MetaServer cluster locally, kill Leader, verify client transparently reconnects and resumes operations

---

### Phase 4: Admin & UI Updates
**Goal:** Admin Server always reads authoritative state from the Raft Leader; UI shows cluster health.

**MetaServer — new RPC:**
- [ ] Implement `GetClusterState` gRPC handler in `internal/metaserver/handler.go` — returns current FSM state as `ClusterStateResponse` (fileservers map, users map, shared map). **Leader-only RPC** — Followers redirect same as write RPCs. Auth: admin password hash OR cluster mTLS peer cert.

**Admin Server — dynamic Leader tracking:**
- [ ] Add `currentMSLeader string` field and `msLeaderMu sync.RWMutex` to `AdminServer` struct — tracked separately from the static `msAddr` config
- [ ] Add `resolveLeader(ctx) (string, error)` method to AdminServer — calls `LeaderInfo` on all known MetaServer addresses (from Gist or config), caches result
- [ ] Update `refreshNodes()` in `internal/admin/poller.go` — call `GetClusterState` via gRPC on the current Leader address instead of reading `metaserver_state.json` directly. On `FAILED_PRECONDITION` (redirect), update `currentMSLeader` and retry on the new Leader
- [ ] Add `LeaderInfo` polling every 10 seconds in `pollAllNodes()` — updates `currentMSLeader` proactively so redirect loops are rare
- [ ] **Fallback:** if all gRPC calls fail (entire control plane down), fall back to reading local `metaserver_state.json` and log a warning — avoids Admin going completely dark during a major outage
- [ ] Add `GET /api/cluster` REST endpoint — returns Raft peer states (sourced from `LeaderInfo` calls to all known MetaServer nodes)

**Admin UI:**
- [ ] Add Raft cluster status panel to `cmd/admin/ui/src/pages/Nodes.tsx` (or a new `Cluster.tsx` page) — shows each control node, its Raft state (Leader/Follower/Offline), and current term
- [ ] Highlight the Leader with a visual indicator
- [ ] Hide the panel entirely if MetaServer returns `raft_enabled: false` in `LeaderInfo` (single-node mode compatibility)
- [ ] Rebuild frontend: `cd cmd/admin/ui && npm run build`

**Tests:**
- [ ] `internal/admin/poller_test.go` — mock `GetClusterState` gRPC, verify `refreshNodes` uses gRPC result and ignores state file
- [ ] Test redirect: mock a Follower returning `FAILED_PRECONDITION`, verify Admin retries on the embedded Leader address
- [ ] Kill Leader while Admin is running — verify Admin reconnects to new Leader within ~15s

---

### Phase 5: Deployment
**Goal:** Roll out to physical cluster — dvfs1, dvfs8, dvfs9.

**Secrets & prerequisites (do this BEFORE touching services):**
- [ ] Copy `/opt/dvfs/.env` from RP-115 to dvfs1, dvfs8, dvfs9 at `/opt/dvfs/.env` — contains Tailscale + GitHub Gist secrets needed by `update_gist.py`
- [ ] Ensure Python deps are installed on all 3 control nodes: `pip3 install requests python-dotenv`
- [ ] Verify Tailscale SSH keys from RP-115 are also present on dvfs1/dvfs8/dvfs9 (or set up new SSH keys — the script SSHes from the control node to other nodes to read LAN IPs)
- [ ] **Stop and disable the OLD gist timer on RP-115 BEFORE enabling it on control nodes** — otherwise RP-115 (which has no leader check) will race with the new leader-aware timers: `sudo systemctl disable --now dvfs-gist.timer dvfs-gist.service`

**Binary & config deployment:**
- [ ] Cross-compile new binaries with `-tags use_google_auth` for `linux/arm64`
- [ ] Copy binaries + updated scripts to dvfs8 and dvfs9 (they currently only have fileserver binaries)
- [ ] Deploy systemd drop-in overrides on all 3 control nodes (`/etc/systemd/system/dvfs-metaserver.service.d/override.conf`) with node-specific `RAFT_ENABLED`, `RAFT_NODE_ID` — **no `RAFT_BOOTSTRAP` here**
- [ ] Deploy `dvfs-gist.service` + `dvfs-gist.timer` to dvfs8 and dvfs9 (already exists on dvfs1/RP-115)
- [ ] Deploy updated `dvfs-admin.service` to dvfs8 and dvfs9
- [ ] `sudo systemctl daemon-reload` on all 3 control nodes

**Gist update (before cluster bootstrap):**
- [ ] Manually run `update_gist.py` once from dvfs1 (or any node with the secrets) to push the updated `machines.json` with `services` tags — this ensures FileServers can discover all 3 MetaServer IPs when they restart

**Bootstrap (one-time only):**
- [ ] Start dvfs1's MetaServer with `RAFT_BOOTSTRAP=true` temporarily (via env override or direct command): `RAFT_BOOTSTRAP=true ./bin/metaserver -raft_enabled=true -raft_node_id=dvfs1 ...`
- [ ] Verify dvfs1 is Leader: `curl http://dvfs1:8090/leader`
- [ ] Start MetaServer on dvfs8 (no bootstrap flag): `sudo systemctl start dvfs-metaserver` — it auto-joins via `RAFT_PEERS`
- [ ] Verify dvfs8 joined: check logs for `"raft: Added voter dvfs8"` on dvfs1
- [ ] Start MetaServer on dvfs9 — same
- [ ] Verify 3-node cluster: `curl http://dvfs1:8090/leader` should show 3 peers
- [ ] **Permanently remove `RAFT_BOOTSTRAP` from dvfs1** — it should never appear in the service file again

**Validation:**
- [ ] Enable gist timers: `sudo systemctl enable --now dvfs-gist.timer` on dvfs1, dvfs8, dvfs9
- [ ] Restart all FileServer services — they will now resolve all 3 MetaServer IPs from the Gist and connect to the Leader
- [ ] Test failover: `sudo systemctl stop dvfs-metaserver` on dvfs1 — verify dvfs8 or dvfs9 becomes Leader within 15s (`curl http://dvfs8:8090/leader`)
- [ ] Test client reconnect: start a client session, kill the current Leader, verify client transparently reconnects
- [ ] Test full cluster reboot: `sudo reboot` all 3 simultaneously — verify cluster reforms automatically and FileServers re-register within ~30s
- [ ] Monitor Admin UI — verify cluster panel shows correct Leader, all 3 nodes visible

---

## 13. Flaws in the Original Plan (Addressed Here)

| Original Plan Statement | Problem | Fix in This Plan |
|------------------------|---------|------------------|
| "SSH fingerprint prompts" problem | Already solved: Go SSH library uses `InsecureIgnoreHostKey()` | No action needed (§9) |
| "Update gist updater to exit if not Leader" | Didn't say HOW to check — race condition risk | Use HTTP `/leader` polling (§4, §5.3) |
| "Update `discovery.go` to parse new schema" | Didn't specify HOW — `ResolveMetaAddress` was hardcoded to `dvfs1` | Full rewrite of `ResolveMetaAddress` with parallel probing (§6) |
| "Fileservers ping the recognized Leader or broadcast" | Vague — `msclient.go` accepts one address; no retry to new leader on failure | Explicit multi-addr + `findLeader` logic (§7) |
| "Update Admin UI to display quorum status" | Didn't address the fact that Admin reads `metaserver_state.json` directly from disk | Replace file read with `GetClusterState` gRPC (§8) |
| "Implement RPC forwarding in `metaserver.proto`" | Didn't distinguish which RPCs need Raft vs which are local reads (heartbeats) | Explicit table of Raft vs. local operations (§3.3) |
| "Phase 1: Machine Selection" | Named dvfs1-dvfs3; user wants dvfs1, dvfs8, dvfs9 | Corrected throughout this plan |
| No mention of Raft storage backend | BoltDB vs in-memory is critical for crash recovery | BoltDB (raft-boltdb/v2) chosen and justified (§3.1) |
| No mention of Raft port | Raft TCP transport can't share gRPC port | Dedicated port 50053 for Raft (§3.6) |
| "gist updater on RP-115" | Plan missed that Tailscale is already on all 8 dvfs nodes — the updater can move to control nodes | Gist timer deployed to dvfs1+dvfs8+dvfs9 (§11.3) |

---

## 14. Go Dependency Additions

```bash
go get github.com/hashicorp/raft@latest
go get github.com/hashicorp/raft-boltdb/v2@latest
go get go.etcd.io/bbolt@latest
```

Add to `go.mod` under the existing dependencies. All packages support `linux/arm64` (Raspberry Pi).

---

## 15. Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| BoltDB corruption on Pi SD card | Low | High | Use SSD storage for Raft data dir; BoltDB has fsync-based durability |
| Split-brain during network partition | Very Low | High | Raft quorum requirement prevents split-brain by design |
| Raft election takes >30s | Low | Medium | Tune `raft_election_timeout` and `raft_heartbeat_timeout` |
| Client stuck talking to Follower | Low | Low | `callWithLeaderRedirect` handles this transparently |
| Gist pushed by multiple Leaders simultaneously | Very Low | Low | Leader check + 60min timer with randomized delay make this astronomically unlikely |
| Metaserver state diverges between nodes | Not Possible | — | FSM guarantees identical state after commit on all nodes |
