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
