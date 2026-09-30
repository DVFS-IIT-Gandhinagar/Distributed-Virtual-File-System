# Dynamic Storage Quota Enforcement

This document details the multi-tenant storage quota subsystem in DVFS, separated into the governing principles (**In Essence**) and the technical implementation (**Implementation Quirks**).

---

## 1. In Essence: Multi-Tenant Storage Governance

In a multi-user distributed filesystem, storage resources must be governed to prevent individual users from starving the host physical disks.

DVFS enforces dynamic per-user quotas with three guarantees:
1. **Pre-Allocation Enforcement**: Before creating a new file or directory, the FileServer checks whether the user's root already exceeds their assigned limit.
2. **Dynamic Runtime Updates**: Quotas are not static compilation constants. Administrators can adjust any user's storage quota on the fly via the Admin Console or gRPC without restarting fileserver daemons.
3. **Persistent Quota Profiles**: Adjusted quotas are committed to persistent storage on the FileServer and survive node restarts.

```
[Upload / Create Request]
           |
           v
  [Check Current Size]
           |
   +-------+-------+
   |               |
< Quota         >= Quota
   |               |
[Allow Action]  [Reject with Quota Exceeded Error]
```

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Dynamic Quota Store (`quota_config.json`)
In early DVFS releases, quotas were hardcoded to a 1 GB constant (`const storageQuota = 1024*1024*1024`). 
`internal/fileserver/quota.go` introduced dynamic quota management:

```go
type QuotaConfig struct {
    Quotas map[string]uint64 `json:"quotas"` // Username -> Quota in Bytes
}
```

- **Default Value**: Any user without an explicit entry in `quota_config.json` defaults to `defaultStorageQuota = 16 * 1024 * 1024 * 1024` (16 GiB).
- **Persistent Location**: Stored as `quota_config.json` directly within the FileServer's `-data` root.
- **Thread Safety & Atomic Persistence**: Updates acquire `fs.mu.Lock()` and write through a temporary file committed via `os.Rename`.

### 2.2 The `SetQuota` gRPC Protocol
Modifying quotas over the network uses a dedicated gRPC RPC in `api/fileserver/fileserver.proto`:

```protobuf
message SetQuotaRequest {
  string username = 1;
  uint64 quota_bytes = 2;
}

message SetQuotaResponse {
  bool success = 1;
  string error = 2;
}

service FileServer {
  ...
  rpc SetQuota(SetQuotaRequest) returns (SetQuotaResponse);
}
```

The Admin Console backend exposes `PUT /api/users/{username}/quota`. When an administrator edits a quota in the web dashboard, the backend resolves the user's home FileServer, dials its gRPC endpoint, and executes `SetQuota`.

### 2.3 Integer Overflow Protection
When testing large uploads, arithmetic checks like `root.Size + additionalBytes > quota` risk unsigned integer overflow wrap if `root.Size` and `additionalBytes` are close to `math.MaxUint64`.

The FileServer safeguards this in `checkStorageQuotaWithAdditional` using subtraction guards:
```go
if quota < root.Size {
    return fmt.Errorf("storage quota exceeded")
}
remaining := quota - root.Size
if additional > remaining {
    return fmt.Errorf("storage quota exceeded")
}
```

### 2.4 Mid-Stream Quota Scrapping & Size Rollback
While `checkStorageQuotaWithAdditional` verifies available headroom prior to upload initialization, chunked streaming uploads (`UploadFile`) can encounter quota exhaustion mid-transfer (e.g. if concurrent writes occur or if actual chunk data exceeds pre-flight estimations).

When a write mid-stream breaches quota:
1. The server aborts the streaming RPC with a quota exceeded error.
2. `h.cleanupFailedUpload(parentFID, name, uploadUser)` is immediately invoked in `internal/fileserver/handler.go`.
3. The partially written file is unlinked and deleted from physical storage via `DeleteFile`.
4. The user's root and parent directory sizes are decremented back to their pre-transfer values, preventing orphan bytes or corrupted partial files from consuming quota headroom.

### 2.5 Quota Threshold Alerts
The Admin Console monitors per-user storage percentages against quotas:
- **Warning State**: Highlighted in yellow in the dashboard when usage exceeds 80%.
- **Critical State**: Highlighted in red and triggers an automated alert in `internal/admin/alerts.go` when usage exceeds 95%.
- **Auto-Recovery**: When the user deletes files and drops usage below 95%, the alert engine marks the quota alert resolved automatically.

### 2.6 Physical Host Storage Safety Buffer (`DiskSafetyBuffer = 20 GiB`)
In addition to per-user logical quotas, FileServers protect the host operating system against disk starvation through an authoritative physical disk safety reserve defined in `internal/fileserver/fileserver.go`:

```go
const DiskSafetyBuffer uint64 = 20 * 1024 * 1024 * 1024 // 20 GiB safety buffer
```

- **Physical Headroom Check**: During pre-flight allocation checks in `CreateFile` and during chunk writes in `WriteFile`, the FileServer invokes `readDiskStats(fs.rootDir)` to query host filesystem metrics (`statvfs` / `GetDiskFreeSpaceExW`).
- **Safety Enforcement**: Even if a user has ample logical quota remaining, if physical free disk space drops to or below 20 GiB (`diskFree <= DiskSafetyBuffer`), or if the write size exceeds `diskFree - DiskSafetyBuffer`, the operation is blocked:
  ```text
  fileserver storage limit reached: cannot write X bytes (usable free space: Y bytes, 20 GiB reserved for system safety)
  ```
- **System Stability Rationale**: This invariant protects the host node against out-of-disk crashes, kernel panics, systemd journal dropouts, and swap exhaustion caused by concurrent large uploads.

---

## 3. Configuration & Changing the Default Storage Quota (16 GiB)

The default storage quota across the cluster is **16 GiB** (`16 * 1024 * 1024 * 1024` bytes, or 17,179,869,184 bytes).

If you want to modify this default storage quota to another limit (for example, 32 GiB, 64 GiB, or back to 1 GiB), the following files and configurations govern the default quota:

### 3.1 Backend Files

| File | Location | Description |
| :--- | :--- | :--- |
| [`internal/fileserver/quota.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/fileserver/quota.go) | `const defaultStorageQuota = 16 * 1024 * 1024 * 1024` (Line 14) | **Authoritative fileserver constant.** Controls the default quota assigned to any user who does not have an explicit override in `quota_config.json`. Used in `getUserQuotaLocked` and published via `/metrics` (`per_user_quota`). |
| [`internal/admin/handlers.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/admin/handlers.go) | `QuotaLimit: 16 * 1024 * 1024 * 1024` in `handleUsers` (Line 306) | **Admin Console API fallback.** Returned by `GET /api/users` if the user's home fileserver has not yet reported metrics or has no `PerUserQuota` entry. |
| [`internal/admin/poller.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/admin/poller.go) | `quota := uint64(16 * 1024 * 1024 * 1024)` in `refreshNodes` (Line 115) | **Admin alert engine fallback.** Used by the background poller when evaluating quota threshold alerts (80% warning / 95% critical) if node metrics are pending. |

> [!NOTE]
> `internal/fileserver/fileserver.go` defines `const storageQuota uint64 = defaultStorageQuota` (Line 60) as a backwards-compatibility alias; it automatically inherits any change made to `defaultStorageQuota`.

### 3.2 Automated Unit & Integration Tests

The following test suites reference the default quota and should be verified after changing the constant:

1. **[`internal/fileserver/quota_test.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/fileserver/quota_test.go)**:
   - `TestGetUserQuotaDefault`: Compares against `defaultStorageQuota` directly (automatically tracks the constant).
2. **[`internal/fileserver/robustness_test.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/fileserver/robustness_test.go)**:
   - `testQuota`: Verifies fallback to `defaultStorageQuota` when quota entry is zero (automatically tracks the constant).
3. **[`internal/fileserver/metrics_test.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/fileserver/metrics_test.go)**:
   - `TestMetricsEndpoint`: Verifies `/metrics` contains `storageQuota` (automatically tracks `defaultStorageQuota`).
4. **[`internal/admin/quota_test.go`](file:///C:/Users/GSRAJA/Desktop/IIT%20GN/DVFS_project/Distributed-Virtual-File-System/internal/admin/quota_test.go)**:
   - Contains mock fileserver configurations initializing mock users with specific quota values for testing aggregations.

Run the test suite to verify:
```bash
go test -tags use_google_auth ./internal/fileserver -run "TestQuota|TestGetUserQuota" -v
go test ./internal/admin -run "TestHandleUserQuota" -v
```

### 3.3 Existing Deployed Data (`quota_config.json`)

- **New Users & Unmodified Users**: Any user without an entry in `<fileserver_data>/quota_config.json` will automatically receive the new default quota as soon as the updated `fileserver` binary is restarted.
- **Existing Users with Explicit Overrides**: If a user was previously assigned an explicit quota limit via the Admin Web Console or `SetQuota` gRPC, their quota is recorded in `<fileserver_data>/quota_config.json`. To apply the new default to those users, either:
  1. Update their quota through the Admin Web Console (**Users** &rarr; **Edit Quota**).
  2. Remove their specific key from `quota_config.json` on the fileserver host while the server is stopped.

### 3.4 Frontend UI Compatibility

The Admin Web Console frontend ([`cmd/admin/ui`](./../../cmd/admin/ui)):
- Dynamically reads `quota_limit` from the `/api/users` REST endpoint.
- Dynamically parses `per_user_quota` from `/api/cluster` node metrics.
- No frontend TypeScript or JSX files hardcode the default quota value; the UI automatically reflects whatever quota is returned by the backend.

### 3.5 Rebuilding and Deployment

After updating the constants:
```bash
# 1. Build local binaries
make build

# 2. Or cross-compile for cluster nodes (ARM64 / AMD64)
make release-nodes

# 3. Deploy the new fileserver binaries to nodes and restart fileserver services
```

---

## Diagrams
See the [Quota Enforcement Layers](../diagrams/fileserver_engine.md#4-quota-enforcement-layers) in the FileServer Engine architecture document for a visual breakdown of logical and physical quota enforcement.


