# Commands To Run the Whole Setup

### For Fileserver (from project root)

```
go run .\cmd\fileserver\main.go --meta_addr <full ip + port of mds> --own_ip=<this fileservers ip to send to mds>
```

Other flags and defaults:

- port = 50052
- id = fs1
- data = fileserver_data
- tls = false

### For Metadata Server (from project root)

```
go run .\cmd\metaserver\main.go
```

Other flags and defaults:

- port = 50051
- tls = false

### For Client (from root)

```
go run .\cmd\client\main.go --username <username> --ip_addr <mds/fs ip address>
```

Other flags and defaults

- port = 50051
- tls = false
- meta = true (whether to go to mds or not)

---

## TLS Mode

### 1. Prepare certificate file paths

Pick a directory for cert stuff and ensure all nodes can access the CA cert file path they are configured with.

Default project paths used in commands below:

- `internal\\certs\\ca.crt`
- `internal\\certs\\ca.key` (MetaServer only)
- `internal\\certs\\metaserver.crt`
- `internal\\certs\\metaserver.key`
- `internal\\certs\\fileserver.crt`
- `internal\\certs\\fileserver.key`

How to generate these cert files:

Option A (recommended, from project root):

```
make certs MDS_IP=<MDS_LAN_IP>
```

Example:

```
make certs MDS_IP=192.168.1.10
```

Option B (direct script):

```
go run scripts/gen-certs/main.go <MDS_LAN_IP>
```

This writes `ca.crt`, `ca.key`, `server.crt`, `server.key` under `internal\\certs`; the Makefile target also maps them to `metaserver.*` and `fileserver.*` filenames expected by the TLS run commands.

### 2. Start MetaServer with TLS

```
go run .\cmd\metaserver\main.go --tls=true --public_ip=<MDS_LAN_IP> --tls_ca_file .\internal\certs\ca.crt --tls_ca_key_file .\internal\certs\ca.key --tls_cert_file .\internal\certs\metaserver.crt --tls_key_file .\internal\certs\metaserver.key
```

Notes:

- `--public_ip` is required in TLS mode.
- Keep the same CA files across restarts to preserve trust.

### 3. Start FileServer with TLS

```
go run .\cmd\fileserver\main.go --meta_addr <MDS_LAN_IP>:50051 --own_ip=<FS_LAN_IP> --tls=true --tls_ca_file .\internal\certs\ca.crt --tls_cert_file .\internal\certs\fileserver.crt --tls_key_file .\internal\certs\fileserver.key
```

go run .\cmd\fileserver\main.go --meta_addr 10.7.63.138:50051 --own_ip=10.7.63.138 --tls=true --tls_ca_file .\internal\certs\ca.crt --tls_cert_file .\internal\certs\fileserver.crt --tls_key_file .\internal\certs\fileserver.key

Notes:

- FileServer auto-requests/renews its serving certificate from MetaServer in TLS mode.
- Keep `--own_ip` as the reachable LAN IP.

### 4. Start Client with TLS

```
go run .\cmd\client\main.go --username <username> --ip_addr <MDS_LAN_IP> --port 50051 --meta=true --tls=true --tls_ca_file .\internal\certs\ca.crt
```

### 5. TLS aliases

All binaries accept these equivalent TLS flags:

- `--tls=true|false`
- `--has_tls=true|false`
- `--has-tls=true|false`
- `--hasl_tls=true|false`
- `--hasl-tls=true|false`

### 6. Dual-root transition (trust rotation):

- Add `--tls_ca_bundle_file <path>` on Client and FileServer to trust additional CA roots during CA rollover.
- The bundle file can contain one or more PEM CA certs.

Example client command with bundle:

```
go run .\cmd\client\main.go --username <username> --ip_addr <MDS_LAN_IP> --port 50051 --meta=true --tls=true --tls_ca_file .\internal\certs\ca.crt --tls_ca_bundle_file .\internal\certs\ca_bundle.crt
```

Example fileserver command with bundle:

```
go run .\cmd\fileserver\main.go --meta_addr <MDS_LAN_IP>:50051 --own_ip=<FS_LAN_IP> --tls=true --tls_ca_file .\internal\certs\ca.crt --tls_ca_bundle_file .\internal\certs\ca_bundle.crt --tls_cert_file .\internal\certs\fileserver.crt --tls_key_file .\internal\certs\fileserver.key
```

Revoked fileserver identity enforcement:

- Add `--revoked_fs_fingerprints_file <path>` on MetaServer to block specific FileServer cert fingerprints.
- File format: SHA-256 fingerprints in hex, one per line (supports `#` comments and colon-separated hex).

Example metaserver command with revocation file:

```
go run .\cmd\metaserver\main.go --tls=true --public_ip=<MDS_LAN_IP> --tls_ca_file .\internal\certs\ca.crt --tls_ca_key_file .\internal\certs\ca.key --tls_cert_file .\internal\certs\metaserver.crt --tls_key_file .\internal\certs\metaserver.key --revoked_fs_fingerprints_file .\internal\certs\revoked_fileserver_fingerprints.txt
```
