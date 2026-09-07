# Remote Orchestration, Logs & Alerting

This document details the remote cluster orchestration, live log streaming, and automated alerting engine in DVFS, divided into its operational philosophy (**In Essence**) and its backend implementation (**Implementation Quirks**).

---

## 1. In Essence: Unified Cluster Management

Operating a multi-node storage cluster across separate physical machines requires continuous supervision and rapid remediation. Manually SSHing into individual nodes to view logs, restart failed daemons, or execute system upgrades is error-prone.

DVFS integrates remote orchestration directly into the Admin Console:

```
[Admin Web Console]
        |
        v
[Admin Backend Orchestrator]
   |                  |
   v                  v
[Alert Engine]   [Remote SSH Executor]
(State Machine)       |
                      +---> dvfs1 (MetaServer): systemctl restart / journalctl
                      |
                      +---> dvfs2 (FileServer): git pull / make build / reboot
                      |
                      +---> dvfs3 (FileServer): apt update && apt upgrade
```

### Core Invariants
1. **Automated Alert State Machine**: Rather than firing duplicate alerts on every 5-second scrape, the alert engine deduplicates active conditions, maintains alert lifecycles, and automatically marks alerts resolved when nominal operating metrics return.
2. **Atomic Batch Orchestration**: When issuing commands across multiple cluster nodes simultaneously, the orchestrator acquires node locks in batch. If any node cannot be locked, previous locks are rolled back to prevent deadlocks.
3. **Auditability**: Every administrative action is logged to persistent JSON storage with timestamps, target nodes, operator identity, exit codes, and stdout/stderr output.

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Remote SSH Executor (`internal/admin/ssh_executor.go`)
Cluster commands are executed over SSH using public-key authentication:
- **Key Configuration**: Default path `~/.ssh/id_ed25519` (configurable via `-ssh_key`).
- **Target Resolution**: Node IPs are resolved dynamically from the MetaServer state file or GitHub Gist.
- **Privilege Delegation**: The remote user executes commands under `/etc/sudoers.d/dvfs`, which allows passwordless execution of `systemctl`, `journalctl`, `reboot`, and `apt`.
- **Supported Operations (`internal/admin/actions.go`)**:
  - `pull` (`ActionPull`): Runs `git pull origin <branch>` to update cluster source code.
  - `build` (`ActionBuild`): Runs `make <target>` (default: `make build`) to recompile binaries on remote nodes.
  - `restart` (`ActionRestart`): Restarts cluster services. Supports `RestartMode`: `"systemctl"` (default: `sudo systemctl restart dvfs-<service>`) or `"binary"` (launches compiled binaries directly with `NodeRestartParams`). `TargetService` can target `"fileserver"`, `"metaserver"`, `"admin"`, or `"all"`.
  - `reboot` (`ActionReboot`): Reboots the remote physical host machine (`sudo reboot`).
  - `apt` (`ActionApt`): Runs system package updates. Supports `AptMode`: `"update_upgrade"` (default: `sudo apt update && sudo apt upgrade -y`) or `"update_only"`.
  - `logs` (`ActionLogs`): Fetches service logs (`journalctl` or direct log file tailing).
  - `custom` (`ActionCustom`): Executes custom administrative shell commands with configurable `TimeoutSeconds`.

### 2.2 The Alert Engine (`internal/admin/alerts.go`)
The alert engine evaluates scraped metrics every 5 seconds:

```go
type Alert struct {
    ID         string        `json:"id"`
    Timestamp  int64         `json:"timestamp"`
    Severity   AlertSeverity `json:"severity"` // "critical", "warning", "info"
    Type       AlertType     `json:"type"`     // Alert condition type
    Title      string        `json:"title"`
    Message    string        `json:"message"`
    NodeID     string        `json:"node_id,omitempty"`
    NodeName   string        `json:"node_name,omitempty"`
    Username   string        `json:"username,omitempty"`
    Resolved   bool          `json:"resolved"`
    ResolvedAt int64         `json:"resolved_at,omitempty"`
}
```

#### Condition Deduplication Keys
To prevent filling the log with identical alerts every 5 seconds, active alerts are indexed by unique condition keys:
- `node_offline:<fsID>`: Raised when a node ceases responding to `/metrics` probes.
- `node_online:<fsID>`: Raised when an offline node recovers.
- `storage_warning:<fsID>`: Raised when disk usage exceeds 90%.
- `temp_warning:<fsID>`: Raised when CPU temperature exceeds 75°C.
- `quota_exceeded:<username>`: Raised when a user exceeds 95% of their storage quota.
- `error_spike:<fsID>`: Raised when handler error rates surge.
- `service_restart:<fsID>`: Raised when an unexpected daemon restart or uptime reset is detected.

#### Automated Recovery
When a node responds again or temperatures return below threshold limits:
1. The active alert is automatically marked `Resolved = true`.
2. A corresponding `info` level recovery alert is recorded (e.g., `"Node FS-1 returned online"`).
3. Alerts are persisted to `admin_alerts.json` using atomic temporary file writes.

### 2.3 Live Remote Log Streaming (`internal/admin/logs.go`)
Administrators can inspect live service logs directly in the web dashboard without opening terminal windows:
- **Command Dispatch**: The backend executes `journalctl -u dvfs-<service> -n <lines> --no-pager` over SSH on the target node.
- **Sanitized Bounds**: Requested line counts are clamped between 10 and 1,000 lines.
- **Development Fallback**: In local environments without systemd, the reader falls back to reading local log files.
- **REST Endpoint**: `GET /api/logs/tail?node=<fsID>&service=<fileserver|metaserver|admin>&lines=100`.

### 2.4 Command History & Audit Trail (`internal/admin/history.go`)
All cluster commands are recorded in `command_history.json`:
- **Capacity**: Configurable ring buffer (default: 100 records via `-history_limit`).
- **REST Endpoints**:
  - `GET /api/actions/history`: Returns chronological command audit logs.
  - `GET /api/actions/status/{actionID}`: Returns live execution state and terminal output for running batch operations.
