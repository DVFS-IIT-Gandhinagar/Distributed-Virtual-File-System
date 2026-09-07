# MetaServer Coordinator & Cluster Topology

This document illustrates the data flow, state transitions, and component interactions within the DVFS MetaServer Coordinator. The diagrams show how file servers register, how liveness is monitored, how client navigation works, and how crash recovery handles state.

## 1. MetaServer Component Structure
```mermaid
graph TD
    GRPCHandler["GRPCHandler"] --> MetaServer["MetaServer"]
    HeartbeatMonitor["HeartbeatMonitor (goroutine)"] --> MetaServer
    
    subgraph MetaServer Component
        MetaServer --> fileservers["fileservers (map[uint64]*domain.FileServerInfo)"]
        MetaServer --> users["users (map[string]uint64)"]
        MetaServer --> shared["shared (map[string][]SharedDirEntry)"]
        MetaServer --> nextFsID["nextFsID (uint64)"]
        MetaServer --> stateMu["mu (sync.RWMutex)"]
        MetaServer --> stateFile["stateFile (string)"]
    end
```

## 2. FileServer Registration and Deduplication
```mermaid
sequenceDiagram
    participant FileServer
    participant MetaServerHandler as GRPCHandler
    participant MetaServer as MetaServer
    participant StateFile as stateFile

    FileServer->>MetaServerHandler: "RegisterFileServer(Address, Users, Shared)"
    MetaServerHandler->>MetaServer: "mu.Lock()"
    MetaServerHandler->>MetaServer: "findFileServerByAddressLocked(Address)"
    alt Address is new
        MetaServer->>MetaServer: "assign nextFsID"
        MetaServer->>MetaServer: "create FileServerInfo"
    else Address already registered
        MetaServer->>MetaServer: "retrieve existing fsID"
    end
    MetaServerHandler->>MetaServer: "Update Status='healthy', LastHeartbeatUnix=now"
    MetaServerHandler->>MetaServer: "Update users and shared maps"
    MetaServerHandler->>MetaServer: "saveStateLocked()"
    MetaServer->>StateFile: "write atomic temp file -> rename"
    MetaServerHandler->>MetaServer: "mu.Unlock()"
    MetaServerHandler-->>FileServer: "RegisterFileServerResponse(Success=true)"
```

## 3. Heartbeat Liveness Monitor
```mermaid
sequenceDiagram
    participant FileServerMSClient as FileServer
    participant MetaServerHandler as GRPCHandler
    participant HeartbeatMonitor as HeartbeatMonitor

    loop every meta_heartbeat_interval
        FileServerMSClient->>MetaServerHandler: "Heartbeat(Address)"
        MetaServerHandler->>MetaServerHandler: "mu.Lock()"
        MetaServerHandler->>MetaServerHandler: "Update LastHeartbeatUnix"
        MetaServerHandler->>MetaServerHandler: "saveStateLocked()"
        MetaServerHandler->>MetaServerHandler: "mu.Unlock()"
        MetaServerHandler-->>FileServerMSClient: "HeartbeatResponse(Success=true)"
    end

    loop every heartbeat_check_interval
        HeartbeatMonitor->>HeartbeatMonitor: "ticker.C"
        HeartbeatMonitor->>MetaServerHandler: "mu.Lock()"
        HeartbeatMonitor->>HeartbeatMonitor: "markStaleFileServersLocked(now)"
        alt now - LastHeartbeatUnix > heartbeatTimeout
            HeartbeatMonitor->>HeartbeatMonitor: "Status='stale'"
            HeartbeatMonitor->>HeartbeatMonitor: "saveStateLocked()"
        end
        HeartbeatMonitor->>MetaServerHandler: "mu.Unlock()"
    end
```

## 4. Navigate and Root Selection Flow
```mermaid
sequenceDiagram
    participant DVFSClient
    participant MetaServerHandler as GRPCHandler
    participant MetaServer as MetaServer

    DVFSClient->>MetaServerHandler: "GetRoots(Username)"
    MetaServerHandler->>MetaServer: "mu.Lock()"
    MetaServerHandler->>MetaServer: "check users[Username]"
    MetaServerHandler->>MetaServer: "retrieve shared[Username] entries"
    MetaServerHandler->>MetaServer: "mu.Unlock()"
    MetaServerHandler-->>DVFSClient: "GetRootsResponse(Roots)"

    DVFSClient->>MetaServerHandler: "Navigate(Username, RootUser)"
    MetaServerHandler->>MetaServer: "mu.Lock()"
    MetaServerHandler->>MetaServer: "check users[RootUser] -> fsID"
    MetaServerHandler->>MetaServer: "retrieve fileservers[fsID]"
    MetaServerHandler->>MetaServer: "isHealthyLocked()"
    alt Status != healthy
        MetaServerHandler-->>DVFSClient: "NavigateResponse(Success=false, Error='unavailable')"
    else Status == healthy
        MetaServerHandler->>MetaServer: "verify access (owner or shared)"
        MetaServerHandler-->>DVFSClient: "NavigateResponse(Success=true, Address)"
    end
    MetaServerHandler->>MetaServer: "mu.Unlock()"
```

## 5. RootShare Deduplication and State Persistence
```mermaid
flowchart TD
    FS_Subtree["FileServer: DFS subtree ACL propagation"] --> FS_Save["FileServer: saveSharesLocked"]
    FS_Save --> FS_Unlock["FileServer releases fs.mu"]
    FS_Unlock --> FS_RPC["FileServer calls RootShare gRPC on MetaServer"]
    FS_RPC --> MS_Lock["MetaServer: mu.Lock()"]
    MS_Lock --> MS_CheckDup["MetaServer: check existing.Owner == req.Owner in shared[ShareWith]"]
    
    MS_CheckDup -->|Duplicate found| MS_Skip["MetaServer: return Success immediately"]
    MS_CheckDup -->|New Share| MS_Append["MetaServer: append SharedDirEntry"]
    
    MS_Append --> MS_Save["MetaServer: saveStateLocked()"]
    MS_Save --> MS_Unlock["MetaServer: mu.Unlock()"]
```

## 6. MetaServer Crash Recovery State Machine
```mermaid
stateDiagram-v2
    state "Starting" as Starting
    state "LoadingState" as LoadingState
    state "Ready" as Ready
    state "Serving" as Serving
    state "Crashed" as Crashed
    state "Recovering" as Recovering

    [*] --> Starting
    Starting --> LoadingState : "parse metaserver_state.json"
    LoadingState --> Ready : "state valid (loadState)"
    LoadingState --> Ready : "file missing (start fresh)"
    Ready --> Serving : "Start gRPC server"
    Serving --> Crashed : "process killed"
    Crashed --> Recovering : "process restarted"
    Recovering --> LoadingState : "reload state file"
```
