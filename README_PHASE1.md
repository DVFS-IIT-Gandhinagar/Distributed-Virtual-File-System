# Distributed Virtual File System - Phase 1 Implementation

This is the **Phase 1** implementation of the Distributed Virtual File System (DVFS) focusing on the AFS-style architecture with client-side caching and server callbacks.

## Features Implemented

### File Server
- **FID-based identification** - Unique global file identifiers
- **In-memory inode map** - Simple map-based storage (no database yet)
- **ACL enforcement** - Permission checking for read/write/lookup operations
- **OS filesystem backend** - Uses host OS for actual file storage
- **gRPC interface** - All file operations via gRPC
- **Client callbacks** - Notifies clients when cached files are modified

### Client
- **Mount table** - Hardcoded mount points to file servers
- **AFS-style caching** - Full file caching on open
- **Cache invalidation** - Receives callbacks from server when files change
- **File operations** - Create, open, read, write, close, delete
- **Interactive shell** - Command-line interface for testing

### Operations Supported
- `CreateFile` - Create files and directories
- `OpenFile` - Open files with version tracking
- `ReadFile` - Read file data (cached locally)
- `WriteFile` - Write file data (invalidates other caches)
- `CloseFile` - Close file handles
- `DeleteFile` - Remove files
- `ListDir` - List directory contents
- `Lookup` - Find files by name
- `GetAttr` - Get file metadata

## Project Structure

```
.
├── proto/
│   ├── fileserver/
│   │   └── fileserver.proto      # File server gRPC definitions
│   └── callback/
│       └── callback.proto         # Client callback definitions
├── fileserver/
│   └── fileserver.go              # File server implementation
├── client/
│   └── client.go                  # Client implementation
├── cmd/
│   ├── fileserver/
│   │   └── main.go                # File server executable
│   └── client/
│       └── main.go                # Client executable
├── generate_proto.sh              # Proto generation script
├── demo.sh                        # Demo script
├── go.mod                         # Go module definition
└── README_PHASE1.md              # This file
```

## Setup

### Prerequisites
- Go 1.21 or later
- Protocol Buffers compiler (`protoc`)
- gRPC Go plugins

Install protoc plugins:
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### Installation

1. **Install dependencies:**
```bash
go mod download
```

2. **Generate protobuf code:**
```bash
chmod +x generate_proto.sh
./generate_proto.sh
```

## Running the System

### Option 1: Quick Demo

Run the demo script:
```bash
chmod +x demo.sh
./demo.sh
```

Then in another terminal:
```bash
go run cmd/client/main.go alice
```

### Option 2: Manual Setup

**Terminal 1 - Start File Server:**
```bash
go run cmd/fileserver/main.go fs1 50051 ./fileserver_data
```

**Terminal 2 - Start Client:**
```bash
go run cmd/client/main.go alice
```

**Terminal 3 - Start Another Client (for testing cache invalidation):**
```bash
go run cmd/client/main.go bob client2
```

## Usage Examples

### Interactive Client Commands

```bash
# Create a file
> create "" myfile.txt

# Write to the file (use the FID from create output)
> write fs1_1_1 "Hello, World!"

# Read the file
> read fs1_1_1

# List directory (root FID is fs1_0_1)
> ls fs1_0_1

# Create a directory
> create "" mydir dir

# Lookup a file in a directory
> lookup fs1_0_1 myfile.txt

# Exit
> exit
```

### Testing Cache Invalidation

1. Start two clients (alice and bob)
2. In alice's client:
   ```
   > create "" shared.txt
   > write fs1_1_1 "Initial content"
   ```
3. In bob's client:
   ```
   > read fs1_1_1
   ```
4. In alice's client:
   ```
   > write fs1_1_1 "Updated content"
   ```
5. Bob's cache is automatically invalidated!
6. In bob's client, read again to fetch new content:
   ```
   > read fs1_1_1
   ```

## Architecture Highlights

### AFS-Style Caching
- **On open**: Client fetches entire file into local cache
- **On read**: Data served from local cache (fast!)
- **On write**: Write goes to server, server invalidates other clients' caches
- **On close**: File handle released

### Callback System
- Each client runs a gRPC callback server
- File server tracks which clients have which files open
- When a client writes to a file, server sends invalidation callbacks to all other clients with that file cached
- Clients mark cache entries as invalid and refetch on next access

### FID-Based Identity
```
FID = <server_id, inode_id, generation_number>
```
- **server_id**: Which file server owns the file
- **inode_id**: Unique ID on that server
- **generation_number**: Increments when file is deleted and recreated

### Simplified Assumptions (Phase 1)
1. **Hardcoded mount table** - Client knows server address at startup
2. **Simple map for inodes** - No persistent database yet
3. **Single file server** - No replication or distribution
4. **No metadata server** - No shared file indexing yet
5. **Basic ACLs** - Simple owner-based permissions

## Design Decisions

### Why In-Memory Map?
For Phase 1, we use a simple `map[FIDKey]*Inode` instead of a database to keep things simple and focus on the core AFS caching semantics. Phase 2 will add proper persistent storage.

### Why Full-File Caching?
AFS caches entire files on open for simplicity and performance. This works well for small-to-medium files and reduces server load for repeated reads.

### Why gRPC?
- Type-safe RPC with protobuf
- Excellent performance
- Built-in streaming support (for future use)
- Easy to implement callbacks

### Why Callbacks Instead of Polling?
Callbacks provide immediate cache invalidation, maintaining consistency without constant polling overhead.

## Testing

### Basic Functionality Test
```bash
# Terminal 1: Start server
go run cmd/fileserver/main.go

# Terminal 2: Run client
go run cmd/client/main.go alice

# In client:
> create "" test.txt
> write fs1_1_1 "test data"
> read fs1_1_1
> ls fs1_0_1
```

### Cache Coherence Test
```bash
# Terminal 1: Server
go run cmd/fileserver/main.go

# Terminal 2: Alice
go run cmd/client/main.go alice

# Terminal 3: Bob
go run cmd/client/main.go bob client2

# Alice creates and writes
> create "" shared.txt
> write fs1_1_1 "version 1"

# Bob reads (fetches from server)
> read fs1_1_1

# Alice writes again (Bob's cache invalidated!)
> write fs1_1_1 "version 2"

# Bob reads again (fetches new version)
> read fs1_1_1
```

## Known Limitations (Phase 1)

1. **No persistence** - Server state lost on restart
2. **No shared namespace** - No `shared/` directory yet
3. **No metadata server** - No central index for shared files
4. **Single server** - No fault tolerance or load balancing
5. **Simple path handling** - No full path resolution
6. **No rename/move** - Not implemented yet
7. **No recursive delete** - No cascade deletion
8. **Limited error handling** - Basic error messages

## Next Steps (Phase 2)

- [ ] Add persistent database for file server
- [ ] Implement metadata server for shared file indexing
- [ ] Add `mydrive/` and `shared/` namespaces
- [ ] Implement full path resolution
- [ ] Add rename and move operations
- [ ] Implement recursive directory operations
- [ ] Add more sophisticated ACLs
- [ ] Better error handling and recovery

## Performance Characteristics

### Strengths
- **Fast repeated reads** - Cached locally, no server round-trip
- **Scalable reads** - Server load reduced by caching
- **Low latency** - Local cache access is fast

### Tradeoffs
- **Large files** - Full-file caching can be memory intensive
- **Write performance** - All writes go to server
- **Cache overhead** - Callback infrastructure for invalidation

## Code Organization

- **proto/** - Protocol buffer definitions
- **fileserver/** - Server-side logic
- **client/** - Client-side logic
- **cmd/** - Executable entry points

Each component is modular and can be extended independently.

## Contributing

This is Phase 1 - a foundation for building the complete distributed VFS. Feel free to explore, experiment, and extend!

## License

MIT License - See main README.md
