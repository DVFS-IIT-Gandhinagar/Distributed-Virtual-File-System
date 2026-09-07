# Distributed Virtual File System (DVFS)

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![gRPC](https://img.shields.io/badge/gRPC-v1.62-244c5a?style=flat&logo=grpc)](https://grpc.io)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/Docs-GitHub_Pages-blue?style=flat&logo=materialformkdocs)](https://dvfs-iit-gandhinagar.github.io/Distributed-Virtual-File-System/)

An Andrew File System (AFS)-inspired, high-performance distributed virtual file system implemented in Go, communicating over gRPC, and secured with Zero-Trust mutual TLS. Designed for collaborative environments across Raspberry Pi clusters, Linux workstations, and cloud servers.

---

> **Complete Documentation Portal**: **[dvfs-iit-gandhinagar.github.io/Distributed-Virtual-File-System](https://dvfs-iit-gandhinagar.github.io/Distributed-Virtual-File-System/)**  
> For in-depth architecture flows, formal security models, multi-node deployment runbooks, and the full 19-command CLI manual, visit our hosted documentation portal or explore the local [`docs/`](docs/index.md) directory.

---

## 1. System Architecture

```
                  +-----------------------------------+
                  |      MetaServer (Coordinator)     |
                  |  - Dynamic Root Discovery (MDS)   |
                  |  - Heartbeat & Liveness Tracker   |
                  |  - State: metaserver_state.json   |
                  +-----------------+-----------------+
                                    ^
                   Registration &   |   Advisory
                   Heartbeats       |   Routing
                                    |
          +-------------------------+-------------------------+
          |                                                   |
          v                                                   v
+-------------------------+                         +-------------------------+
|    FileServer Node 1    |                         |    FileServer Node 2    |
| - Authoritative Storage |                         | - Authoritative Storage |
| - InodeStore (.dvfs...) |                         | - InodeStore (.dvfs...) |
| - Per-Directory .acl    |                         | - Per-Directory .acl    |
| - Streaming I/O (4 MB)  |                         | - Streaming I/O (4 MB)  |
| - Metrics HTTP (:9052)  |                         | - Metrics HTTP (:9053)  |
+------------+------------+                         +------------+------------+
             ^                                                   ^
             |                 Push Invalidation                 |
             |                 Callbacks                         |
             |                                                   |
             +--------------------------+------------------------+
                                        |
                             +----------+----------+
                             |       DVFS Client   |
                             | - Interactive REPL  |
                             | - Local CNode Cache |
                             | - Embedded Root CA  |
                             | - Dynamic Gist SNI  |
                             +---------------------+
```

---

## 2. Core Highlights

- **Multi-Root Virtual Namespace**: Users interact with a private home root (`mydrive`) and cluster-shared roots (`[owner]: [folder]`) presented via an interactive root menu (`GetRoots`). Navigating `cd ..` from the top level of any root returns cleanly to root selection.
- **Whole-File Caching & Push Callbacks**: AFS-style local UUID caching with zero-network reads on cache hits. Authoritative FileServers push gRPC `Invalidate` callbacks directly to connected clients upon file modification or deletion.
- **Authoritative Storage & Advisory Indexing**: FileServers manage physical disk I/O, enforce ACLs, allocate logical Inode IDs, and execute atomic operations. The MetaServer coordinates cluster routing and indexes shared directories.
- **Zero-Trust PKI & Dynamic Discovery**: Inter-node communication is strictly encrypted with mutual TLS 1.3 using an air-gapped Root CA. Node certificates use DNS SANs (`dvfs1`–`dvfs9`), decoupling TLS verification from dynamic campus DHCP IP addresses via Tailscale and GitHub Gist.
- **Soft Deletion Recycle Bin**: `trash` moves files and directories into a hidden `.trash/` container with automatic collision suffixing (`file__<inodeID>`). `restore` reinstates files to their original parent (falling back to user root if the parent was deleted).
- **Dynamic Multi-Tenant Quotas**: Per-user storage quotas with dynamic adjustment via `SetQuota` gRPC and Admin Web Console, integer overflow protection, and mid-stream chunk scrapping with automatic storage rollback.
- **Integrated Observability & Remote Orchestration**: FileServers expose `/metrics` HTTP sidecars with streaming throughput, IOPS, and latency percentiles (p50/p95/p99). A centralized React web console monitors cluster health, triggers state-machine alerts, and executes remote SSH batch commands (`systemctl`, `journalctl`, `apt`, `reboot`).

---

## 3. Quick Start (Single Machine)

### Prerequisites
- **Go**: 1.24+ installed
- **Make** & **OpenSSL**

### 1. Build and Initialize
```bash
# Clone the repository
git clone https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System.git
cd Distributed-Virtual-File-System

# Generate development certificates
make certs

# Build all binaries into ./bin/
make build
```

### 2. Start Cluster Components (Separate Terminals)

```bash
# Terminal 1: Start MetaServer Coordinator
./bin/metaserver -port=50051

# Terminal 2: Start Storage FileServer
./bin/fileserver -id=fs1 -port=50052 -data=./fileserver_data -meta_addr=127.0.0.1:50051 -own_ip=127.0.0.1

# Terminal 3: Start Admin Web Console (Optional)
./bin/admin -port=8080 -state_file=./metaserver_state.json -static=./cmd/admin/static

# Terminal 4: Launch Interactive Client Shell
./bin/client -username=alice -ip_addr=127.0.0.1 -port=50051 -meta=true
```

Inside the client REPL, type `help` to list all available commands (`ls`, `cd`, `pwd`, `create`, `mkdir`, `read`, `upload`, `download`, `trash`, `restore`, `show_trash`, `clear_trash`, `delete`, `sharewith`, `unsharewith`, `viscache`, `refresh`, `info`, `clear`, `exit`).

---

## 4. Documentation Index

The complete documentation is organized into modular guides:

| Document | Description |
|---|---|
| [**Portal Home**](docs/index.md) | Executive overview, design principles, and system topology. |
| [**Setup & Deployment Guide**](docs/setup.md) | Complete multi-node cluster deployment runbook for Raspberry Pis, Linux servers, systemd daemons, and development hosts. |
| [**System & Hosting Architecture**](docs/architecture.md) | Inode data structures, FID identity, AFS workflows, 18 architectural design decisions, and IITGN campus workarounds (Fortinet, Gist, SANs). |
| [**Client CLI Reference**](docs/client_cli.md) | Full syntax, flags, examples, and behavior for all 20 terminal shell commands. |
| [**Client Caching & Push Callbacks**](docs/features/caching.md) | In-memory CNode tree, UUID cache files, push invalidations, 45s session TTL, and readline prompt protection. |
| [**Sharing & Access Control Lists**](docs/features/sharing_acls.md) | Authoritative ACL propagation (DFS), `RootShare` protocol, and formal security invariants. |
| [**Crash Recovery & Inode Persistence**](docs/features/crash_recovery.md) | Persistent InodeStore (`.dvfs_inodes_index.json`), MetaServer state snapshots, heartbeat isolation, and 4-terminal runbooks. |
| [**Recycle Bin & Trash Subsystem**](docs/features/trash.md) | Two-tier deletion safety, collision-safe renaming, metadata fallback, and 9-case test runbook. |
| [**Dynamic Storage Quotas**](docs/features/quotas.md) | Per-user quota enforcement, `SetQuota` gRPC protocol, and mid-stream chunk scrapping. |
| [**Zero-Trust TLS & PKI**](docs/features/tls_security.md) | Offline Root CA ceremony, DNS SAN leaf certificates, and dynamic SNI resolution. |
| [**Telemetry & Performance Monitoring**](docs/features/admin_telemetry.md) | HTTP metrics sidecars, real-time chunked throughput, latency percentiles, and CSV exports. |
| [**Remote Cluster Orchestration**](docs/features/cluster_orchestration.md) | Remote SSH execution, live journalctl streaming, deduplicated alerts, and command history. |
| [**Authentication & Access Control**](docs/features/authentication.md) | SHA-256 password verification, constant-time checks, and cookie-authenticated WebSockets. |
| [**Project Artifacts & Poster**](docs/artifacts.md) | Academic presentation poster (`Poster.pdf`) and research findings summary. |

---

## 5. Repository Structure

```
.
├── api/                   # Protocol Buffer definitions and generated gRPC stubs
│   ├── fileserver/        # FileServer service & streaming RPCs
│   ├── metaserver/        # MetaServer routing & advisory RPCs
│   └── callback/          # Client push invalidation callback RPCs
├── cmd/                   # Application entry points
│   ├── client/            # Interactive Cobra CLI REPL & cache engine
│   ├── fileserver/        # Authoritative storage daemon & metrics sidecar
│   ├── metaserver/        # Coordinator & liveness tracking daemon
│   └── admin/             # Centralized admin console server & React SPA
├── internal/              # Core business logic and shared domain packages
│   ├── client/            # CNode tree, cache handler, callback listener
│   ├── fileserver/        # InodeStore, ACL enforcement, chunk streaming, trash
│   ├── metaserver/        # Advisory index, state snapshotting, heartbeat monitor
│   ├── admin/             # Telemetry poller, alert engine, SSH orchestrator
│   └── domain/            # FID, Inode, and ACL domain types
├── docs/                  # Full documentation source (Material for MkDocs)
├── scripts/               # Automation, systemd unit files, PKI, and campus workarounds
├── Makefile               # Standard build, test, cert generation, and execution targets
└── mkdocs.yml             # Documentation portal configuration
```

---

## 6. Testing

Run the automated test suite across all modules:
```bash
go test ./...
```

Run test suite with race detection:
```bash
go test -race ./...
```

---
