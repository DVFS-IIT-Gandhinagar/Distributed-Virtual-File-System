# Authentication & Security Access Controls

This document details the administrative authentication and data redaction subsystem in DVFS, divided into its security design (**In Essence**) and its backend implementation (**Implementation Quirks**).

---

## 1. In Essence: Dual-Mode Public & Admin Monitoring

A distributed filesystem dashboard often needs to be viewed by both casual observers (e.g. students or lab members checking cluster uptime) and privileged administrators (who manage quotas and execute cluster commands).

DVFS implements a **dual-mode presentation model**:

```
[Unauthenticated Visitor] -------------> [Public Dashboard View]
                                          - Node uptimes & hardware temps
                                          - Cluster throughput & IOPS
                                          - Redacted user identities & quotas

[Administrator + Password] ------------> [Admin Console Mode]
                                          - User listing & dynamic quota editing
                                          - Remote SSH commands (reboot, git, apt)
                                          - Live journalctl log streaming
                                          - Alert resolution
```

### Core Security Invariants
1. **Zero Plaintext Passwords**: Passwords are never stored in plaintext on disk or in source code. Authentication verifies submitted passwords strictly against a precomputed SHA-256 hash.
2. **Timing Attack Immunity**: Hash comparisons use constant-time verification to prevent cryptographic timing side-channel attacks.
3. **Data Redaction by Default**: All API endpoints scrub user identities, active usernames, and quota details when accessed without valid administrative session credentials.
4. **Cookie-Based WebSocket Protection**: Real-time operational streams authenticate via browser session cookies by default, preventing credentials from appearing in web server access logs (with fallback query parameter support).

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The SHA-256 Authentication Engine (`internal/admin/auth.go`)
The Admin Console uses standard library cryptography for credential management:

```go
type AuthManager struct {
    passwordHash string
    sessions     map[string]time.Time // Token -> Expiration
    mu           sync.RWMutex
}
```

- **Environment Configuration**: Configured in `.env` via `ADMIN_PASSWORD_HASH`:
  - **Linux / macOS / WSL**:
    ```bash
    echo -n "YourSecretPassword" | sha256sum | awk '{print "ADMIN_PASSWORD_HASH="$1}' > .env
    chmod 600 .env
    ```
  - **Windows PowerShell**:
    ```powershell
    $hash = [System.BitConverter]::ToString([System.Security.Cryptography.SHA256]::Create().ComputeHash([System.Text.Encoding]::UTF8.GetBytes("YourSecretPassword"))).Replace("-","").ToLower()
    "ADMIN_PASSWORD_HASH=$hash" | Out-File -Encoding ascii .env
    ```
- **Environment & Systemd Configuration**: Administrative credentials must be supplied via `ADMIN_PASSWORD_HASH` (or plaintext `ADMIN_PASSWORD` for development) within a `.env` file in the working directory, or exported directly into the system environment (e.g. in `dvfs-admin.service` via `Environment="ADMIN_PASSWORD_HASH=..."`).
- **Verification**: On `POST /api/auth/login`, the submitted password string is hashed via `sha256.Sum256()` and compared against `passwordHash` using `crypto/subtle.ConstantTimeCompare()`.
- **Session Tokens**: On successful authentication, the server generates a 32-byte cryptographically secure random token (`crypto/rand`) hex-encoded to a 64-character string.
- **Session TTL**: Tokens remain valid for 12 hours.
- **Cookie Delivery**: Tokens are written to the browser via an `HttpOnly`, `Path=/`, `SameSite=Lax` cookie named `dvfs_admin_token`.

### 2.2 Endpoint Protection & Middleware
Privileged endpoints are protected by `RequireAuth` middleware:
- **Token Extraction**: Checks both the `Authorization: Bearer <token>` header and the `dvfs_admin_token` cookie.
- **Enforced Routes (401 Unauthorized if unauthenticated)**:
  - `GET /api/users` (User quota directory)
  - `PUT /api/users/{username}/quota` (Quota adjustments)
  - `POST /api/actions/*` (Cluster command execution)
  - `POST /api/alerts/resolve` and `POST /api/alerts/resolve-all`
  - `GET /api/logs/tail` (Live journalctl log streaming)
  - `GET /ws/actions` (WebSocket command stream)

### 2.3 Automatic Public Data Redaction (`internal/admin/handlers.go`)
When unauthenticated visitors access telemetry endpoints, the backend automatically sanitizes the response:
- **`GET /api/cluster` and `GET /api/cluster/summary`**:
  - `users` map is set to `nil`.
  - `total_users` is set to `0`.
  - Per-node `per_user_storage`, `per_user_quota`, and `active_users` dictionaries are stripped.
  - Hardware health, storage capacities, cluster throughput (MiB/s), and IOPS remain visible.
- **`GET /api/alerts`**:
  - Suppresses all `quota_exceeded` alerts.
  - Strips user identity strings from alert messages.

### 2.4 Secure WebSocket Authentication & Token Extraction
The interactive command execution terminal (`/ws/actions`) and metrics stream (`/ws/metrics`) connect via WebSockets. 
- **Primary Method (HTTP-Only Cookie)**: Standard browser WebSocket clients authenticate seamlessly via the `dvfs_admin_token` cookie sent automatically during the HTTP upgrade handshake, avoiding exposure of session tokens in server URL access logs.
- **Fallback Support (Query Parameter & Bearer Header)**: For non-browser clients, automated scripts, or environments unable to manipulate WebSocket upgrade cookies, `ExtractToken` in `internal/admin/auth.go` also supports `Authorization: Bearer <token>` and `?token=<token>` query parameter extraction.
