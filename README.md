# AFS Phase-0 Implementation Plan (Go + gRPC)

This document summarizes the **Phase-0 plan** for implementing an AFS-inspired distributed file system in Go.  
The goal of Phase-0 is to build a **correct AFS core** (FID-based access, client caching, and callbacks) while deliberately simplifying persistence and discovery.

---

## 1. Phase-0 Goals

Phase-0 focuses on correctness of semantics, not completeness.

### What Phase-0 MUST achieve
- FID-based file access (no path-based identity)
- AFS-style client-side caching
- Server-driven callback invalidation
- Clean separation of client and file server responsibilities
- gRPC-based RPC interfaces

### What Phase-0 deliberately omits
- Persistent inode database
- Metadata server (MDS)
- Sharing and ACLs beyond ownership
- Rename and move
- Crash recovery

---

## 2. High-Level Architecture (Phase-0)

Components:
- **Client**
  - Maintains a mount table (hardcoded for now)
  - Caches file data indexed by FID
  - Receives callback invalidations from file servers
- **File Server (FS)**
  - Authoritative for data and metadata
  - Generates FIDs
  - Maps FIDs to OS filesystem paths (hardcoded, in-memory)
  - Grants and revokes callbacks

> The metadata server is intentionally excluded in Phase-0.

---

## 3. File Identifier (FID)

Each file or directory is identified by a **File Identifier (FID)**:

```
FID = <fs_id, inode_id, generation>
```

### FID Invariants
- FIDs are generated **only by the file server**
- Clients treat FIDs as opaque values
- FIDs are the only stable identity in the system

---

## 4. FID Generation Strategy

### Phase-0 Rule
> **inode_id values are never reused**

- `inode_id` is monotonically increasing
- `generation` is always set to `1`
- Deletion removes the mapping but does not recycle identifiers

This guarantees:
- No stale references
- No generation mismatch handling needed yet
- Extremely simple implementation

---

## 5. FS Internal State (Phase-0)

The file server maintains in-memory state only.

### FID Generator
- Generates new `(inode_id, generation=1)` pairs
- Protected by a mutex

### Inode Table (Hardcoded Mapping)

```
FID → OSPath
```

Example:
```
<1,1,1> → /srv/fs1/users/umang
<1,2,1> → /srv/fs1/users/umang/a.txt
```

No lookup, rename, or persistence is implemented in Phase-0.

---

## 6. Client Mounting (Phase-0)

Mounting is **hardcoded in the client**.

Example mount table:
```
mydrive → (fs_addr, root_fid)
```

This avoids:
- FS mount RPCs
- Metadata server dependencies

The mount table abstraction remains unchanged for later phases.

---

## 7. gRPC API Shape

Even in Phase-0, RPCs are designed **as if FIDs are permanent**.

### Example RPCs
- `Read(FID, client_id) → data`
- `Write(FID, data)`
- `Invalidate(FID)` (callback)

Only payloads will evolve in later phases; API semantics remain stable.

---

## 8. AFS-Style Client-Side Caching

### Cache Key
- Cache entries are indexed by **FID**, not by path

### Cache Rule
- If the client holds a valid callback for a FID, cached data is trusted
- On callback invalidation, cache entry is marked invalid

---

## 9. Callback Mechanism

### FS Responsibilities
- Track which clients hold callbacks per FID
- Invalidate callbacks on write or delete
- Never rely on clients for correctness

### Client Responsibilities
- Mark cached data invalid on callback
- Refetch data on next access

This mechanism is the **core of AFS correctness**.

---

## 10. Delete Semantics (Phase-0)

On delete:
- FS removes FID → OSPath mapping
- FS invalidates all callbacks
- inode_id is never reused

---

## 11. Evolution Path

The Phase-0 design is intentionally forward-compatible.

| Phase | Added Features |
|-----|---------------|
| Phase-1 | Persistent InodeDB, lookup, generation handling |
| Phase-2 | Metadata Server (MDS), shared volumes |
| Phase-3 | ACLs, rename, directory sharing |

Client semantics remain unchanged across phases.

---

## 12. Key Invariant

> **All correctness-critical mechanisms (identity, caching, callbacks) are implemented in Phase-0; later phases only add metadata and discovery.**

---

## 13. Summary

Phase-0 establishes a correct AFS foundation using FIDs, gRPC, and server-driven callbacks, while intentionally simplifying persistence and mounting. By preserving the correct identity and caching model from the start, the system can evolve incrementally without architectural refactoring.
