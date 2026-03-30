# MDS and Heartbeat System Diagram

This document visualizes the current metadata server (MDS) crash-recovery and heartbeat architecture.

## 1) System Topology

```mermaid
flowchart LR
    C1[Client Alice]
    C2[Client Bob]

    subgraph MDSHost[MDS Process]
      MDS[MetaServer gRPC]
      HMON[Heartbeat Monitor Goroutine]
      MEM[(In-memory maps\nfileservers, users, nextFsID)]
    end

    STATE[(metaserver_state.json)]

    subgraph FSHost1[FileServer fs1 Process]
      FS1[FileServer gRPC]
      SYNC1[Meta Sync Loop\nregister retry + heartbeat]
      DATA1[(fileserver_data/fs1)]
    end

    subgraph FSHost2[FileServer fs2 Process]
      FS2[FileServer gRPC]
      SYNC2[Meta Sync Loop\nregister retry + heartbeat]
      DATA2[(fileserver_data/fs2)]
    end

    C1 -->|Navigate user| MDS
    C2 -->|Navigate user| MDS

    MDS <--> MEM
    MDS -->|snapshot save/load| STATE

    SYNC1 -->|RegisterFileServer| MDS
    SYNC1 -->|Heartbeat| MDS
    SYNC1 -->|retry register on failure| MDS

    SYNC2 -->|RegisterFileServer| MDS
    SYNC2 -->|Heartbeat| MDS
    SYNC2 -->|retry register on failure| MDS

    MDS -->|Navigate response: healthy FS address| C1
    MDS -->|Navigate response: healthy FS address| C2

    C1 -->|file ops| FS1
    C2 -->|file ops| FS2

    FS1 <--> DATA1
    FS2 <--> DATA2

    HMON -->|mark stale on timeout| MEM
```

## 2) Startup and Crash Recovery Flow

```mermaid
sequenceDiagram
    participant M as MetaServer
    participant S as metaserver_state.json
    participant F as FileServer Sync Loop

    M->>S: Load snapshot on startup
    S-->>M: fileservers, users, nextFsID
    M->>M: Initialize missing heartbeat fields
    M->>M: Start heartbeat monitor ticker

    F->>M: RegisterFileServer(address, users)
    M->>M: Upsert FS by address
    M->>M: Mark healthy + update LastHeartbeatUnix
    M->>S: Persist state atomically

    loop Every heartbeat interval
      F->>M: Heartbeat(address)
      M->>M: Mark healthy + refresh LastHeartbeatUnix
      M->>S: Persist state
    end
```

## 3) Liveness, Stale Transition, and Routing Behavior

```mermaid
flowchart TD
    A["Heartbeat monitor tick"] --> B{"Heartbeat timeout exceeded?"}
    B -- "No" --> C["Keep status healthy"]
    B -- "Yes" --> D["Set status stale"]
    D --> E["Persist updated state"]

    F["Client Navigate(user)"] --> G{"User mapped to FS?"}
    G -- "No" --> H["Select least-loaded healthy FS"]
    G -- "Yes" --> I{"Mapped FS healthy?"}
    I -- "Yes" --> J["Return mapped FS address"]
    I -- "No" --> K["Remove stale mapping"]
    K --> H
    H --> L{"Any healthy FS available?"}
    L -- "Yes" --> M["Assign user to healthy FS and persist"]
    M --> N["Return selected FS address"]
    L -- "No" --> O["Return error: no healthy file server registered"]
```

## 4) Operational Notes

- MDS persists metadata changes after registration, heartbeat updates, stale transitions, and user remapping.
- File server liveness uses a two-layer strategy:
  - Frequent heartbeat RPC for low-overhead health signals.
  - Registration retry whenever registration or heartbeat fails.
- On MDS restart, previously known file servers are trusted initially from snapshot, then corrected by heartbeat timeout logic.
