# Example: Testing the distributed VFS

This example demonstrates basic operations with the distributed file system.

## Start the System

Terminal 1 - File Server:
```bash
go run cmd/fileserver/main.go fs1 50051 ./fileserver_data
```

Terminal 2 - Client (Alice):
```bash
go run cmd/client/main.go alice
```

## Example Session

```
VFS Client started (user: alice, client_id: client1)
Type 'help' for available commands

> help
Available commands:
  create <path> <name> [dir]          - Create a file or directory
  write <fid> <data>                  - Write data to a file
  read <fid>                          - Read and display file contents
  ls <fid>                            - List directory contents
  lookup <parent_fid> <name>          - Lookup a file in directory
  exit/quit                           - Exit the client

FID format: serverid_inodeid_generation (e.g., fs1_0_1 for root)

> create "" document.txt
Created: /document.txt (FID: fs1_1_1)

> write fs1_1_1 This is my first file in the distributed VFS!
Opened file FID fs1_1_1, fd=1, version=1
Wrote 47 bytes (fd=1), new version=2
Closed fd=1

> read fs1_1_1
Opened file FID fs1_1_1, fd=1, version=2
Cached full file (47 bytes) for fd=1
Read 47 bytes from cache (fd=1)
Content:
This is my first file in the distributed VFS!
Closed fd=1

> ls fs1_0_1
Directory contents:
  [FILE] document.txt

> create "" mydir dir
Created: /mydir (FID: fs1_2_1)

> ls fs1_0_1
Directory contents:
  [FILE] document.txt
  [DIR ] mydir

> lookup fs1_0_1 document.txt
Found: fs1_1_1

> exit
Goodbye!
```

## Testing Cache Invalidation

Terminal 1 - Server:
```bash
go run cmd/fileserver/main.go fs1 50051 ./fileserver_data
```

Terminal 2 - Alice:
```bash
go run cmd/client/main.go alice client1
```

Terminal 3 - Bob:
```bash
go run cmd/client/main.go bob client2
```

### Alice's Session:
```
> create "" shared.txt
Created: /shared.txt (FID: fs1_1_1)

> write fs1_1_1 Version 1 of the document
Wrote 25 bytes (fd=1), new version=2
```

### Bob's Session:
```
> read fs1_1_1
Opened file FID fs1_1_1, fd=1, version=2
Cached full file (25 bytes) for fd=1
Content:
Version 1 of the document
```

### Alice Writes Again:
```
> write fs1_1_1 Version 2 - Alice updated it!
Wrote 30 bytes (fd=1), new version=3
```

### Bob's Terminal Shows:
```
Cache invalidated for FID fs1_1_1, new version: 3
```

### Bob Reads Again:
```
> read fs1_1_1
Opened file FID fs1_1_1, fd=1, version=3
Cached full file (30 bytes) for fd=1
Content:
Version 2 - Alice updated it!
```

## Key Observations

1. **File IDs are stable** - FID `fs1_1_1` stays the same across operations
2. **Versions increment** - Each write increases the version number
3. **Caching works** - Reads after the first are served from cache
4. **Callbacks fire** - Bob's cache is automatically invalidated when Alice writes
5. **Consistency maintained** - Bob always sees the latest version after invalidation
