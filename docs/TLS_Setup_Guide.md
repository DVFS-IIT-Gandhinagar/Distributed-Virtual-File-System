# DVFS Zero-Trust TLS Setup & Operations Guide

This guide provides step-by-step instructions for generating certificates, distributing secrets, starting services, and verifying TLS across the DVFS cluster.

---

## 1. Architecture & Trust Model Overview

The DVFS TLS architecture operates on a **zero-trust PKI** model:

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
                         |    DVFS Client      |
                         | - Embedded Root CA  |
                         | - Gist IP Discovery |
                         | - Isolated TrustPool|
                         +---------------------+
```

### Key Security Principles
1. **Air-Gapped Root CA**: The private key `ca.key` is **never** deployed to any server or client node. It stays strictly offline on the certificate administrator machine.
2. **Hardcoded Client Trust**: The client binary embeds the public Root CA directly in Go source code (`internal/client/ca.go`). It uses an isolated `x509.CertPool` and ignores the host OS certificate store.
3. **Decoupled DHCP IPs via DNS SANs**: Node certificates are minted with DNS SANs (`dvfs1` through `dvfs9`). The client queries node LAN IPs dynamically from the GitHub Gist, dials the IP, but specifies the node's hostname via TLS SNI (`ServerName = "dvfsX"`). Handshakes succeed regardless of DHCP IP reassignments.
4. **Callback Transport Policy (Option A)**: Fileservers dial client invalidation listeners in plaintext (`grpc.WithInsecure()`). Callbacks are lightweight FID/version pulses only, eliminating client-side certificate provisioning.

---

## 2. Step 1: Certificate Generation Ceremony

Run these commands on your administrative / workstation machine in the repository root.

### 2.1 Generate the Offline Root CA (10+ Year Validity)
```bash
go run scripts/gen-certs/cmd/gen_root_ca/main.go
```
- Outputs:
  - `certs/ca.crt` (Public Root CA certificate)
  - `certs/ca.key` (RSA 4096-bit private key with `0600` permissions)
- *Note:* If `certs/ca.crt` already exists, the script aborts to protect against accidental overwrite. Use `-force` if you intentionally want to regenerate.

### 2.2 Generate Node Certificates
```bash
go run scripts/gen-certs/cmd/gen_node_certs/main.go
```
- This automatically loads `certs/ca.crt` and `certs/ca.key` and signs individual 2-year leaf certificates for:
  - `dvfs1` through `dvfs9`
  - `localhost`
  - `fs1`, `mds`
- Outputs to `deploy_certs/`:
  ```
  deploy_certs/
  ├── ca.crt                  <- Public CA certificate
  ├── dvfs1/
  │   ├── server.crt          <- Certificate with DNS SAN dvfs1, dvfs1.local, localhost
  │   ├── server.key          <- 4096-bit RSA Private Key (0600)
  │   └── ca.crt
  ├── dvfs2/
  │   ├── server.crt
  │   ├── server.key
  │   └── ca.crt
  ...
  └── dvfs9/
      ├── server.crt
      ├── server.key
      └── ca.crt
  ```

### 2.3 Cryptographic Verification
Verify the generated certificates against the Root CA:
```bash
# Verify Root CA properties
openssl x509 -in deploy_certs/ca.crt -text -noout | grep -E "(Issuer|Subject|Not After|CA:TRUE)"

# Verify node certificate validity and signature chain
openssl verify -CAfile deploy_certs/ca.crt deploy_certs/dvfs1/server.crt
openssl verify -CAfile deploy_certs/ca.crt deploy_certs/dvfs2/server.crt
```
Expected output:
```text
deploy_certs/dvfs1/server.crt: OK
deploy_certs/dvfs2/server.crt: OK
```

---

## 3. Step 2: Distribution to Cluster Nodes

### Distribution Matrix

| Node | Hostname / Role | Files to Copy | Remote Destination | Permissions |
|---|---|---|---|---|
| **dvfs1** | Metaserver & Admin | `deploy_certs/dvfs1/server.crt`<br/>`deploy_certs/dvfs1/server.key`<br/>`deploy_certs/ca.crt` | `~/Distributed-Virtual-File-System/certs/` | `server.key`: `0600`<br/>`*.crt`: `0644` |
| **dvfs2** | Fileserver 2 | `deploy_certs/dvfs2/server.crt`<br/>`deploy_certs/dvfs2/server.key`<br/>`deploy_certs/ca.crt` | `~/Distributed-Virtual-File-System/certs/` | `server.key`: `0600`<br/>`*.crt`: `0644` |
| **dvfs3** | Fileserver 3 | `deploy_certs/dvfs3/server.crt`<br/>`deploy_certs/dvfs3/server.key`<br/>`deploy_certs/ca.crt` | `~/Distributed-Virtual-File-System/certs/` | `server.key`: `0600`<br/>`*.crt`: `0644` |
| ... | ... | ... | ... | ... |
| **dvfs9** | Fileserver 9 | `deploy_certs/dvfs9/server.crt`<br/>`deploy_certs/dvfs9/server.key`<br/>`deploy_certs/ca.crt` | `~/Distributed-Virtual-File-System/certs/` | `server.key`: `0600`<br/>`*.crt`: `0644` |
| **Client** | Client workstations | **NONE** (Root CA is hardcoded in binary) | N/A | N/A |

> [!CAUTION]
> **CRITICAL SECURITY DIRECTIVE:**
> **DO NOT copy `ca.key` to any cluster node.** `ca.key` must remain only on your secure offline machine.

### Deployment Script / Commands

From your administrative workstation, distribute the bundles via SSH / SCP:

```bash
# Example for dvfs1 (Metaserver)
ssh dvfs1 "mkdir -p ~/Distributed-Virtual-File-System/certs"
scp deploy_certs/dvfs1/server.crt dvfs1:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/dvfs1/server.key dvfs1:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/ca.crt dvfs1:~/Distributed-Virtual-File-System/certs/
ssh dvfs1 "chmod 600 ~/Distributed-Virtual-File-System/certs/server.key && chmod 644 ~/Distributed-Virtual-File-System/certs/*.crt"

# Example for dvfs2 (Fileserver)
ssh dvfs2 "mkdir -p ~/Distributed-Virtual-File-System/certs"
scp deploy_certs/dvfs2/server.crt dvfs2:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/dvfs2/server.key dvfs2:~/Distributed-Virtual-File-System/certs/
scp deploy_certs/ca.crt dvfs2:~/Distributed-Virtual-File-System/certs/
ssh dvfs2 "chmod 600 ~/Distributed-Virtual-File-System/certs/server.key && chmod 644 ~/Distributed-Virtual-File-System/certs/*.crt"
```

---

## 4. Step 3: Starting Cluster Services with TLS

### 4.1 Metaserver (`dvfs1`)
Using the startup wrapper (auto-detects `certs/server.crt` and `certs/server.key`):
```bash
./scripts/start-metaserver.sh
```
Or directly via binary:
```bash
./bin/metaserver \
  -port=50051 \
  -tls_cert="certs/server.crt" \
  -tls_key="certs/server.key"
```
**Log Confirmation:**
You should see:
```text
TLS enabled with server certificate
Meta server starting on 0.0.0.0:50051
```

### 4.2 Fileserver (`dvfs2`–`dvfs9`)
Using the startup wrapper:
```bash
./scripts/start-fileserver.sh
```
Or directly via binary:
```bash
./bin/fileserver \
  -id="fs2" \
  -port=50052 \
  -meta_addr="10.0.171.38:50051" \
  -own_ip="10.0.171.39" \
  -tls_cert="certs/server.crt" \
  -tls_key="certs/server.key"
```
**Log Confirmation:**
You should see:
```text
TLS enabled with server certificate
File server starting on 0.0.0.0:50052
```
And fileserver-to-metaserver registration will dial over Root-CA TLS targeting `ServerName: "dvfs1"`.

### 4.3 Admin Console (`dvfs1` or Management Node)
Using the startup wrapper:
```bash
./scripts/start-admin.sh
```
Or directly via binary:
```bash
./bin/admin \
  -port=8080 \
  -tls_cert="certs/server.crt" \
  -tls_key="certs/server.key"
```
**Log Confirmation:**
You should see:
```text
[ADMIN] Direct TLS configured (cert: certs/server.crt, key: certs/server.key)
[ADMIN] Listening on https://0.0.0.0:8080 (Direct TLS), serving static from ./cmd/admin/static
```
- Access the dashboard at `https://<ip>:8080`.
- All session cookies (`dvfs_admin_token`) are automatically set with `Secure: true`.
- Real-time WebSocket streaming connects via encrypted `wss://<ip>:8080/ws/actions`.

---

## 5. Step 4: Running the Client

### 5.1 Compile the Client Binary
Build the client binary (it has the Root CA public key compiled into its code):
```bash
go build -o ./bin/client cmd/client/main.go
```

### 5.2 Standard Execution (Dynamic Discovery via GitHub Gist)
Run the client without passing any IP flags:
```bash
./bin/client -username=alice
```
**What happens under the hood:**
1. Client fetches `https://gist.githubusercontent.com/dvfs-iitgn/6eb8da397735b83f76b54af4cca64c83/raw/machines.json`.
2. Locates `dvfs1`'s current IP (e.g., `10.0.171.38`).
3. Saves a local copy to `~/.dvfs/machines_cache.json` as offline fallback.
4. Dials `10.0.171.38:50051` with TLS SNI `ServerName = "dvfs1"` and validates against embedded Root CA.
5. On file navigation, dials the target fileserver's IP with SNI `ServerName = "dvfsX"`.

### 5.3 Local Development / Offline Override
To run against a local mock or bypass Gist discovery:
```bash
# Connect to localhost over TLS (localhost SAN is in the certificate)
./bin/client -username=alice -ip_addr=127.0.0.1 -use_gist=false

# Connect with insecure plaintext (for unit testing only)
./bin/client -username=alice -ip_addr=127.0.0.1 -use_gist=false -insecure=true
```

---

## 6. Verification & Troubleshooting

### Check 1: Verify Node Certificate Expiration & SANs
```bash
openssl x509 -in certs/server.crt -text -noout | grep -A 2 "Subject Alternative Name"
```
Output must show:
```text
X509v3 Subject Alternative Name:
    DNS:dvfsX, DNS:dvfsX.local, DNS:dvfsX.dvfs.cluster, DNS:localhost, IP Address:127.0.0.1
```

### Check 2: Test Admin Console HTTPS Endpoint
```bash
# Query the health endpoint using the Root CA
curl --cacert certs/ca.crt https://127.0.0.1:8080/health
```
Output:
```json
{"status":"ok"}
```

### Check 3: Common Issues & Solutions

| Issue | Cause | Fix |
|---|---|---|
| `x509: certificate signed by unknown authority` | Server presented a cert not signed by the DVFS Root CA, or client has wrong CA embedded | Regenerate node certs with `scripts/gen-certs/cmd/gen_node_certs/main.go` using the active Root CA. |
| `x509: certificate is valid for dvfs1, not 10.0.171.38` | Client dialed an IP without setting TLS SNI `ServerName` | The client automatically sets SNI via `DiscoveryResolver`. If manually dialing, specify `ServerName: "dvfs1"`. |
| `permission denied` opening `server.key` | `server.key` permissions are too restrictive for the current user | Run `chmod 600 certs/server.key` as the service owner user. |
| Gist fetch failed or rate limited | GitHub network issue or offline mode | Client automatically falls back to `~/.dvfs/machines_cache.json`. To pre-populate or override, edit that file. |
| Browser warns `NET::ERR_CERT_AUTHORITY_INVALID` on Admin Console | Browser's system store does not have the custom Root CA installed | Normal for custom Root CAs. You can either import `certs/ca.crt` into your browser/system trust store, or click "Advanced -> Proceed". |
