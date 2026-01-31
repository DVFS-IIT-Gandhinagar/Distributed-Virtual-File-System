# Distributed Virtual File System

## Phase 1: AFS-Style Implementation (✅ Complete)

This is a distributed virtual file system inspired by AFS (Andrew File System), implemented in Go with gRPC. The current Phase 1 implementation includes:

- **File Server** with FID-based identification, ACL enforcement, and OS filesystem backend
- **Client** with AFS-style whole-file caching and cache invalidation callbacks
- **gRPC-based communication** for all file operations
- **Simple in-memory storage** (map-based, no database yet)

## 🚀 Quick Start

### Prerequisites
- Go 1.21 or later
- Protocol Buffers compiler (`protoc`)
- gRPC Go plugins

### Installation

1. **Generate protobuf code:**
```bash
./generate_proto.sh
```

2. **Build the system:**
```bash
go build -o bin/fileserver cmd/fileserver/main.go
go build -o bin/client cmd/client/main.go
```

### Running the System

**Terminal 1 - Start File Server:**
```bash
./bin/fileserver fs1 50051 ./fileserver_data
```

**Terminal 2 - Start Client:**
```bash
./bin/client alice
```

Or use the test script:
```bash
./test_client.sh
```

## 💡 Usage Examples

Once the client starts, try these commands:

```bash
# Create a file
> create "" myfile.txt
Created: /myfile.txt (FID: fs1_1_1)

# Write to the file
> write fs1_1_1 Hello, distributed world!
Wrote 24 bytes

# Read the file
> read fs1_1_1
Content:
Hello, distributed world!

# List directory
> ls fs1_0_1
Directory contents:
  [FILE] myfile.txt

# Create a directory
> create "" docs dir
Created: /docs (FID: fs1_2_1)

# Exit
> exit
```

## 🏗️ Architecture

### Key Components

1. **File Server**
   - Stores files on OS filesystem
   - Maintains FID → Inode mapping in memory
   - Enforces ACL permissions
   - Tracks which clients have files open
   - Sends cache invalidation callbacks

2. **Client**
   - Hardcoded mount table (Phase 1)
   - Whole-file caching (AFS-style)
   - Receives invalidation callbacks
   - Provides file operations: create, open, read, write, close

3. **FID (File Identifier)**
   ```
   FID = <server_id, inode_id, generation_number>
   ```
   - Globally unique file identifier
   - Stable across renames

### Cache Coherence

**AFS-Style Callbacks:**
1. Client opens file → fetches entire file to cache
2. Client reads → served from local cache (fast!)
3. Client writes → server increments version
4. Server sends invalidation to other clients
5. Other clients mark cache invalid
6. Next read fetches fresh data

## 📁 Project Structure

```
.
├── proto/                      # Protocol buffer definitions
│   ├── fileserver/
│   │   └── fileserver.proto
│   └── callback/
│       └── callback.proto
├── fileserver/                 # File server implementation
│   └── fileserver.go
├── client/                     # Client implementation
│   └── client.go
├── cmd/                        # Executables
│   ├── fileserver/
│   │   └── main.go
│   └── client/
│       └── main.go
├── bin/                        # Built binaries
│   ├── fileserver
│   └── client
├── README.md                   # This file
├── README_PHASE1.md           # Detailed Phase 1 docs
├── EXAMPLES.md                # Usage examples
└── generate_proto.sh          # Proto generation script
```

## 🧪 Testing Cache Invalidation

**Terminal 1: Server**
```bash
./bin/fileserver fs1 50051 ./fileserver_data
```

**Terminal 2: Alice (client1)**
```bash
./bin/client alice client1
> create "" shared.txt
> write fs1_1_1 Version 1
```

**Terminal 3: Bob (client2)**
```bash
./bin/client bob client2
> read fs1_1_1
Content:
Version 1
```

**Back to Alice's terminal:**
```bash
> write fs1_1_1 Version 2 - Alice updated!
```

**Bob's terminal will show:**
```
Cache invalidated for FID fs1_1_1, new version: 3
```

**Bob reads again:**
```bash
> read fs1_1_1
Content:
Version 2 - Alice updated!
```

## 📊 Features Implemented

✅ File operations: create, open, read, write, close, delete  
✅ Directory operations: list, lookup  
✅ FID-based identification  
✅ ACL-based permissions  
✅ AFS-style whole-file caching  
✅ Cache invalidation callbacks  
✅ Multiple concurrent clients  
✅ gRPC communication  

## 🎯 Phase 1 Design Decisions

- **In-memory map** instead of database (simplicity)
- **Whole-file caching** (AFS semantic, good for small files)
- **Hardcoded mount table** (no dynamic discovery yet)
- **Single file server** (no replication yet)
- **No metadata server** (no shared namespace yet)

## 🔜 Future Phases

**Phase 2 - Full DVFS:**
- [ ] Persistent database (replace in-memory map)
- [ ] Metadata server for shared file indexing
- [ ] `mydrive/` and `shared/` namespaces
- [ ] Full path resolution
- [ ] Rename and move operations
- [ ] Recursive directory operations

## 📖 Documentation

- [README_PHASE1.md](README_PHASE1.md) - Detailed Phase 1 implementation guide
- [EXAMPLES.md](EXAMPLES.md) - More usage examples and test scenarios

## 🛠️ Development

**Run file server:**
```bash
go run cmd/fileserver/main.go [server_id] [port] [data_dir]
```

**Run client:**
```bash
go run cmd/client/main.go [username] [client_id]
```

**Regenerate proto files:**
```bash
./generate_proto.sh
```

## 📝 License

MIT License

---

**Built with Go, gRPC, and inspiration from AFS**

## 1. System Overview

This document describes the final design of a **distributed virtual file system (VFS)** inspired by AFS and implemented **in userspace on top of the host OS filesystem**.

The system consists of:
- **File Servers (FS)** – store file data and enforce access control
- **Metadata Server (MDS)** – stores indexing metadata for fast shared-file discovery
- **Clients** – expose a virtual namespace and interact with FS and MDS

### Design Goals
- Correct filesystem semantics (`open`, `read`, `write`, `rename`, `ls`)
- Each user sees exactly **two namespaces**:
  - **`mydrive`** (private)
  - **`shared`** (shared with the user)
- Fast common case (`ls shared`)
- Clear separation between authority and indexing

---

## 2. Terminology

### 2.1 Logical Inode
A **logical inode** is an internal representation of a file or directory.  
It is **not** an OS inode.

Each logical inode has:
- a stable identity
- metadata (name, ACL)
- a cached OS path

---

### 2.2 File Identifier (FID)

A **FID** uniquely identifies a file or directory globally.

```
FID = <file_server_id, inode_id, generation_number>
```

- `file_server_id` – which file server owns the file
- `inode_id` – unique logical inode number on that server
- `generation_number` – increments on delete + recreate

**FID is the only identity in the system.**

---

### 2.3 Access Control List (ACL)

An **ACL** defines which users may access a file or directory.

```
ACL {
  read   → users allowed to read file
  write  → users allowed to modify file
  lookup → users allowed to traverse directory
}
```

Rules:
- ACLs are stored and enforced only on file servers
- Metadata server never enforces permissions

---

### 2.4 Private vs Shared Files
- **Private file** – accessible only to the owner
- **Shared file** – ACL allows additional users

---

### 2.5 Shared Index
A **shared index** is denormalized metadata on the metadata server used only for:

```
ls shared
```

---

## 3. High-Level Architecture

![High-level architecture showing client mount-table, AFS-style caching, and MDS callbacks](./architecture.png)

---

## 4. File Server Design (Authoritative)

Each file server stores actual data on the OS filesystem and maintains one authoritative database.

### 4.1 Unified File Server Database

```
InodeDB {
  fid                PRIMARY KEY
  type               ENUM {file, directory}
  name               STRING
  os_path            STRING
  child_fids[]       ARRAY<FID>
  acl                ACL
}
```

**Invariants**
- FID is identity
- `os_path` is cached, derived state
- ACLs are enforced before OS access
- One inode maps to one OS path

---

### 4.2 Shared ACL Table (FS-local)

```
SharedACLTable {
  fid → users[]
}
```

Used to:
- track shared inodes
- notify metadata server
- rebuild shared state after crashes

---

## 5. Metadata Server Design

The metadata server stores no file data and no ACLs.

### 5.1 Metadata Server Database

```
SharedIndex {
  fid           PRIMARY KEY
  cached_name
  users[]
}
```

- `users[]` represents visibility only
- access is always validated by file servers

---

## 6. Client Namespace (User View)

Each user sees **exactly two directories**:

```
mydrive/
shared/
```

### Semantics
- **`mydrive`**
  - User’s private files
  - Backed by the user’s root directory on a file server
- **`shared`**
  - Virtual directory
  - Contains files and directories shared *with* the user
  - Backed by metadata server + FIDs

---

## 7. Private File Operations (`mydrive`)

### Create Private File

```
touch mydrive/fileA
```

File server:
1. Allocate new FID
2. Create OS file
3. Insert entry into `InodeDB`
4. ACL allows only owner

---

### Open Private File

```
open("mydrive/fileA")
```

File server:
1. Resolve path → FID
2. Check ACL
3. Use `os_path`
4. Call OS `open()`

---

## 8. Sharing Operations

### Share a File

```
setacl mydrive/fileA alice read
```

File server:
1. Update `InodeDB[fid].acl`
2. Update `SharedACLTable`
3. Notify metadata server

Metadata server updates `SharedIndex`.

---

## 9. Shared File Discovery (`shared`)

### List Shared Files

```
ls shared
```

Client queries metadata server:

```
SELECT * FROM SharedIndex
WHERE user ∈ users[]
```

Result:
- List of `(fid, cached_name, fs_id)`
- No file-server RPCs required

---

## 10. Open Shared File

```
open("shared/fileA")
```

Client:
- Resolves name → FID using metadata server

File server:
1. Validate generation number
2. Check ACL
3. Use cached `os_path`
4. Call OS `open()`

Client never sees real OS paths.

---

## 11. Rename and Move

### Rename File (Private or Shared)

```
mv mydrive/fileA mydrive/fileX
```

File server:
- updates `name`
- updates `os_path`
- FID unchanged
- metadata server notified if shared

---

### Move Directory (Rare, Expensive)

```
mv mydrive/project mydrive/archive/project
```

File server:
- updates directory `os_path`
- recursively updates children `os_path`
- notifies metadata server for shared FIDs only

---

## 12. Delete / Unshare with Cascade

```
rm -r mydrive/project
```

File server:
1. DFS using `child_fids`
2. Remove each FID from `SharedACLTable`
3. Notify metadata server
4. Delete OS files and DB entries

Guarantee:
- No ghost shared entries
- No stale visibility

---

## 13. Failure Handling

### Metadata Server Failure
- File servers continue enforcing ACLs
- `ls shared` may be stale
- Access remains correct

### File Server Failure
On restart:
1. Scan OS filesystem
2. Rebuild `InodeDB`
3. Rebuild `SharedACLTable`
4. Re-register shared FIDs

---

## 14. Design Invariants

1. FID is the only identity
2. ACLs enforced only on file servers
3. Metadata server is advisory
4. `os_path` must match OS filesystem
5. Rename/move updates `os_path`
6. Shared index staleness is safe

---

## 15. Summary

Each user interacts with exactly two namespaces—`mydrive` for private data and `shared` for shared data. File servers maintain authoritative metadata and enforce ACLs, while the metadata server maintains a denormalized shared index to optimize listing without participating in access control.
