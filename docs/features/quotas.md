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
In Phase 2, `internal/fileserver/quota.go` introduced dynamic quota management:

```go
type QuotaConfig struct {
    Quotas map[string]uint64 `json:"quotas"` // Username -> Quota in Bytes
}
```

- **Default Value**: Any user without an explicit entry in `quota_config.json` defaults to `defaultStorageQuota = 1024 * 1024 * 1024` (1 GiB).
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

## Diagrams
See the [Quota Enforcement Layers](../diagrams/fileserver_engine.md#4-quota-enforcement-layers) in the FileServer Engine architecture document for a visual breakdown of logical and physical quota enforcement.

