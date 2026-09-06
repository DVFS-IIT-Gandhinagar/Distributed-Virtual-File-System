# DVFS Dynamic TLS Migration & Security Architecture Plan

**Document Version:** 1.0  
**Author:** Senior Distributed Systems & Security Engineer  
**Date:** September 2026  
**Status:** PROPOSED (Pending Review & Sign-Off)  
**Target File:** `docs/TLS_Plan.md`  

---

## Executive Summary

The Distributed Virtual File System (DVFS) cluster consists of a Metaserver (MDS), up to nine Fileservers (`dvfs1` through `dvfs9`), and client binaries operated by users on diverse client machines. The current security posture relies on statically distributed certificates, hardcoded/flag-driven IP routing, and disabled-by-default TLS in systemd units.

This document presents an end-to-end architectural plan to migrate DVFS to a **Zero-Trust PKI & Dynamic Discovery Architecture**:
1. **Root of Trust:** An air-gapped, offline Custom Root Certificate Authority (CA) with a 10+ year validity period. Its private key never touches cluster servers or network nodes.
2. **Dynamic Routing:** Client discovery powered by GitHub Gist JSON polling (`machines.json`) maintained by the cluster's network daemon.
3. **Strict Cryptographic Verification:** Direct hardcoding of the Root CA public key block (PEM) into client binaries, enforcing isolated certificate validation (`x509.CertPool`) that completely bypasses and ignores the host operating system's trust store.
4. **Resilient Identity Validation:** Solving the dynamic DHCP IP vs TLS certificate Subject Alternative Name (SAN) challenge via host-identity SNI mapping.
5. **Admin Console Security Hardening:** Critical audit of the current administrative password authentication system, identifying vulnerabilities and remediation steps.

---

## 1. Current State Audit & Architectural Vulnerabilities

An exhaustive code and infrastructure audit of the DVFS repository revealed the following architectural facts and critical security flaws:

```
[CURRENT RUdimentary TLS TOPOLOGY]
+-------------------------------------------------------------+
|  scripts/gen-certs/main.go                                  |
|  - Generates BOTH ca.key + ca.crt AND server.key + server.crt |
|  - All written into same ./certs/ directory                |
|  - LAN IP detected via UDP dial to 8.8.8.8 and baked to SAN |
+-------------------------------------------------------------+
        |                                       |
        v                                       v
+------------------+                    +--------------------+
| Metaserver / FS  |                    | Client Binary      |
| - Reads certs/   |                    | - Reads ca_cert    |
|   server.crt/key |                    |   file from disk   |
| - TLS disabled   |                    | - Static IP flags  |
|   by default     |                    | - TLS optional     |
+------------------+                    +--------------------+
```

### 1.1 Co-location of CA Private Key and Server Certificates
In `scripts/gen-certs/main.go`, the script generates a 4096-bit Root CA and an end-entity server certificate in a single execution. Both `ca.key` and `server.key` are written to `./certs/`. In a multi-node deployment, if the repository is cloned across Raspberry Pi nodes or server images, the Root CA private key is at risk of being copied onto edge devices. Compromise of a single node would compromise the entire PKI.

### 1.2 Brittle IP-in-SAN Binding
In `scripts/gen-certs/main.go` lines 69-83, the certificate embeds the machine's local LAN IP at the time of certificate generation into the certificate SAN list. If the node's IP changes via DHCP or network migration:
- gRPC clients dialing the new IP will fail TLS certificate verification with `x509: certificate is valid for 10.7.52.85, not 10.0.171.38`.
- Reissuing certificates requires re-running the generator with the CA key.

### 1.3 Asymmetric Callback TLS Deadlock (Critical Bug)
In `internal/client/callback_server.go:21-39`, the client starts an invalidation callback server:
```go
lis, err := net.Listen("tcp", "0.0.0.0:0")
grpcServer := grpc.NewServer() // Plaintext listener! No TLS!
cbpb.RegisterClientCallbackServer(grpcServer, &callbackServer{client: c})
```
However, in `internal/fileserver/callback_server.go:187-207`, when the fileserver runs with `useTLS = true`, it dials the client callback server using TLS:
```go
if fs.useTLS {
    creds := credentials.NewClientTLSFromCert(cp, host)
    opts = append(opts, grpc.WithTransportCredentials(creds))
}
conn, err := grpc.NewClient(target.callbackAddress, opts...)
```
**Result:** Enabling `--tls=true` on both servers and clients causes all cache invalidation callbacks from fileservers to clients to fail silently or error out during the TLS handshake because the client is speaking plaintext.

### 1.4 Systemd Deployment Runs in Plaintext
In `scripts/start-fileserver.sh`, `scripts/start-metaserver.sh`, and systemd units `dvfs-fileserver.service` and `dvfs-metaserver.service`, the binaries are launched without `-tls`, `-tls_cert`, or `-tls_key` flags. All lab deployment communication currently runs over unencrypted plaintext gRPC.

---

## 2. Target Architecture Specification

```
+---------------------------------------------------------------------------------+
|                               AIR-GAPPED OFFLINE                                |
|                                                                                 |
|   +---------------------+             +-------------------------------------+   |
|   | Root CA Private Key | (Never      | Root CA Certificate (ca.crt)        |   |
|   | ca.key (4096-bit)   |  leaves     | CN=DVFS Offline Root CA, 10+ Years  |   |
|   +----------+----------+  offline)   +------------------+------------------+   |
+--------------|-------------------------------------------|----------------------+
               |                                           |
               | (Signs server certs once)                 | (Embedded into code)
               v                                           v
+-----------------------------+             +-------------------------------------+
| Node Certificates           |             | Client Binary Source                |
| - dvfs1.crt / dvfs1.key     |             | `internal/client/ca.go`             |
| - dvfs2.crt / dvfs2.key     |             | const RootCAPEM = `...`             |
| - ... through dvfs9.crt/key |             | Isolated x509.CertPool              |
+--------------+--------------+             +------------------+------------------+
               | (Deployed to nodes)                           |
               v                                               v
+-----------------------------+  HTTPS Fetch       +------------------------------+
| Cluster Nodes (dvfs1-dvfs9) | <----------------- | Client Discovery Routine     |
| Serving gRPC with node cert |  Gist machines.json| - Resolves dvfs1 IP (MDS)    |
| SAN: DNS:dvfsX, dvfsX.local |                    | - Resolves dvfs2-9 IPs (FS)  |
+--------------^--------------+                    +---------------+--------------+
               |                                                   |
               +================= gRPC over TLS ===================+
                     Client dials IP:PORT, TLS SNI = "dvfsX"
                     Verified strictly against RootCAPEM
```

### 2.1 Core Pillars
1. **Isolated Trust Anchor:** The client binary trusts exactly one CA: the DVFS Offline Root CA. Host OS certificates (`/etc/ssl/certs`, Windows CryptoAPI, macOS Keychain) are excluded from the gRPC TLS CertPool. A compromised corporate proxy or rogue public CA cannot forge DVFS certificates.
2. **Dynamic Gist Discovery:** The client uses an HTTPS client (leveraging OS TLS for public GitHub traffic) to fetch `machines.json` from the raw Gist URL, obtaining current mappings of hostname -> LAN IP.
3. **Name-Based TLS Identity Binding:** Instead of embedding volatile IP addresses into server certificates, each node is issued a certificate with a canonical DNS SAN (e.g., `dvfs1`, `dvfs1.dvfs.local`, `*.dvfs.cluster`). When the client connects to an IP fetched from the Gist, it explicitly sets `tls.Config.ServerName` to the node's hostname (`dvfs1`). This decouples network addressing from cryptographic identity.
4. **Defense in Depth against Gist Tampering:** If an attacker tampers with the GitHub Gist or performs a Man-in-the-Middle (MitM) attack on the HTTPS Gist fetch to substitute malicious server IPs, the client's subsequent gRPC connection will abort immediately during the TLS handshake because the rogue server cannot present a certificate signed by the private Root CA key.

---

## 3. Detailed Migration Phases

### Phase 1: Offline Root CA Generation (Air-Gapped Ceremony)

Create a dedicated script `scripts/gen-certs/gen_root_ca.go` that runs once in a secure, air-gapped environment.

```
Key Parameters:
- Algorithm: RSA 4096-bit (or ECDSA P-384 / Ed25519)
- Signature Algorithm: SHA-384 with RSA (or SHA-256)
- Validity: 10 years (3650 days + leap days = 3652 days)
- Basic Constraints: IsCA = true, MaxPathLen = 1, MaxPathLenZero = false
- Key Usage: KeyUsageCertSign | KeyUsageCRLSign | KeyUsageDigitalSignature
- Subject: CN="DVFS Offline Root CA", O="DVFS Project", OU="Security", C="IN", ST="Gujarat", L="Gandhinagar"
```

**Artifacts Generated:**
- `root_ca/ca.key` (Permissions: `0400` / read-only owner; stored on encrypted offline storage / USB drive).
- `root_ca/ca.crt` (Permissions: `0644`; public certificate distributed for node signing and client embedding).

---

### Phase 2: Multi-Node Server Certificate Generation & Signing

Create `scripts/gen-certs/gen_node_certs.go` to generate private keys and signed certificates for all cluster nodes:
- Metaserver: `dvfs1`
- Fileservers: `dvfs1`, `dvfs2`, `dvfs3`, `dvfs4`, `dvfs5`, `dvfs6`, `dvfs7`, `dvfs8`, `dvfs9`
- Standalone / Local Development: `localhost`, `fs1`, `mds`

```
Node Certificate Parameters:
- Validity: 2 years (730 days) — allows predictable lifecycle rotation.
- Key Usage: KeyUsageDigitalSignature | KeyUsageKeyEncipherment
- ExtKeyUsage: ExtKeyUsageServerAuth (and ExtKeyUsageClientAuth for mTLS readiness)
- Subject: CN = "dvfsX", O = "DVFS Project"
- Subject Alternative Names (SANs):
  * DNS Names:
    - dvfsX
    - dvfsX.local
    - dvfsX.dvfs.cluster
    - localhost (for local loopback testing)
  * IP Addresses (Static / Fallback SANs):
    - 127.0.0.1
    - Known static LAN IP from deployment plan (optional fallback)
```

**Output Structure:**
```
deploy_certs/
├── ca.crt                  # Public CA certificate
├── dvfs1/
│   ├── server.crt          # dvfs1 certificate (signed by Root CA)
│   └── server.key          # dvfs1 private key (RSA 4096, 0600)
├── dvfs2/
│   ├── server.crt
│   └── server.key
...
└── dvfs9/
    ├── server.crt
    └── server.key
```

---

### Phase 3: Server-Side Deployment & Secret Isolation

1. **Deploy to Nodes:** Transfer only `dvfsX/server.crt`, `dvfsX/server.key`, and `ca.crt` to `/opt/dvfs/certs/` or `./certs/` on node `dvfsX` via secure SCP/Ansible:
   ```bash
   chmod 600 /opt/dvfs/certs/server.key
   chmod 644 /opt/dvfs/certs/server.crt /opt/dvfs/certs/ca.crt
   ```
2. **Strict Exclusion:** The private key `ca.key` **MUST NOT** be copied to any server node. Add `ca.key` to `.gitignore` and deployment exclusion filters.
3. **Enable TLS Flags in Server Startup:**
   Update `scripts/start-metaserver.sh`:
   ```bash
   exec ./bin/metaserver \
     -port="${META_PORT:-50051}" \
     -tls=true \
     -tls_cert="certs/server.crt" \
     -tls_key="certs/server.key" \
     "$@"
   ```
   Update `scripts/start-fileserver.sh`:
   ```bash
   exec ./bin/fileserver \
     -id="${FS_ID}" \
     -port="${FS_PORT}" \
     -data="${DATA_DIR}" \
     -meta_addr="${META_ADDR}" \
     -own_ip="${OWN_IP}" \
     -tls=true \
     -tls_cert="certs/server.crt" \
     -tls_key="certs/server.key" \
     -ca_cert="certs/ca.crt" \
     "$@"
   ```

---

### Phase 4: Client Architecture — Hardcoded Trust Anchor & Dynamic Gist Discovery

#### 4.1 Hardcoded Root CA in Client
Create `internal/client/ca.go`:
```go
package client

import (
	"crypto/x509"
	"fmt"
)

// RootCAPEM is the raw PEM-encoded certificate of the offline DVFS Root CA.
// Embedded at build time; clients do not require external ca.crt files.
const RootCAPEM = `-----BEGIN CERTIFICATE-----
MIIEczCCA1ugAwIBAgIU... [10-YEAR OFFLINE ROOT CA PEM BLOCK] ...
-----END CERTIFICATE-----`

// NewDVFSUniversalCertPool creates a dedicated x509.CertPool containing strictly
// the embedded DVFS Root CA, completely ignoring the host OS trust store.
func NewDVFSUniversalCertPool() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM([]byte(RootCAPEM)); !ok {
		return nil, fmt.Errorf("failed to parse hardcoded DVFS Root CA PEM")
	}
	return pool, nil
}
```

#### 4.2 Dynamic Gist Resolver Module
Create `internal/client/discovery.go`:
```go
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	DefaultGistURL = "https://gist.githubusercontent.com/dvfs-iitgn/6eb8da397735b83f76b54af4cca64c83/raw/machines.json"
	GistTimeout    = 8 * time.Second
)

type GistNode struct {
	Username string `json:"username"` // "dvfs1", "dvfs2", ...
	MAC      string `json:"mac"`
	IP       string `json:"ip"`       // "10.0.171.38"
	LastSeen string `json:"last_seen"`
}

type DiscoveryResolver struct {
	gistURL    string
	httpClient *http.Client
	mu         sync.RWMutex
	cache      map[string]string // hostname -> IP
	lastFetch  time.Time
}

func NewDiscoveryResolver(gistURL string) *DiscoveryResolver {
	if gistURL == "" {
		gistURL = DefaultGistURL
	}
	return &DiscoveryResolver{
		gistURL: gistURL,
		httpClient: &http.Client{
			Timeout: GistTimeout,
		},
		cache: make(map[string]string),
	}
}

// FetchNodes polls the GitHub Gist over standard HTTPS and updates the node map.
func (d *DiscoveryResolver) FetchNodes(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.gistURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gist fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gist fetch returned status %d", resp.StatusCode)
	}

	var nodes []GistNode
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		return nil, fmt.Errorf("failed to parse gist JSON: %w", err)
	}

	freshMap := make(map[string]string)
	for _, n := range nodes {
		if n.Username != "" && n.IP != "" {
			freshMap[n.Username] = n.IP
		}
	}

	d.mu.Lock()
	d.cache = freshMap
	d.lastFetch = time.Now()
	d.mu.Unlock()

	return freshMap, nil
}
```

#### 4.3 Secure gRPC TLS Dialing with Name-Based Verification
Update `internal/client/client.go` and `internal/client/msclient.go`:
```go
// buildTLSConfig returns a TLS credentials configuration that targets expectedServerName
// while verifying exclusively against the hardcoded Root CA.
func buildTLSConfig(expectedServerName string) (credentials.TransportCredentials, error) {
	cp, err := NewDVFSUniversalCertPool()
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{
		ServerName: expectedServerName, // e.g., "dvfs1", "dvfs2"
		RootCAs:    cp,
		MinVersion: tls.VersionTLS13,   // Enforce modern TLS 1.3
	}
	return credentials.NewTLS(tlsCfg), nil
}
```

When dialing Metaserver (`dvfs1`):
```go
metaIP := resolver.Lookup("dvfs1") // e.g. "10.0.171.38"
creds, _ := buildTLSConfig("dvfs1")
conn, err := grpc.NewClient(fmt.Sprintf("%s:50051", metaIP), grpc.WithTransportCredentials(creds))
```

When Metaserver responds with navigation address to Fileserver `dvfs3` (`10.0.172.45:50052`):
```go
// Client maps IP or node ID to expected hostname "dvfs3"
creds, _ := buildTLSConfig("dvfs3")
conn, err := grpc.NewClient(fsAddress, grpc.WithTransportCredentials(creds))
```

---

## 4. Implementation Specifics, Edge Cases & Threat Analysis

### 4.1 The Dynamic IP vs Certificate SAN Conundrum
*Problem:* In cloud or DHCP environments, Raspberry Pi nodes receive arbitrary IP addresses from the network gateway. If server certificates include static IP addresses in SANs, any DHCP renewal will break TLS connections with certificate mismatch errors.
*Solution:* 
- Server certificates contain **DNS SANs only** (`dvfs1`, `dvfs2`, ... `dvfs9`).
- The client dials the discovered **IP address**, but instructs Go's crypto/tls layer:
  `tls.Config.ServerName = "dvfsX"`.
- The TLS handshake performs standard SNI and validates that the leaf certificate's SAN covers `dvfsX`.
- The client verifies that the certificate was signed by the offline Root CA.
- This provides full cryptographic integrity without requiring server certificates to be reissued when IP addresses change.

### 4.2 Gist Delay & Hourly Staleness Window
*Analysis:* The GitHub Gist is updated hourly by `scripts/rp_115/update_gist.py`. If a node's IP changes midway through an hour, or if a node reboots with a new DHCP lease, the Gist will provide a stale IP for up to 60 minutes.
*Mitigations:*
1. **Client-Side Exponential Backoff & Cache Refresh:** If gRPC connection fails with `i/o timeout` or `connection refused`, the client triggers an immediate cache refresh from the Gist.
2. **Local Fallback Cache:** The client persists the last-known good Gist map to `~/.dvfs/machines_cache.json`. If GitHub is experiencing an outage or rate-limiting, the client falls back to cached entries.
3. **MDS Re-verification:** When the Metaserver directs a client to a fileserver via `Navigate`, it returns both the IP and the node identifier (`dvfsX`), allowing the client to set the correct SNI regardless of routing changes.

### 4.3 Man-in-the-Middle (MitM) Threat on Gist Polling
*Analysis:* What if an attacker compromises local DNS or conducts ARP spoofing to intercept the client's HTTP request to `gist.githubusercontent.com`?
- **Transport Security:** The Gist request uses HTTPS, authenticated via standard public Web PKI (DigiCert / GitHub CA).
- **Even if Gist is Compromised:** Suppose an adversary manages to tamper with the Gist JSON content and substitutes `10.0.171.38` (Metaserver) with an attacker-controlled IP `10.0.171.99`.
- **Cryptographic Barrier:** When the client dials `10.0.171.99:50051`, it expects a certificate for `dvfs1` signed by the **DVFS Offline Root CA**. The attacker does not possess the Root CA private key and cannot generate a valid certificate. The gRPC TLS handshake fails instantly with `x509: certificate signed by unknown authority`.
- **Verdict:** The architecture is cryptographically self-healing against IP redirection attacks.

### 4.4 Fix for the Client Callback Server TLS Mismatch
*Bug Identified:* `internal/client/callback_server.go` creates a plaintext gRPC listener, but `internal/fileserver/callback_server.go` dials it with TLS when `fs.useTLS = true`.

**Confirmed Decision (Option A):**
The Fileserver will dial the client callback server in plaintext (`grpc.WithInsecure()`).
- **Rationale:** The client callback channel is purely a lightweight, ephemeral notification signal. It only receives invalidation pulses containing an Inode FID and version number; absolutely no user files, directory payloads, or credentials are transmitted over this channel.
- **Operational Advantage:** This resolves the asymmetric TLS handshake deadlock immediately without requiring client binaries to be provisioned with client certificates and private keys.
- **Scope:** All data-carrying and security-sensitive channels (Client <-> Fileserver, Client <-> Metaserver, and Fileserver <-> Metaserver) strictly enforce Root-CA TLS.


---

## 5. Critical Audit of Admin Console Password Authentication

A comprehensive security review of `internal/admin/auth.go`, `internal/admin/handlers.go`, and the admin frontend was performed:

```
[CURRENT ADMIN AUTH PIPELINE]
User Password -> SHA-256 (Single pass, unsalted) -> ConstantTimeCompare -> 32-Byte Token -> Lax Cookie
```

### 5.1 High-Severity Security Vulnerabilities

| # | Vulnerability | Severity | Impact | Code Reference |
|---|---|---|---|---|
| 1 | **Unsalted SHA-256 Password Hash** | **HIGH** | Single-pass SHA-256 without salt is vulnerable to precomputed rainbow tables and high-speed GPU hash cracking (billions of guesses/sec). | `internal/admin/auth.go:104-107` |
| 2 | **No Rate Limiting on `/api/auth/login`** | **HIGH** | An attacker on the local network can mount an automated brute-force dictionary attack with unlimited attempts per second. | `internal/admin/handlers.go:928-972` |
| 3 | **Plaintext HTTP on Admin Console (Port 8080)** | **HIGH** | `http.ListenAndServe(":8080")` transmits session cookies and commands in plaintext. Anyone on the local WiFi/LAN can sniff the admin session token. | `internal/admin/handlers.go:1083` |
| 4 | **Missing `Secure` Cookie Flag** | **MEDIUM** | `http.Cookie` sets `HttpOnly: true` and `SameSite: Lax`, but omits `Secure: true`. Browsers will transmit the session cookie over insecure HTTP. | `internal/admin/handlers.go:959-966` |
| 5 | **Volatile In-Memory Sessions** | **LOW/OPS** | Active session tokens are stored in a Go map `sessions map[string]time.Time`. Restarting the admin daemon terminates all active sessions. | `internal/admin/auth.go:55` |
| 6 | **Lack of CSRF Tokens on Mutation Endpoints** | **MEDIUM** | State-changing POST endpoints (`/api/actions/execute`, `/api/users/quota`) rely solely on cookie presence with `SameSite=Lax`, which does not protect against all cross-site trigger contexts. | `internal/admin/handlers.go:1057-1066` |

### 5.2 Strengths of Current Implementation
- **Timing Attack Resistance:** Uses `crypto/subtle.ConstantTimeCompare` to evaluate password hashes, preventing timing side-channel attacks.
- **Cryptographically Secure Session Tokens:** Generates 32 random bytes from `crypto/rand`, producing 256 bits of entropy.
- **Defensive Session Lifecycle:** Sets a sensible 12-hour expiration window (`sessionTTL = 12 * time.Hour`) with proactive pruning of expired tokens.
- **Strict Hash-Only Storage:** Plaintext passwords are never accepted, stored, or compared.

### 5.3 Hardening Recommendations for Admin Auth
1. **Upgrade Hash Algorithm to Argon2id / bcrypt:**
   Replace single-pass SHA-256 with Argon2id (`golang.org/x/crypto/argon2`) or bcrypt (`golang.org/x/crypto/bcrypt`) with work factor >= 12.
2. **Implement IP-Based Rate Limiting:**
   Add an in-memory token-bucket or sliding-window rate limiter on `/api/auth/login` allowing max 5 failed attempts per IP per minute before a 15-minute lockout.
3. **Bind Admin Console Directly to TLS (Confirmed):**
   Directly bind the Admin Console on port 8080 (or 8443) using `http.ListenAndServeTLS`. Configure `cmd/admin/main.go` and `internal/admin/server.go` with `-tls=true`, `-tls_cert`, and `-tls_key` flags using the node certificate (`server.crt` and `server.key`). Enforce `Secure: true` on the `dvfs_admin_token` session cookie. This guarantees that administrator login credentials, session tokens, real-time WebSocket telemetry, and action executions are fully encrypted in transit.

---

## 6. Implementation Checklist & Verification Plan

### 6.1 Code Changes Required
- [ ] `scripts/gen-certs/gen_root_ca.go` — Air-gapped 10+ year Root CA generator.
- [ ] `scripts/gen-certs/gen_node_certs.go` — Multi-node certificate generator with DNS SANs (`dvfs1`-`dvfs9`).
- [ ] `internal/client/ca.go` — Hardcoded Root CA public key constant and isolated `CertPool`.
- [ ] `internal/client/discovery.go` — Dynamic GitHub Gist polling, node IP caching, and resolver.
- [ ] `internal/client/client.go` & `msclient.go` — Update gRPC dial options to use isolated `CertPool` and SNI.
- [ ] `internal/fileserver/callback_server.go` — Update `sendInvalidate` to dial client callbacks with `grpc.WithInsecure()` (Option A) to resolve callback TLS deadlock.
- [ ] `cmd/admin/main.go` & `internal/admin/handlers.go` — Bind Admin Console port to TLS with `http.ListenAndServeTLS` and enable `Secure: true` on cookies.
- [ ] `scripts/start-metaserver.sh` & `scripts/start-fileserver.sh` — Enable `-tls=true` in startup scripts.
- [ ] `internal/admin/auth.go` — Add rate-limiting middleware to `/api/auth/login`.

### 6.2 Test & Verification Procedure

#### Step 1: Root CA & Node Cert Verification
```bash
go run scripts/gen-certs/gen_root_ca.go
go run scripts/gen-certs/gen_node_certs.go
openssl x509 -in deploy_certs/ca.crt -text -noout | grep -E "(Issuer|Subject|Not After|CA:TRUE)"
openssl verify -CAfile deploy_certs/ca.crt deploy_certs/dvfs1/server.crt
```
*Expected Result:* `deploy_certs/dvfs1/server.crt: OK`. Expiration >= 10 years for CA.

#### Step 2: Client Hardcoded CA Isolation Test
Create a unit test `internal/client/tls_isolation_test.go`:
- Verify `NewDVFSUniversalCertPool()` loads the embedded PEM.
- Attempt to verify a certificate issued by Let's Encrypt or Google Trust Services against this pool; assert verification **FAILS**.
- Verify a certificate issued by the DVFS Root CA succeeds.

#### Step 3: Dynamic Gist Discovery Test
Create `internal/client/discovery_test.go`:
- Mock HTTP server serving `machines.json`.
- Verify `DiscoveryResolver` parses IPs and maps `dvfs1` -> `10.0.171.38`.
- Test fallback behavior when HTTP request times out.

#### Step 4: End-to-End TLS Handshake & Data Transfer
```bash
# Start Metaserver with TLS
./bin/metaserver -port=50051 -tls=true -tls_cert=certs/dvfs1.crt -tls_key=certs/dvfs1.key

# Start Fileserver with TLS
./bin/fileserver -id=fs1 -port=50052 -meta_addr=127.0.0.1:50051 -own_ip=127.0.0.1 -tls=true -tls_cert=certs/dvfs2.crt -tls_key=certs/dvfs2.key -ca_cert=certs/ca.crt

# Run Client with TLS enabled against discovered node
./bin/client -username=alice -tls=true
```
*Verification:* Client authenticates, navigates, uploads a 10MB test file, and downloads it without TLS errors. Wire inspection via `tcpdump` confirms binary gRPC frames are wrapped in TLS 1.3 records.

---

## 7. Confirmed Architectural Decisions & Final Alignment

1. **Client Callback Channel Policy (Option A Confirmed):**
   - Fileservers dial client callback listeners over plaintext (`grpc.WithInsecure()`).
   - The callback channel acts solely as a lightweight, ephemeral notification pulse (carrying only Inode FID and version numbers; no payloads or tokens).
   - This prevents asymmetric TLS handshake deadlocks while avoiding the complexity of provisioning client certificates/private keys for every client binary.
   - All data-carrying channels (Client <-> Fileserver, Client <-> Metaserver, Fileserver <-> Metaserver) strictly enforce Root-CA TLS.

2. **Admin Console TLS Integration (Direct Port Binding Confirmed):**
   - The Admin Console binary directly binds its listening port (8080 or dedicated HTTPS port) to TLS via `http.ListenAndServeTLS`.
   - Utilizes the node's server certificate (`server.crt` / `server.key`).
   - Enforces `Secure: true` on the `dvfs_admin_token` session cookie.

3. **Server SAN Naming Scheme:**
   - Server certificates will be minted with canonical DNS SANs `dvfs1` through `dvfs9`, along with `dvfsX.local` and `localhost`.
   - Clients resolve the target IP from the Gist and dial the IP directly while specifying `ServerName: "dvfsX"`, making TLS verification resilient against dynamic DHCP IP reassignments.

