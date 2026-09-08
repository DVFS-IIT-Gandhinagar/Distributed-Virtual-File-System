# Distributed Virtual File System (DVFS)

Welcome to the technical documentation for the **Distributed Virtual File System (DVFS)** — an Andrew File System (AFS)-inspired, high-performance distributed virtual file system implemented in Go, communicating over gRPC, and secured with Zero-Trust mutual TLS.

<div style="display: flex; gap: 0.8rem; flex-wrap: wrap; margin: 1.2rem 0;">
  <a href="downloads.md" class="md-button md-button--primary">Download Client</a>
  <a href="setup.md" class="md-button">Setup Guide</a>
  <a href="architecture.md" class="md-button">Architecture</a>
</div>

---

## 1. Executive Overview

DVFS is designed for multi-user collaboration in cluster and campus environments (such as networked Raspberry Pi clusters and Linux servers). It bridges the usability of a local terminal shell with the power of distributed storage, whole-file caching, real-time push invalidations, and centralized observability.

### Core Design Principles

1. **Multi-Root Namespace & Virtual Sharing**:
   Every authenticated user interacts with a unified namespace containing:
   - **`mydrive` (Home Root)**: The user's private storage, hosted authoritatively on the user's home FileServer.
   - **Shared Roots**: Folders shared with the user across the cluster (e.g. `alice: project`), indexed by the MetaServer via `GetRoots`.
   At login, the client CLI presents an interactive root selection menu, and navigating up (`cd ..`) from the top of any root seamlessly returns the user to root selection.

2. **Clear Separation of Authority & Indexing**:
   - **FileServers (FS)** are **authoritative**: They store raw file data, maintain persistent logical inode mappings, manage access control lists (ACLs), execute atomic disk operations, and track active client sessions.
   - **MetaServer (MDS)** is **advisory**: It maintains user routing maps, tracks cluster node health via heartbeats, and maintains a denormalized shared directory index to ensure instantaneous directory listings without querying individual fileservers.

3. **AFS-Style Whole-File Caching & Push Callbacks**:
   Clients maintain an in-memory CNode cache tree mirroring remote structures. File reads download content into local UUID-backed cache files. Whenever a file is modified or deleted, the authoritative FileServer pushes gRPC `Invalidate` callbacks directly to connected clients, invalidating stale cache entries in real time while preserving terminal prompt redraws.

4. **Zero-Trust PKI & Dynamic Discovery**:
   All inter-node gRPC communication is encrypted with TLS 1.3 using an offline, air-gapped Root Certificate Authority. Node leaf certificates use DNS Subject Alternative Names (SANs), allowing cluster nodes to function seamlessly over dynamic DHCP network addresses. Client binaries embed the public Root CA trust store at compile time, eliminating manual certificate distribution to end-user machines.

5. **User-Facing Google OAuth 2.0 & High-Performance Session Security**:
   End users authenticate via Google OAuth 2.0 using the Authorization Code Flow with RFC 7636 PKCE. A single-gate handshake (`RegisterClient`) exchanges the Google ID token for a 256-bit CSPRNG Server Session Token (SST). The SST is bound to the client's network IP address, cached in memory via SHA-256 hashes, and verified across all unary and streaming gRPC calls with sub-millisecond overhead. A Centralized Policy Enforcement Point (`fs.Authorize`) authoritatively verifies file permissions and ownership, eliminating Insecure Direct Object References (IDOR).

6. **Integrated Observability & Remote Orchestration**:
   FileServers run an integrated `/metrics` HTTP sidecar exposing real-time chunked streaming throughput, IOPS, and sliding-window latency percentiles (p50, p95, p99). An administrative web console polls telemetry every 5 seconds, maintains in-memory ring buffers, raises state-machine alerts, and executes remote operational commands over SSH.

---

## 2. System Topology

```
                  +-----------------------------------+
                  |      MetaServer (Coordinator)     |
                  |  - User -> FileServer Routing     |
                  |  - Heartbeat & Liveness Tracker   |
                  |  - Shared Directory Index         |
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
|   FileServer Node 1     |                         |   FileServer Node 2     |
| - Authoritative Inodes  |                         | - Authoritative Inodes  |
| - Persistent InodeStore |                         | - Persistent InodeStore |
| - ACL & PEP Authorize   |                         | - ACL & PEP Authorize   |
| - SessionStore (SST)    |                         | - SessionStore (SST)    |
| - Streaming File I/O    |                         | - Streaming File I/O    |
| - Metrics HTTP Sidecar  |                         | - Metrics HTTP Sidecar  |
+------------+------------+                         +------------+------------+
             ^                                                   ^
             |                 Push Invalidation                 |
             |                 Callbacks                         |
             |                                                   |
             +--------------------------+------------------------+
                                        |
                             +----------+----------+
                             |     DVFS Client     |
                             | - Cobra CLI REPL    |
                             | - Google OAuth PKCE |
                             | - Local CNode Cache |
                             | - Embedded Root CA  |
                             | - Dynamic Discovery |
                             +---------------------+
```

---

## 3. Documentation Roadmap

Explore the comprehensive guides and references below:

- [**Architecture Diagrams**](diagrams/overview.md): Master system topology, end-to-end data flows, and interactions.

- [**Setup Guide**](setup.md): Complete, all-in-one deployment runbook for development machines, cluster nodes, systemd services, and clients.
- [**Architecture**](architecture.md):
  - **Level 1 (System Architecture)**: Logical inodes, File Identifiers (FIDs), client CNodes, ACL inheritance, locking discipline, and atomic persistence.
  - **Level 2 (Hosting Architecture & IITGN Workarounds)**: 24-hour captive portal automation (`fortinet.service`), dynamic DHCP IP discovery via Tailscale and GitHub Gist, zero-trust TLS SNI routing, and systemd sleep masking.
- [**Features & Subsystems**](features/caching.md):
  - [Client Caching](features/caching.md): In-memory CNode tree, whole-file caching, read workflows, and invalidation callbacks.
  - [Sharing & Access Control](features/sharing_acls.md): Two-tier namespace, authoritative ACL propagation, `RootShare` RPCs, and formal properties.
  - [Crash Recovery & Inode Persistence](features/crash_recovery.md): InodeStore (`.dvfs_inodes_index.json`), MetaServer state snapshots, and heartbeat monitoring.
  - [Recycle Bin & Trash](features/trash.md): Two-tier soft deletion, collision resolution, and metadata-assisted restoration.
  - [Dynamic Storage Quotas](features/quotas.md): Per-user quota enforcement, `SetQuota` gRPC protocol, and runtime adjustments.
  - [Zero-Trust TLS & PKI](features/tls_security.md): Air-gapped Root CA ceremony, DNS SAN certificates, and dynamic SNI resolution.
  - [Telemetry & Performance](features/admin_telemetry.md): HTTP sidecars, chunked streaming throughput, IOPS, and latency histograms.
  - [Remote Orchestration & Alerts](features/cluster_orchestration.md): Remote SSH management, live log streaming, and deduplicated alerts.
  - [Authentication & Security](features/authentication.md): Google OAuth 2.0 PKCE, loopback listener, Server Session Tokens (SST), centralized PEP (`fs.Authorize`), dual-mode `SetQuota`, cluster mTLS, and Admin Web Console security.
- [**Client CLI Reference**](client_cli.md): Complete manual for all 20 interactive shell commands, flags, syntax, and examples.
- [**Project Artifacts**](artifacts.md): Downloadable academic poster (`Poster.pdf`), summary of research findings, and cross-compiled release binaries.

---

## Architecture Diagrams

The following Mermaid diagram files provide precise visual documentation of every system component, grounded in the actual codebase. Each file contains multiple diagram types (topology graphs, sequence diagrams, state machines, class diagrams, and flowcharts).

| Diagram File | Component Covered | Diagrams Inside |
|---|---|---|
| [System Overview](diagrams/overview.md) | Full system, E2E flows, deployment topology, startup sequence | 6 diagrams |
| [Admin Console](diagrams/admin_system.md) | Auth flow, alert state machine, metrics pipeline, SSH orchestration | 6 diagrams |
| [FileServer Engine](diagrams/fileserver_engine.md) | Upload workflow, trash/restore, quota layers, InodeStore states | 6 diagrams |
| [MetaServer Coordinator](diagrams/metaserver_coordinator.md) | Registration, heartbeat, Navigate flow, crash recovery | 6 diagrams |
| [Client Shell and Cache](diagrams/client_system.md) | CNode structure, session lifecycle, cache flows, callback handler | 7 diagrams |
| [TLS PKI and Network](diagrams/tls_and_network.md) | Cert hierarchy, mTLS handshake, Gist IP discovery, campus workarounds | 5 diagrams |

Start with [diagrams/overview.md](diagrams/overview.md) for the complete system topology.
