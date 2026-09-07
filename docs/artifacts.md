# Project Artifacts & Academic Deliverables

This page catalogs the formal deliverables, academic publications, and multi-platform binary distributions for the Distributed Virtual File System (DVFS).

---

## 1. Academic Presentation Poster

The DVFS project was presented at the Indian Institute of Technology Gandhinagar (IIT Gandhinagar) as part of distributed systems coursework and research.

- **Title**: Building DVFS, a Distributed 'Cloud' for our Campus: Architecture, Protocol Design, Caching, and Multi-User Consistency over gRPC
- **Authors**: Romit Mohane, Umang Shikarvar
- **Faculty Guide**: Prof. Abhishek Bichhawat
- **Institute**: Department of Computer Science & Engineering, IIT Gandhinagar

### Download Document
Direct download link to the ready-to-print vector presentation poster:

- [**Download Academic Poster (PDF)**](assets/Poster.pdf)

---

### Poster Specification & Architectural Summary

```
+---------------------------------------------------------------------------------------+
|  BUILDING DVFS: A DISTRIBUTED 'CLOUD' FOR OUR CAMPUS                                  |
|  Authors: Romit Mohane, Umang Shikarvar | Guide: Prof. Abhishek Bichhawat (IITGN)     |
+---------------------------------------------------------------------------------------+
| 1. Abstract & Goals        | 3. Initiation Sequence      | 5. Core Data Structures    |
| - Userspace distributed VFS| - Client GetRoots query     | - FID: fs1_42_1            |
| - AFS-style local caching  | - Advisory node selection   | - Inode (authoritative)    |
| - Zero-Trust mTLS 1.3      | - Secure session connect    | - CNode (client mirror)    |
| - Zero polling overhead    | - Interactive REPL startup  | - ACL: Owner + Shared      |
+----------------------------+-----------------------------+----------------------------+
| 2. System Architecture     | 4. Protocol & Stream Flows  | 6. Results & Takeaways     |
| - MetaServer control plane | - Chunked upload streaming  | - Zero-RPC local reads     |
| - FileServer data plane    | - Invalidation callbacks    | - Seamless DHCP recovery   |
| - Client presentation plane| - Subtree DFS propagation   | - Instantaneous UI sync    |
+---------------------------------------------------------------------------------------+
```

#### Core Takeaways from the Academic Study
1. **Low-Latency Interactive UX**: By maintaining an in-memory `CNode` tree mirroring the remote hierarchy, directory inspection commands (`ls`, `cd`, `pwd`, `viscache`) execute entirely in local userspace with zero network round trips.
2. **Push-Driven Cache Invalidation**: Server-push gRPC callbacks eliminate polling traffic, ensuring connected clients observe updates immediately without repeatedly querying fileserver status.
3. **Campus-Resilient Zero-Trust PKI**: Decoupling certificate validation from volatile DHCP IP addresses via DNS SANs enables headless cluster nodes to remain cryptographically authenticated across dynamic IP reassignments.

---

## 2. Multi-Platform Binary Distributions

DVFS includes a native, pure Go cross-compilation release builder (`scripts/build-release/main.go`). It compiles stripped, self-contained, statically linked binaries (`CGO_ENABLED=0`, `-ldflags="-s -w"`) with embedded Root CA certificates and automated SHA-256 checksum generation.

### Standalone Client Executables
Users can download and run standalone client executables directly without installing the Go development toolchain:

| Target Platform | Architecture | Standalone Executable | Compressed Archive |
|---|---|---|---|
| **Windows** | x86_64 / amd64 | `dvfs-client-windows-amd64.exe` | `dvfs-client-windows-amd64.zip` |
| **Linux** | x86_64 / amd64 | `dvfs-client-linux-amd64` | `dvfs-client-linux-amd64.tar.gz` |
| **Linux (ARM)** | aarch64 / arm64 | `dvfs-client-linux-arm64` | `dvfs-client-linux-arm64.tar.gz` |
| **macOS (Apple Silicon)** | arm64 (M1/M2/M3/M4) | `dvfs-client-darwin-arm64` | `dvfs-client-darwin-arm64.tar.gz` |
| **macOS (Intel)** | x86_64 / amd64 | `dvfs-client-darwin-amd64` | `dvfs-client-darwin-amd64.tar.gz` |

### Cluster Node Bundles
Complete server distributions (containing `fileserver`, `metaserver`, `admin`, systemd units, and setup scripts):

| Target Architecture | Description | Distribution Package |
|---|---|---|
| **Linux ARM64** | Raspberry Pi 4 / Raspberry Pi 5 cluster nodes (`dvfs1` through `dvfs9`) | `dvfs-nodes-linux-arm64.tar.gz` |
| **Linux AMD64** | x86 Ubuntu Live Server cluster nodes | `dvfs-nodes-linux-amd64.tar.gz` |

### Building Releases Locally
To compile all distributions locally from source:

```bash
# Build all client and node distributions
make release

# Build client standalone executables only
make release-client

# Build cluster node bundles only
make release-nodes
```

All compiled packages and `SHA256SUMS.txt` are exported into the `release/` directory. Automated releases are published to GitHub Releases upon pushing version tags (`git push origin v1.x.x`).
