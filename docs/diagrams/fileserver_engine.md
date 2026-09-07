# FileServer Storage Engine Architecture

This document illustrates the internal architecture, state flows, and component interactions of the DVFS FileServer storage engine based on the underlying source code implementations.

## 1. FileServer Component Structure

This diagram outlines the `FileServer` struct and its integration with external handlers (gRPC, Metrics, Callback).

```mermaid
graph TD
    subgraph Handlers
        gRPCHandler["gRPC Handler (Register, Upload, List)"]
        MetricsSidecar["HTTP Metrics Sidecar (:9052)"]
        CBClient["gRPC Callback Client"]
    end

    subgraph Core
        FS["FileServer Core Component"]
        FS -->|RWMutex| MU["sync.RWMutex (mu)"]
        FS --> ID["serverID (string)"]
        FS --> RD["rootDir (string)"]
        FS --> IM["inodes (map[string]*domain.Inode)"]
        FS --> UM["users (map[string]*domain.FID)"]
        FS --> SM["sessions (map[string]*clientSession)"]
        FS --> TM["trashMeta (map[string]trashEntry)"]
        FS --> NID["nextInodeID (uint64)"]
    end

    subgraph Persistence
        IS["InodeStore (inodestore.go)"]
        QS["Quota config (quotas map)"]
        OM["OperationMetrics (opMetrics)"]
    end

    gRPCHandler -->|Executes Methods| FS
    MetricsSidecar -->|Reads| OM
    FS -->|Sends Invalidations| CBClient
    FS --> IS
    FS --> QS
    FS --> OM
```

## 2. Upload File Workflow

The complete lifecycle of a file upload, including chunking, disk checks, quota checks, hashing, and invalidation.

```mermaid
sequenceDiagram
    participant Client
    participant Handler as GRPCHandler
    participant FS as FileServer
    participant IS as InodeStore
    participant CB as CallbackServer

    Client->>Handler: UploadFile (Stream)
    Handler->>FS: checkStorageQuotaWithAdditional(user, size)
    FS-->>Handler: Quota OK (Logical & Physical)
    
    Handler->>FS: CreateFile(parentFID, name, user, type)
    FS->>IS: GetOrAssign(relPath)
    IS-->>FS: InodeID
    FS-->>Handler: new FID

    loop For each 4MB chunk
        Client->>Handler: Send Chunk
        Handler->>FS: WriteFile(parentFID, name, offset, chunk)
        FS-->>Handler: Chunk written
        alt Quota Breach Mid-stream
            Handler->>FS: cleanupFailedUpload()
            FS-->>Handler: Inode deleted, quota rolled back
        end
    end

    Client->>Handler: EOF
    Handler->>FS: GetFileHash (Verify SHA256)
    FS-->>Handler: Hash Match
    Handler->>FS: NotifyNewFileInDir(parentFID, name, user)
    FS->>CB: sendInvalidate(event=2 DIR_NEW_FILE)
    CB-->>Client: (Other active sessions invalidated)
    Handler-->>Client: Success Response
```

## 3. Trash and Restore Flow

The operational flow for moving files to the `.trash` directory and restoring them back to their original locations.

```mermaid
flowchart TD
    subgraph TrashFile
        T1["Verify ACL/Owner"] --> T2["Unique Name Gen (name__inodeID)"]
        T2 --> T3["os.Rename (src -> .trash/dst)"]
        T3 --> T4["InodeStore.RenamePrefix"]
        T4 --> T5["Store trashMeta (origParent, origRelPath)"]
        T5 --> T6["Detach shared snapshots"]
        T6 --> T7["Update Parent & Subtree Paths"]
    end

    subgraph RestoreFile
        R1["Look up trashMeta by FID"] --> R2["Verify original parent exists (fallback to root)"]
        R2 --> R3["os.Rename (.trash/src -> dst)"]
        R3 --> R4["InodeStore.RenamePrefix (reverse)"]
        R4 --> R5["Reattach shared snapshots"]
        R5 --> R6["Remove from trashMeta"]
    end
```

## 4. Quota Enforcement Layers

The dual-layer strategy ensuring both per-user fair usage and physical host system stability.

```mermaid
graph TD
    Req["Upload / Create Request"] --> Check["checkStorageQuotaWithAdditional(username, additionalBytes)"]
    
    Check --> Layer1
    Check --> Layer2

    subgraph Layer 1: Logical Quota
        Layer1["User Quota Limit (getUserQuotaLocked)"]
        Layer1 --> L1Check{"RootInode.Size + additional > Quota?"}
        L1Check -->|Yes| L1Fail["Deny: Quota Exceeded"]
        L1Check -->|No| L1Pass["Logical OK"]
    end

    subgraph Layer 2: Physical Disk
        Layer2["Disk Free Space (readDiskStats)"]
        Layer2 --> L2Check{"DiskFree - additional <= DiskSafetyBuffer (20 GiB)?"}
        L2Check -->|Yes| L2Fail["Deny: System Disk Near Full"]
        L2Check -->|No| L2Pass["Physical OK"]
    end

    L1Pass --> Final["Allow Operation"]
    L2Pass --> Final
```

## 5. Push Invalidation and Session Targeting

How the callback system targets active client sessions for cache invalidation.

```mermaid
sequenceDiagram
    participant FS as FileServer
    participant SM as SessionMap
    participant Target as ClientB (Active Session)
    
    FS->>SM: snapshotNotifyTargetsForDirLocked()
    note over SM: Filter: currentDirFID matches target
    note over SM: Filter: Ignore originUser
    note over SM: Filter: lastSeenAt <= 45s TTL
    SM-->>FS: []clientSession targets
    
    loop For each target session
        FS->>Target: Invalidate RPC (Fid, EventType)
        alt Success
            Target-->>FS: OK
            FS->>SM: recordCallbackResult(success=true, resets consecutiveFailures)
        else Failure (x3)
            FS->>SM: recordCallbackResult(success=false, increments consecutiveFailures)
            note over SM: If failures >= 3, delete(session)
        end
    end
```

## 6. InodeStore State Transitions

State transitions for persistent tracking of Inode assignments based on the index file behavior.

```mermaid
stateDiagram-v2
    state "Empty (No .dvfs_inodes_index.json)" as Empty
    state "Loaded (from disk)" as Loaded
    state "Modified (Memory updated)" as Modified
    state "Saving (tmp file write)" as Saving
    state "Saved (atomic rename)" as Saved

    [*] --> Empty : New Install
    Empty --> Loaded : Startup / Load
    [*] --> Loaded : Server Restart

    Loaded --> Modified : GetOrAssign (New Path)
    Loaded --> Modified : RenamePrefix (Move/Trash)
    Loaded --> Modified : Remove (Permanent Delete)

    Modified --> Saving : Save() Called
    Saving --> Saved : os.Rename(tmp, index)
    Saved --> Loaded
```
