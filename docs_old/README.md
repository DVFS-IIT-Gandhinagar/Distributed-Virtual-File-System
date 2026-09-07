# DVFS Documentation Index

Welcome to the documentation for the **Distributed Virtual File System (DVFS)** — an Andrew File System (AFS)-inspired, high-performance distributed virtual file system implemented in Go, secured with mutual TLS, and designed for cluster environments (such as Raspberry Pi clusters).

---

## 1. Cluster Setup & Deployment (`docs/setup/`)

Step-by-step guides for installing, configuring, securing, and operating the DVFS cluster in a multi-node environment:

| Guide | Description | Target Audience |
|---|---|---|
| [**Cluster Setup Guide**](./setup/cluster_setup.md) | Complete guide to configuring dev machines, the Gist discovery service, Metaserver, Admin Console, and Fileservers. | Administrators & DevOps |
| [**TLS & PKI Setup Guide**](./setup/tls_setup.md) | Air-gapped Root CA generation, multi-node certificate minting with DNS SANs, distribution via `scp`, and verification. | Security & Operations |
| [**SSH & Systemd Setup Guide**](./setup/ssh_and_systemd.md) | Setting up passwordless SSH, `/etc/sudoers.d/dvfs` privilege delegation, and systemd service templates. | Node Administrators |

---

## 2. System Architecture (`docs/architecture/`)

Detailed design documents describing system topology and communication protocols:

| Document | Focus |
|---|---|
| [**Protocol Flow & Sequence Diagrams**](./architecture/flow.md) | Client bootstrap, metaserver advisory routing, fileserver registration, heartbeat monitoring, and directory navigation flows. |

---

## 3. Subsystem Features (`docs/features/`)

Technical specifications for core filesystem capabilities:

| Feature | Description |
|---|---|
| [**Client-Side Caching**](./features/cache.md) | Local client read/write caching, cache hierarchies, and AFS-style cache consistency models. |
| [**Sharing & Access Control**](./features/sharing.md) | Two-tier namespace (`mydrive` / `shared`), authoritative fileserver ACL enforcement, and metaserver shared index. |
| [**Crash Recovery**](./features/crash_recovery.md) | State serialization, restart recovery, and persistent inode index reconstruction. |
| [**Heartbeat Monitoring**](./features/heartbeat.md) | Health tracking, least-loaded node allocation, and stale server pruning. |
| [**Recycle Bin & Trash**](./features/trash.md) | Soft deletion, directory restoration, and permanent pruning policies. |

---

## 4. Project Roadmaps & Technical Plans (`docs/project/`)

Architecture proposals, implementation roadmaps, and phase designs:

| Plan | Overview |
|---|---|
| [**TLS Security Plan**](./project/tls_plan.md) | Architecture for air-gapped Root CA, embedded client trust anchors, Gist SNI resolution, and TLS 1.3 encryption. |
| [**Admin Console Plan (Phases 1–3)**](./project/admin_console_plan.md) | Real-time telemetry scraping, dynamic user quota management, and remote SSH cluster orchestration. |
| [**Admin Console Performance Plan (Phase 4)**](./project/phase_4_admin_console_plan.md) | Cluster throughput streaming, IOPS calculations, and latency percentile tracking (p50, p95, p99). |
| [**Fileserver Restart & Persistence**](./project/fileserver_restart_cache_recovery.md) | Persistent inode mapping, cache recovery, and offline directory consistency. |
| [**Phase 1 Specification**](./project/phase-1.md) | Original DVFS architecture, gRPC RPC specifications, and component division. |
| [**Project Todo & Tracker**](./project/todo.md) | Historical task tracker and development milestone checklist. |

---

## 5. Presentation Materials (`docs/poster/`)

- [**Academic Poster Specification**](./poster/Poster.md): Academic overview, architectural diagrams, performance results, and design tradeoffs.
- [**Poster PDF**](./poster/Poster.pdf): Ready-to-print project presentation poster.
