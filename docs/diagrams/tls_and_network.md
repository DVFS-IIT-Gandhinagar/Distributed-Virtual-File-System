# DVFS ZERO-TRUST PKI, TLS CONFIGURATION, and NETWORK TOPOLOGY

This document provides comprehensive Mermaid diagrams detailing the DVFS cryptographic design, transport security, and network workarounds.

## 1. Zero-Trust PKI Certificate Hierarchy

```mermaid
graph TD
    AdminBoundary["Air-gap boundary: ca.key never leaves admin machine"] --- RootCA
    RootCA["RootCA\nca.key (0400) + ca.crt"]
    
    GenRootCA["gen_root_ca.go"] -->|generates| RootCA
    
    NodeCert1["Node Certificate\nserver.crt + server.key (0600)\nDNS SANs: dvfs1"]
    NodeCertN["Node Certificate\nserver.crt + server.key (0600)\nDNS SANs: dvfs9"]
    
    RootCA -->|signs| NodeCert1
    RootCA -->|signs| NodeCertN
    
    GenNodeCerts["gen_node_certs.go / main.go"] -->|generates| NodeCert1
    GenNodeCerts -->|generates| NodeCertN
    
    Client["DVFSClient"] -->|embeds| CACrt["ca.crt via NewDVFSUniversalCertPool"]
```

## 2. mTLS Handshake Flow

```mermaid
sequenceDiagram
    participant DVFSClient
    participant FileServer

    Note over DVFSClient: Client resolves physical IP from Gist
    DVFSClient->>FileServer: Dial IP (SNI: dvfs1) / TLS ClientHello
    FileServer-->>DVFSClient: server.crt (DNS SAN: dvfs1)
    Note over DVFSClient: Verifies against embedded ca.crt pool (NewDVFSUniversalCertPool)
    FileServer->>DVFSClient: Request client cert (for mutual TLS)
    DVFSClient-->>FileServer: client cert
    Note over DVFSClient,FileServer: Connection established with TLS 1.3
    Note over DVFSClient,FileServer: DHCP IP Decoupling: physical IP changes but SNI hostname stays constant
```

## 3. Dynamic IP Discovery via GitHub Gist

```mermaid
sequenceDiagram
    participant UpdateGistScript
    participant TailscaleAPI
    participant RemoteNode
    participant GitHubGist
    participant DVFSClient

    Note over UpdateGistScript: Hourly Flow
    UpdateGistScript->>TailscaleAPI: Query API (tag:dvfsmachines)
    TailscaleAPI-->>UpdateGistScript: Return overlay IPs
    UpdateGistScript->>RemoteNode: SSH over Tailscale
    RemoteNode-->>UpdateGistScript: Return physical campus LAN IP (eth interface)
    UpdateGistScript->>GitHubGist: PATCH machines.json (hostname -> LAN IP mapping)
    
    Note over DVFSClient: Client Startup Flow
    DVFSClient->>GitHubGist: Fetch URL
    GitHubGist-->>DVFSClient: machines.json
    DVFSClient->>DVFSClient: Parse machines.json
    DVFSClient->>RemoteNode: Dial physical LAN IP with SNI = hostname
```

## 4. IITGN Campus Network Workarounds

```mermaid
graph TD
    Layer1["Layer 1: Campus Ethernet\n(dynamic DHCP, Fortinet captive portal)"]
    Layer2["Layer 2: connect.sh + fortinet.service\nPOST to fwg.iitgn.ac.in (CSRF token extraction)"]
    Layer3["Layer 3: Tailscale overlay network\n(stable 100.x IPs for management)"]
    Layer4["Layer 4: update_gist.py hourly\nGitHub Gist machines.json (LAN IP discovery)"]
    Layer5["Layer 5: persist.sh\nmask sleep/suspend/hibernate targets"]
    
    Layer1 -->|"Solves portal timeouts"| Layer2
    Layer2 -->|"Solves reachability"| Layer3
    Layer3 -->|"Solves client routing"| Layer4
    Layer4 -->|"Solves hardware power-saving"| Layer5
```

## 5. Certificate Generation Workflow

```mermaid
flowchart TD
    MakeCerts["make certs"] --> MainGo["scripts/gen-certs/main.go"]
    MakeForce["make certs-force"] -->|always regenerate| MainGo
    
    MainGo --> CheckCA{"Check if ca.crt exists"}
    
    CheckCA -->|No| GenRoot["gen_root_ca"]
    GenRoot --> OutRoot["ca.key (0400) + ca.crt"]
    
    CheckCA -->|Yes| ForEachNode
    OutRoot --> ForEachNode["For each node (dvfs1..dvfs9)"]
    
    ForEachNode --> GenNode["gen_node_certs"]
    GenNode --> OutNode["server.key (0600) + server.crt (DNS SAN: dvfsN)"]
    
    OutNode --> CopyCA["Copy ca.crt to client embed path"]
