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

- `certs\\ca.crt`
- `certs\\ca.key` (MetaServer only)
- `certs\\metaserver.crt`
- `certs\\metaserver.key`
- `certs\\fileserver.crt`
- `certs\\fileserver.key`

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
