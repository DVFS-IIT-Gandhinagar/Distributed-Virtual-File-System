# MASTER SYSTEM ARCHITECTURE OVERVIEW

This document serves as the top-level reference for the Distributed Virtual File System (DVFS). It illustrates the complete system topology, end-to-end data flows, deployment layout, and component interaction matrices based directly on the implementation.

---

## 1. System Topology

### 1.1 Core Architecture (High-Level)

The following diagram depicts the primary architectural tiers and communication boundaries across the cluster:

```mermaid
graph TB
    subgraph Clients["DVFS Client"]
        CLI["Client Shell (Cobra REPL)<br>Local CNode Cache (.cache/)"]
    end

    subgraph Coordinator["MetaServer Coordinator (:50051)"]
        MDS["Routing & Health Advisory<br>Atomic State (metaserver_state.json)"]
    end

    subgraph Storage["FileServer Cluster (:50052, :9052)"]
        FS["Authoritative Storage Nodes (dvfs1..dvfs9)<br>Streaming I/O & InodeStore (.dvfs_inodes_index.json)"]
    end

    subgraph Management["Admin Web Console (:8080)"]
        ADMIN["Centralized Web Dashboard<br>Live Telemetry & SSH Orchestrator"]
    end

    CLI -->|"1. Discover Roots & Routing (mTLS 1.3)"| MDS
    CLI -->|"2. Direct File Streaming & ACL Checks (mTLS 1.3)"| FS
    FS -->|"Heartbeat & Registration (mTLS 1.3)"| MDS
    FS -->|"Push Invalidation Callbacks"| CLI
    ADMIN -->|"Scrape Telemetry (HTTP :9052)"| FS
    ADMIN -->|"Remote Host Management (SSH :22)"| FS
    ADMIN -.->|"Reads State Snapshot"| MDS
```

---

### 1.2 Detailed Subsystem Wiring Topology

The following comprehensive diagram shows internal subsystem modules, persistence files, and network discovery mechanisms across all cluster components:

```mermaid
graph TD
    subgraph PKINetwork["Zero-Trust PKI and Network Discovery"]
        RC["Root CA (ca.crt / ca.key)"]
        NC["Node Leaf Certificates (server.crt/key)"]
        GG["GitHub Gist (machines.json)"]
        TO["Tailscale Overlay Network"]

        RC -->|"Issues and Signs (DNS SANs)"| NC
    end

    subgraph Clients["DVFS Client Application"]
        CR["Client REPL Shell"]
        CC["CNode Cache Tree (.cache/UUID)"]
        CL["Callback Listener (ephemeral port)"]

        CR <-->|"Instant Cache Hits / Navigation"| CC
        CL -->|"Evicts Stale Entries"| CC
    end

    subgraph MetaServer["MetaServer Coordinator (:50051)"]
        MG["gRPC Coordinator Handler"]
        MH["Heartbeat Liveness Monitor"]
        MS[("State Persistence (metaserver_state.json)")]

        MG <-->|"Persist Routing / Reconstitute"| MS
        MH -->|"Evaluate Timeouts (30s) and Mark Stale"| MS
    end

    subgraph FileServerNodes["Authoritative FileServer Nodes (:50052, :9052)"]
        FG["gRPC Storage Handler"]
        FI[("Persistent InodeStore (.dvfs_inodes_index.json)")]
        FQ[("QuotaStore (quota_config.json)")]
        FM["HTTP Metrics Sidecar (:9052)"]
        FC["Callback Invalidation Sender"]
        FHOST["Host Daemon Environment (sshd :22, systemd)"]

        FG <-->|"Path to Inode ID Resolution"| FI
        FG <-->|"Soft/Hard Quota and 20 GiB Buffer"| FQ
        FG -->|"Trigger Cache Invalidation Events"| FC
        FG -.->|"Expose Latencies and IOPS"| FM
    end

    subgraph AdminConsole["Admin Console and Web Dashboard (:8080)"]
        AH["HTTP REST API"]
        AM["Auth Manager (SHA-256 / HttpOnly Cookie)"]
        AW["WebSocket Terminal (/ws/actions)"]
        AA["Alert Engine (Storage, Temp, Quota)"]
        AP["Metrics Poller (5s Scrape Ticker)"]
        AS["Remote SSH Orchestrator"]

        AH --- AM
        AP -->|"Evaluate Health Rules"| AA
        AP -->|"Feed Real-Time Dashboard"| AW
    end

    %% Client Communication Flows
    CR -->|"1. Query Server LAN IPs"| GG
    CR -->|"2. GetRoots, Navigate (mTLS 1.3)"| MG
    CR -->|"3. Upload, Download, ListDir, Share (mTLS 1.3)"| FG
    FC -->|"4. Push Invalidate (FILE_UPDATED, DIR_NEW, DELETED)"| CL

    %% Inter-Server Coordination
    FG -->|"RegisterFileServer, Heartbeat, RootShare (mTLS 1.3)"| MG

    %% Telemetry and Management
    AP -->|"HTTP Scrape GET /metrics"| FM
    AS -->|"Execute systemctl / journalctl via SSH"| FHOST
    AH -->|"Inspect Cluster State File"| MS
    AS -.->|"Secure Fallback Tunnel"| TO

    %% Security and Trust Bindings
    RC -.->|"Embeds Root CA Trust Pool"| CR
    NC -.->|"Server TLS Verification"| MG
    NC -.->|"Server TLS Verification"| FG
```

## 2. End-to-End Data Flow: File Upload

```mermaid
sequenceDiagram
    participant User
    participant DVFSClient
    participant MetaServer
    participant FileServer
    participant InodeStore
    participant QuotaStore
    participant CallbackSender
    participant PeerClient

    User->>DVFSClient: upload ./bigfile.pdf
    DVFSClient->>DVFSClient: lookup curr dir FID
    DVFSClient->>FileServer: UploadFile stream open (parentFID, name, size)
    FileServer->>QuotaStore: checkStorageQuotaWithAdditional
    FileServer->>InodeStore: GetOrAssign path -> inodeID
    loop Chunk loop (4MB each)
        DVFSClient->>FileServer: chunk bytes
        FileServer->>FileServer: write chunk to disk, DiskSafetyBuffer check
    end
    DVFSClient->>FileServer: send EOF
    FileServer->>FileServer: SHA256 verify, update inode size
    FileServer->>CallbackSender: notifyNewFileInDir (event 2)
    CallbackSender->>PeerClient: Invalidate{new_version=2}
    FileServer->>DVFSClient: UploadFileResponse{success:true}
```

## 3. End-to-End Data Flow: File Read with Cache

```mermaid
sequenceDiagram
    participant User
    participant DVFSClient
    participant CacheHandler
    participant LocalDiskCache
    participant FileServer

    User->>DVFSClient: read file.txt
    DVFSClient->>CacheHandler: ReadFile("file.txt")
    alt Cache Hit (contentCached == true)
        CacheHandler->>LocalDiskCache: Read bytes from ./.cache/UUID
        LocalDiskCache-->>CacheHandler: return data
        CacheHandler-->>DVFSClient: return data
        DVFSClient-->>User: Display content
    else Cache Miss (contentCached == false)
        CacheHandler->>FileServer: DownloadFile(parentFID, name)
        FileServer-->>CacheHandler: Stream 4MB chunks
        CacheHandler->>LocalDiskCache: Save chunks to ./.cache/UUID
        CacheHandler->>CacheHandler: set contentCached = true
        CacheHandler->>LocalDiskCache: Read bytes from ./.cache/UUID
        LocalDiskCache-->>CacheHandler: return data
        CacheHandler-->>DVFSClient: return data
        DVFSClient-->>User: Display content
    end
```

## 4. Component Interaction Matrix

| Source Component | Target Component | Protocol | Auth Mechanism | Description |
|---|---|---|---|---|
| Client | MetaServer | mTLS gRPC | Zero-Trust Root CA | GetRoots, Navigate routing |
| Client | FileServer | mTLS gRPC | Zero-Trust Root CA | File I/O (Upload, Download, ListDir) |
| FileServer | MetaServer | mTLS gRPC | Zero-Trust Root CA | RegisterFileServer, Heartbeats |
| Admin Console | FileServer | HTTP | None (internal sidecar) | Scrape `/metrics` for telemetry |
| Admin Console | FileServer | SSH | SSH Keys / scoped sudoers | Remote command orchestration |
| Admin Console | MetaServer | File I/O | FS Permissions | Reads `metaserver_state.json` |
| FileServer | Client | mTLS gRPC | Zero-Trust Root CA | Push Invalidation callbacks |

## 5. Deployment Topology on IITGN Cluster

```mermaid
graph TD
    subgraph CampusNetwork["Campus Network"]
        FN["Fortinet Firewall (24h Captive Portal)"]
        
        subgraph PhysicalNodes["Physical Cluster Nodes"]
            ADMIN_HOST["Admin Machine (1x)<br>Admin Console :8080<br>SSH key holder"]
            META_HOST["MetaServer Node (1x)<br>gRPC :50051<br>state file"]
            FS1_HOST["FileServer Node dvfs1<br>gRPC :50052, metrics :9052"]
            FS9_HOST["FileServer Node dvfs9<br>gRPC :50052, metrics :9052"]
            CLIENT_HOST["Client Machines<br>interactive REPL"]
        end
    end

    subgraph Overlays["Network Overlays"]
        TS["Tailscale Overlay (100.x.y.z)"]
        GH["GitHub Gist (machines.json)"]
    end

    FN --- PhysicalNodes
    TS -.->|"Overlay Management"| ADMIN_HOST
    TS -.->|"Overlay Management"| META_HOST
    TS -.->|"Overlay Management"| FS1_HOST
    TS -.->|"Overlay Management"| FS9_HOST
    
    ADMIN_HOST -.->|"SSH Remote Commands / Scrapes"| FS1_HOST
    ADMIN_HOST -.->|"SSH Remote Commands / Scrapes"| FS9_HOST
    CLIENT_HOST -->|"Fetches Dynamic Node IPs"| GH
    FS1_HOST -.->|"Auto-Discovered via Gist"| CLIENT_HOST
```

## 6. System Startup Sequence

```mermaid
sequenceDiagram
    participant MetaServer
    participant FileServer1
    participant FileServer2
    participant AdminConsole
    participant DVFSClient

    MetaServer->>MetaServer: load state (metaserver_state.json)
    MetaServer->>MetaServer: start gRPC listener (:50051)
    
    FileServer1->>FileServer1: load InodeStore (.dvfs_inodes_index.json)
    FileServer1->>MetaServer: RegisterFileServer
    FileServer1->>MetaServer: start heartbeat (periodic)
    
    FileServer2->>FileServer2: load InodeStore (.dvfs_inodes_index.json)
    FileServer2->>MetaServer: RegisterFileServer
    FileServer2->>MetaServer: start heartbeat (periodic)
    
    AdminConsole->>AdminConsole: load metaserver_state.json
    AdminConsole->>AdminConsole: start polling metrics (:9052)
    
    DVFSClient->>MetaServer: GetRoots
    DVFSClient->>MetaServer: Navigate
    DVFSClient->>FileServer1: RegisterClient (session start)
```
