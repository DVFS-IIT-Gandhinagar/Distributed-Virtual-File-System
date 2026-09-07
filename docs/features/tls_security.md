# Zero-Trust TLS & PKI Architecture

This document details the cryptographic design and transport security of DVFS, divided into its trust model (**In Essence**) and its operational implementation (**Implementation Quirks**).

---

## 1. In Essence: Zero-Trust PKI

DVFS protects all network communication using mutual TLS 1.3 based on a custom, private Public Key Infrastructure (PKI).

```
                      +-----------------------------------+
                      |      OFFLINE ROOT CA (Air-Gapped) |
                      |   certs/ca.key (RSA 4096, 10-Yr)  |
                      |   certs/ca.crt (Public Root Cert) |
                      +-----------------+-----------------+
                                        |
                        Signs all node certificates
                                        |
             +--------------------------+--------------------------+
             |                          |                          |
             v                          v                          v
    +------------------+       +------------------+       +------------------+
    |  Node dvfs1 (MDS)|       | Node dvfs2 (FS)  |       | Node dvfs3-9 (FS)|
    |  server.crt/key  |       | server.crt/key   |       | server.crt/key   |
    |  (SAN: dvfs1)    |       | (SAN: dvfs2)     |       | (SAN: dvfsX)     |
    +------------------+       +------------------+       +------------------+
             ^                          ^                          ^
             |                          |                          |
        gRPC TLS 1.3               gRPC TLS 1.3               gRPC TLS 1.3
        (SNI: dvfs1)               (SNI: dvfs2)               (SNI: dvfsX)
             |                          |                          |
             +--------------------------+--------------------------+
                                        |
                             +----------+----------+
                             |     DVFS Client     |
                             | - Embedded Root CA  |
                             | - Gist IP Discovery |
                             | - Isolated TrustPool|
                             +---------------------+
```

### Core Security Invariants
1. **Air-Gapped Root of Trust**: The Root CA private key (`ca.key`) is generated once on an administrative workstation and never deployed to any server or client machine.
2. **Hardcoded Client Trust**: Client binaries embed the public Root CA certificate directly into Go source code. The client uses an isolated `x509.CertPool` and completely ignores the host operating system's certificate store, preventing rogue CAs or enterprise proxy interception.
3. **Decoupled Identity (DNS SANs vs Dynamic IPs)**: Node certificates do not bind to volatile local IP addresses. Instead, they embed DNS Subject Alternative Names (`dvfs1` through `dvfs9`). Clients dynamically resolve IP addresses from GitHub Gist, connect to the IP, and supply the node's hostname via TLS Server Name Indication (SNI).

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Certificate Generation Suite
Certificate minting is handled by standalone Go tools in `scripts/gen-certs/`:

```
scripts/gen-certs/
├── cmd/
│   ├── gen_root_ca/main.go     # Mints 10-year RSA 4096-bit Root CA
│   └── gen_node_certs/main.go  # Signs 2-year leaf certificates for dvfs1-9
├── pki/
│   ├── ca.go                   # Root CA generation and validation
│   └── node.go                 # Node CSR creation and SAN injection
└── main.go                     # Local dev single-host cert generator (make certs)
```

#### Step 0: Local Development Certificate Generation (Single Host)
For rapid single-machine local testing, `scripts/gen-certs/main.go` automates Root CA generation, client trust pool synchronization, and server certificate minting with SANs for `localhost`, `server`, `127.0.0.1`, and local outbound LAN IP:
```bash
# Invoked via make targets
make certs             # Generates certs/ca.crt, certs/ca.key, certs/server.crt, certs/server.key
make certs-force       # Force regenerates certificates
```

#### Step 1: Root CA Generation
```bash
go run scripts/gen-certs/cmd/gen_root_ca/main.go
```
- Creates `certs/ca.crt` (Public Root CA certificate).
- Creates `certs/ca.key` (RSA 4096-bit private key with `0400` read-only owner permissions).
- Safeguard: Will abort if `certs/ca.crt` already exists to prevent accidental PKI overwrites (override with `-force`).

#### Step 2: Cluster Node Certificate Generation
```bash
go run scripts/gen-certs/cmd/gen_node_certs/main.go
```
- Reads `certs/ca.crt` and `certs/ca.key`.
- Signs 2-year leaf certificates for `dvfs1` through `dvfs9`, `localhost`, `fs1`, and `mds`.
- Outputs all artifacts into `deploy_certs/` formatted for distribution:
  ```
  deploy_certs/
  ├── ca.crt
  ├── dvfs1/ (server.crt, server.key, ca.crt)
  ├── dvfs2/ (server.crt, server.key, ca.crt)
  ...
  └── dvfs9/ (server.crt, server.key, ca.crt)
  ```

### 2.2 Client-Side Trust Embedding (`internal/client/ca.go`)
Clients require zero manual certificate setup. The public Root CA PEM block is compiled directly into the client package:

```go
const RootCAPEM = `-----BEGIN CERTIFICATE-----
MIIF6jCCA9KgAwIBAgIQ...
-----END CERTIFICATE-----`

func NewDVFSUniversalCertPool() (*x509.CertPool, error) {
    pool := x509.NewCertPool()
    if !pool.AppendCertsFromPEM([]byte(RootCAPEM)) {
        return nil, fmt.Errorf("failed to append hardcoded DVFS Root CA certificate")
    }
    return pool, nil
}
```

When connecting to servers, the client supplies this pool via `client.NewDVFSUniversalCertPool()` and `credentials.NewTLS(&tls.Config{RootCAs: pool})`.

### 2.3 Dynamic SNI Hostname Resolution
When dialing a FileServer or MetaServer, `cmd/client/main.go` uses `client.NewDiscoveryResolver()`:
1. Queries the GitHub Gist `machines.json` for current LAN IP assignments.
2. Identifies the node's hostname (e.g. `dvfs2`).
3. Sets `tls.Config{ServerName: "dvfs2"}`.
4. Dials the physical LAN IP (`10.7.52.86:50052`).
The gRPC TLS handshake verifies that the server presents a valid certificate signed by the embedded Root CA matching `dvfs2`.

### 2.4 Server Auto-Detection
MetaServer, FileServer, and Admin Server binaries automatically enable TLS if certificates exist:
- Checks `-tls_cert` and `-tls_key` flags (defaults: `certs/server.crt`, `certs/server.key`).
- If both files exist on disk, the server configures gRPC credentials with `credentials.NewServerTLSFromCert(&tlsCert)` and logs `TLS enabled with server certificate`.
- If certificate files are absent, servers safely fall back to plaintext for local development.

### 2.5 Invalidation Callback Transport Policy
While client-to-server and server-to-server channels enforce TLS 1.3, client-side invalidation callback listeners run in plaintext:
- The client starts a lightweight listener on a random local port (`lis, err := net.Listen("tcp", "0.0.0.0:0")`).
- The FileServer dials the client using `grpc.WithInsecure()`.
- Rationale: Callback payloads contain only lightweight cache invalidation pulses (`FID`, `eventType`). Running this channel without client-side certificates eliminates the operational burden of minting and distributing client certificates to end-user laptops.

## Diagrams

For a visual overview of the Zero-Trust PKI, mTLS Handshake, Dynamic IP Discovery via Gist, and IITGN Network Workarounds, see the diagrams in [tls_and_network.md](../diagrams/tls_and_network.md).
