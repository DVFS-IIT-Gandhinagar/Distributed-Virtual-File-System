# Cache Handling System

## Overview

The Cache Handling System provides a client-side caching layer for the Distributed Virtual File System (DVFS). It sits between the user interface (Cobra handlers) and network operations (Client), reducing network calls and improving performance by caching file system metadata and file content locally.

## Architecture

```
User Interface (Cobra Handlers)
           ↓
    Cache Handler Layer
           ↓
    Client (Network Operations)
           ↓
    File Server (Remote)
```

**Components:**

- **CacheHandler**: Main cache management interface
- **CNode**: Individual cache nodes representing files/directories
- **Local Cache Directory**: `./.cache/` for storing file content

## Cache Structure

### CNode Tree

The cache uses a tree structure (`CNode`) that mirrors the remote file system (which is discovered so far):

```go
type CNode struct {
    Name          string                // File/directory name
    Type          domain.InodeType      // File (0) or Directory (1)
    fid           *domain.FID           // Remote file identifier
    children      map[string]*CNode     // Child nodes (directories only)
    contentCached bool                  // File content cached flag
    contentUID    string                // Unique cache file identifier
    parent        *CNode                // Parent directory reference
}
```

### Cache Hierarchy

- **Root Node**: Represents remote root directory (`mydrive`)
- **Directory Nodes**: Contain child maps for navigation
- **File Nodes**: Store content cache status and unique identifiers

## Core Workflows

### 1. Cache Initialization

```
1. Create root CNode for remote file system
2. Fetch root directory contents from server
3. Populate initial cache with metadata
4. Set current directory to root
```

### 2. File Read Operation

```
Input: filename
├── Cache Hit (file content cached)
│   ├── Read from local cache file (.cache/{contentUID})
│   └── Return content
└── Cache Miss (not cached or not found)
    ├── Validate file exists in current directory
    ├── Generate unique cache ID (UUID)
    ├── Download file content from server
    ├── Store in local cache directory
    ├── Mark as cached in CNode
    └── Return content
```

### 3. Directory Navigation

```
Input: directory name
├── Special Cases
│   ├── "/" → Navigate to root
│   └── ".." → Navigate to parent
└── Standard Navigation
    ├── Lookup directory in current cache
    ├── Update current directory pointer
    ├── Change client FID context
    └── Refresh directory cache from server
```

### 4. File/Directory Creation

```
1. Perform server operation (create file/directory)
2. On success: Update local cache structure
3. Add new CNode to current directory children
4. Maintain cache consistency
```

## Cache Management

### Cache Scope

**Cached Data:**

- File and directory metadata (names, types, FIDs)
- File content (stored as UUID-named files in `.cache/`)
- Directory structure and hierarchy via the CNodes based local tree

**Always Server-Fetched:**

- File sizes (not currently cached)
- Directory contents (refreshed on navigation)

### Cache Invalidation

**Current Strategy:**

- Cache refreshes on directory change
- Manual cache clearing available
- Session-only persistence (cleared on restart)

**Future Enhancement:**

- Server callback-based invalidation for multi-client consistency

### Cache Operations

| Operation                | Cache Behavior                                   |
| ------------------------ | ------------------------------------------------ |
| `ReadFile()`             | Check cache → Download if miss → Store locally   |
| `ListFiles()`            | Return from current directory cache              |
| `ChangeDirectory()`      | Navigate cache tree → Refresh directory contents |
| `CreateFile/Directory()` | Update cache after server operation              |
| `ClearCache()`           | Remove all cached content files                  |

## Key Benefits

1. **Reduced Network Calls**: File content and metadata cached locally
2. **Improved Navigation**: Directory structure cached for faster and offline traversal/browsing
3. **Performance**: Local file reads eliminate network latency

## Current Limitations

1. **Single Client**: No multi-client cache invalidation
2. **Size Information**: File sizes not cached, always fetched from server
4. **Manual Refresh**: Directory contents only refresh on navigation

## Implementation Details

### Cache Directory Structure

```
./.cache/
├── {uuid-1}    # Cached file content
├── {uuid-2}    # Cached file content
└── ...
```

### Memory Structure

```
Root (mydrive)
├── documents/
│   ├── file1.txt (cached: true, uid: uuid-1)
│   └── subfolder/
└── images/
    └── photo.jpg (cached: false)
```

## Error Handling

- **Cache Miss**: Transparent server fallback
- **Network Errors**: Return appropriate error messages
- **File System Errors**: Handle local cache I/O issues
- **Invalid Navigation**: Validate directory existence in cache

## Future Enhancements

1. **Server Callbacks**: Real-time cache invalidation
2. **Persistent Cache**: Survive client restarts
3. **Size Caching**: Include file sizes in metadata
4. **Smart Refresh**: Selective cache updates
5. **Cache Policies**: LRU eviction, size limits

---

_This cache system provides the foundation for efficient client-side operations while maintaining consistency with the remote file system state._
