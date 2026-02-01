# Simple Distributed Virtual File System - Clean Architecture

## Overview

A minimal, clean implementation of a distributed virtual file system with basic client-server gRPC communication, CRUD operations, and an interactive terminal-based client interface.

## Architecture

### Simple 3-Layer Design

```
internal/
├── domain/
│   └── types.go           # Core types (FID, Inode, InodeType)
├── fileserver/
│   ├── server.go          # File server business logic
│   └── handlers.go        # gRPC handlers
└── client/
    ├── client.go          # Simple client implementation
    └── handler.go         # Terminal command interface

cmd/
├── fileserver/
│   └── main.go            # File server main
└── client/
    └── main.go            # Client main with interactive shell
```

## Components

### Domain Types (`internal/domain/types.go`)

- **FID**: File Identifier with server ID, inode ID, generation number
- **Inode**: Represents files and directories
- **InodeType**: File vs Directory enumeration
- Clean protobuf conversion methods

### File Server (`internal/fileserver/`)

- **server.go**: Core file operations (create, list, get, delete)
- **handlers.go**: gRPC request handlers
- Thread-safe with proper locking
- User directory management

### Client (`internal/client/`)

- **client.go**: Simple connection to file server and basic CRUD operations
- **handler.go**: Interactive terminal command interface
- Clean error handling and user-friendly interface

## Interactive Terminal Interface

The client now provides an interactive shell with the following commands:

- `ls`, `list` - List files and directories
- `create <filename>` - Create a new file
- `mkdir <dirname>` - Create a new directory
- `info` - Show root directory information
- `help` - Show available commands
- `exit`, `quit` - Exit the client

### Example Session

```
dvfs> ls
(empty directory)

dvfs> create hello.txt
File 'hello.txt' created successfully (FID: fs1_2_1)

dvfs> mkdir documents
Directory 'documents' created successfully (FID: fs1_3_1)

dvfs> ls
Name                 Type       Size
----                 ----       ----
hello.txt            file            0
documents            dir             0

dvfs> info
Root Directory Information:
  Name: romit
  Type: directory
  Size: 0 bytes
  FID:  fs1_0_1
```

## Function Call Traces

### Client Connection Flow

```
main.go:main()
├── client.NewClient(username)
├── client.Connect(serverAddress)
│   ├── grpc.NewClient() → establish connection
│   ├── pb.NewFileServerClient() → create gRPC client
│   └── RegisterClient() gRPC call
│       ├── GRPCHandler.RegisterClient() [server]
│       ├── FileServer.GetUserRoot() [server]
│       │   ├── os.MkdirAll() → create user directory
│       │   └── creates root inode with FID{fs1, 0, 1}
│       └── returns UserRootFid to client
└── handler.Start() → begins interactive shell
```

### File Creation Flow

```
handler.handleCreateFile(filename)
├── client.CreateFile(filename)
│   └── CreateFile() gRPC call
│       ├── GRPCHandler.CreateFile() [server]
│       ├── FileServer.GetUserRoot() → get parent FID
│       ├── FileServer.CreateFile(parentFID, name, user, FILE)
│       │   ├── atomic.AddUint64() → generate new inode ID
│       │   ├── os.Create() → create OS file
│       │   ├── creates new inode with generated FID
│       │   ├── stores inode in memory map
│       │   └── adds FID to parent's children list
│       └── returns new FID to client
└── displays success message with FID
```

### Directory Listing Flow

```
handler.handleList()
├── client.ListFiles()
│   └── ListDir() gRPC call
│       ├── GRPCHandler.ListDir() [server]
│       ├── FileServer.ListDirectory(rootFID)
│       │   ├── gets root inode from memory map
│       │   ├── iterates through children FIDs
│       │   ├── retrieves each child inode
│       │   └── updates file sizes via os.Stat()
│       └── returns DirEntry list to client
└── displays formatted table of files
```

### File Information Flow

```
handler.handleInfo()
├── client.GetFileInfo()
│   └── GetAttr() gRPC call
│       ├── GRPCHandler.GetAttr() [server]
│       ├── FileServer.GetInode(rootFID)
│       │   ├── retrieves inode from memory map
│       │   └── updates size via os.Stat() if file
│       └── returns inode metadata to client
└── displays root directory information
```

## Features

✅ **Interactive Terminal**: Command-line interface with help system
✅ **Client Registration**: Get user root FID from server  
✅ **File Creation**: Create files and directories  
✅ **Directory Listing**: List files in user's root  
✅ **File Attributes**: Get file/directory information  
✅ **Clean Error Handling**: Proper error propagation  
✅ **Thread Safety**: Safe concurrent access

## Usage

### 1. Start File Server

```bash
go run cmd/fileserver/main.go
```

- Starts server on `0.0.0.0:50051`
- Data stored in `./fileserver_data/`

### 2. Run Client

```bash
go run cmd/client/main.go
```

- Connects as user "romit"
- Starts interactive terminal interface

## Example Session

```
Connecting to server at 127.0.0.1:50051 as user romit...
Connected successfully!

=== Distributed VFS Client ===
Available commands: ls, create, mkdir, info, help, exit

dvfs> ls
(empty directory)

dvfs> create hello.txt
File 'hello.txt' created successfully (FID: fs1_2_1)

dvfs> mkdir documents
Directory 'documents' created successfully (FID: fs1_3_1)

dvfs> ls
Name                 Type       Size
----                 ----       ----
hello.txt            file            0
documents            dir             0

dvfs> info
Root Directory Information:
  Name: romit
  Type: directory
  Size: 0 bytes
  FID:  fs1_0_1

dvfs> help
Available commands:
  ls, list           - List files and directories
  create <filename>  - Create a new file
  mkdir <dirname>    - Create a new directory
  info               - Show root directory information
  help               - Show this help message
  exit, quit         - Exit the client

dvfs> exit
Goodbye!
```

## Key Design Decisions

### 1. **Simplicity First**

- No complex abstractions
- Minimal interfaces
- Direct, clear code paths

### 2. **Clean Separation**

- Domain types separate from transport
- Business logic separate from gRPC
- Clear dependency direction

### 3. **No Over-Engineering**

- No caching (will be added later)
- No complex mount tables
- No callback servers
- Just the essentials

### 4. **Thread Safety**

- Proper mutex usage
- Atomic operations where appropriate
- Safe concurrent access patterns

### 5. **Error Handling**

- Clear error messages
- Proper error propagation
- No silent failures

## Current Limitations (By Design)

- Only supports root directory operations
- No file I/O (read/write) yet
- No permissions/ACLs (simplified)
- No metadata server (MDS)
- Single file server only

## Next Steps

1. Add file read/write operations
2. Implement subdirectory navigation
3. Add basic permissions
4. Add metadata server for sharing
5. Add proper configuration management
6. Add comprehensive tests

This provides a solid, clean foundation for building the full distributed file system while maintaining simplicity and clarity.
