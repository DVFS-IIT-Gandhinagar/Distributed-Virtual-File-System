# Refactored Architecture - Clean Go Project Structure

## Overview

The codebase has been refactored into a clean, maintainable architecture following Go project conventions and domain-driven design principles.

## Architecture Layers

### 1. Domain Layer (`internal/domain/`)

**Purpose**: Core business entities and rules

- `inode.go`: Core domain models (FID, Inode, ACL, InodeType)
- Pure domain logic with no external dependencies
- Thread-safe operations with proper synchronization

### 2. Storage Layer (`internal/fileserver/storage/`)

**Purpose**: Data persistence and retrieval abstractions

- `storage.go`: Storage interfaces and implementations
- `InodeStorage`: Manages inode metadata (memory-based)
- `FileSystemStorage`: Manages OS filesystem operations
- Clear separation between logical and physical storage

### 3. Service Layer (`internal/fileserver/service/`)

**Purpose**: Business logic and orchestration

- `fileserver.go`: Core file server business logic
- Coordinates between storage layers
- Handles user authentication/authorization
- Manages file operations (create, read, delete, etc.)

### 4. Handler Layer (`internal/fileserver/handlers/`)

**Purpose**: gRPC transport layer

- `handlers.go`: gRPC request/response handling
- Converts protobuf messages to/from domain objects
- Minimal logic - delegates to service layer

### 5. Client Architecture

#### Mount Layer (`internal/client/mount/`)

- `mount.go`: Virtual filesystem mount table
- Handles path resolution (`/mydrive`, `/shared`)
- Maps virtual paths to server connections

#### Cache Layer (`internal/client/cache/`)

- `cache.go`: Client-side caching with TTL
- Caches file contents and metadata
- Thread-safe operations

#### Client Service (`internal/client/service/`)

- `client.go`: Main client business logic
- Coordinates server communication
- Manages file handles and client state

## Key Improvements

### 1. **Separation of Concerns**

- Domain logic separated from transport/storage
- Clear interfaces between layers
- Easy to test and modify individual components

### 2. **Thread Safety**

- All shared state properly protected with mutexes
- Concurrent access patterns handled correctly
- No race conditions

### 3. **Error Handling**

- Consistent error propagation
- Proper error wrapping with context
- Graceful failure handling

### 4. **Testability**

- Interfaces allow easy mocking
- Pure domain logic testable in isolation
- Dependency injection pattern

### 5. **Type Safety**

- Strong typing with domain models
- Conversion between protobuf and domain types
- Compile-time safety

## Usage

### Starting File Server

```go
// Run the new file server
go run cmd/fileserver/main_new.go
```

### Running Client

```go
// Run the new client
go run cmd/client/main_new.go
```

## Current State

- ✅ Clean architecture established
- ✅ Core domain models defined
- ✅ Storage interfaces and implementations
- ✅ Basic file server service logic
- ✅ gRPC handlers for core operations
- ✅ Client service with mount table and caching
- ⚠️ Some gRPC operations need completion (ReadFile, WriteFile, etc.)
- ⚠️ Path resolution needs full implementation
- ⚠️ MDS (Metadata Server) not yet implemented

## Next Steps

1. Complete remaining gRPC operations
2. Implement full path resolution
3. Add comprehensive error handling
4. Implement MDS for shared file discovery
5. Add proper logging and metrics
6. Add configuration management
7. Add unit tests for all layers

## File Structure

```
internal/
├── domain/
│   └── inode.go          # Core business entities
├── fileserver/
│   ├── handlers/
│   │   └── handlers.go   # gRPC transport layer
│   ├── service/
│   │   └── fileserver.go # Business logic
│   └── storage/
│       └── storage.go    # Data persistence
└── client/
    ├── cache/
    │   └── cache.go      # Client-side caching
    ├── mount/
    │   └── mount.go      # Virtual mount management
    └── service/
        └── client.go     # Client business logic

cmd/
├── fileserver/
│   ├── main.go          # Original (to be replaced)
│   └── main_new.go      # Clean main function
└── client/
    ├── main.go          # Original (to be replaced)
    └── main_new.go      # Clean main function
```

This refactored architecture provides a solid foundation for implementing the full distributed virtual file system while maintaining code quality and extensibility.
