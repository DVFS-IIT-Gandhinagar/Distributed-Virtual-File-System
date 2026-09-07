# Recycle Bin & Trash Subsystem

This document specifies the DVFS deletion architecture, broken down into its safety principles (**In Essence**) and its internal mechanics (**Implementation Quirks**).

---

## 1. In Essence: Two-Tier Deletion Safety

Distributed filesystems require protection against accidental user deletions. DVFS provides a two-tier deletion lifecycle:

```
[Active File]
      |
      +---> trash <name> ---> [Soft-Deleted in .trash/] ---> restore <name> ---> [Restored File]
      |                                |
      |                                +---> delete -t <name> / clear_trash ---> [Permanently Destroyed]
      |
      +---> delete <name> ---> [Permanently Destroyed]
```

1. **Soft Delete (`trash`)**: Moves the file or directory into a protected, hidden `.trash/` container. The item is removed from active directory listings but its data and permissions remain intact on disk, allowing near-instant restoration.
2. **Permanent Delete (`delete`)**: Executes a recursive depth-first search (DFS) post-order purge from the FileServer, unlinks inodes from memory and persistent stores, removes physical OS files, and broadcasts unsharing notices.
3. **Trash Isolation Invariant**: The `.trash/` container is strictly protected. Users cannot navigate into `.trash/` with `cd`, nor can they create new files directly inside `.trash/`.

---

## 2. Implementation Quirks & Practical Realities

### 2.1 The Physical `.trash/` Directory
Each user root on a FileServer maintains a dedicated `.trash/` folder:
- **Automatic Initialization**: Created automatically on user creation or first trash operation.
- **Reserved Name**: The name `.trash` is reserved by the FileServer. Any attempt to create a file or folder named `.trash` via `create` or `mkdir` is rejected with an error.
- **Physical Relocation**: Trashing uses `os.Rename` to move the physical file or directory on the host filesystem into `.trash/`. Subtree paths and internal inode parent pointers are updated in memory.

### 2.2 Collision-Safe Renaming in Trash
If a user trashes a file named `report.pdf` from `mydrive/docs/`, and later trashes another file named `report.pdf` from `mydrive/downloads/`, storing both in `.trash/` would cause an OS collision.

The FileServer resolves this with `uniqueNameInDirLocked`:
- If `report.pdf` does not exist in `.trash/`, it is moved as `report.pdf`.
- If a collision occurs, the FileServer appends the inode ID: `report.pdf__42`.
- When restored, the collision suffix is stripped, restoring the original name.

### 2.3 Restore Metadata & Fallback Rules
When an item is moved to trash, the FileServer records its restoration context in an in-memory table named `fs.trashMeta`:

```go
type trashEntry struct {
    originalParentFID string
    originalName      string
    originalRelPath   string
    sharedSnapshots   []sharedDirSnapshot
}
```

- **Standard Restoration**: When `restore <name>` is executed, the FileServer looks up `fs.trashMeta`, identifies the original parent directory FID, and re-attaches the inode to its original parent.
- **Parent Deletion Fallback**: If the original parent directory was deleted while the file was in trash, but the metadata is present, the FileServer gracefully falls back to restoring the item directly into the user's root directory (`mydrive`).
- **Restart Limitation**: Because `fs.trashMeta` is stored in-memory, if the FileServer process restarts while items remain in trash, attempting to restore them returns:
  `"restore metadata not available (try restoring before restarting the server)"`.
  Users should restore required files prior to planned FileServer maintenance restarts.

### 2.4 Shared-User Trash Scoping
When multiple users collaborate inside a shared directory:
- Shared users can move files they have permission to modify into trash.
- However, when a shared user runs `show_trash`, the FileServer filters the entries using `userCanAccessInode`. A shared user only sees trashed items that were shared with them, and cannot see or restore the owner's private trashed files.
- Restoring is also strictly ACL-checked; a user cannot restore files from `.trash/` that they do not have permissions to access.

### 2.5 Complete CLI Deletion Reference

| Command | Flags | Target | Behavior |
|---|---|---|---|
| `trash <name>` | `-r` (recursive) | Active Directory | Soft delete: moves file or directory to `.trash/`. Non-empty directories require `-r`. |
| `restore <name>` | None | Trash Directory | Restores specified item from `.trash/` back to its original directory. |
| `show_trash` | None | Trash Directory | Lists all items currently in `.trash/` without requiring directory navigation. |
| `clear_trash` | None | Trash Directory | Permanently deletes all items currently in `.trash/`. |
| `delete <name>` | `-r` (recursive) | Active Directory | Permanent hard delete: bypasses trash and destroys file/directory immediately. |
| `delete -t <name>` | `-t` (from trash) | Trash Directory | Permanently purges a specific item from `.trash/` without clearing other trashed files. |

---

## 3. Manual Trash & Restore Test Runbook

Follow these 9 scenarios inside the client REPL to verify all trash and restore invariants:

### Case 1: Basic File Trash & Restore
```bash
mkdir test_trash
cd test_trash
create doc.txt
ls
trash doc.txt
ls
```
*Expected*: `doc.txt` is removed from `test_trash/`.
```bash
show_trash
```
*Expected*: `doc.txt` appears in `.trash/` listing.
```bash
restore doc.txt
ls
```
*Expected*: `doc.txt` is successfully restored back into `test_trash/`.

### Case 2: Non-Empty Directory Trash Requires `-r`
```bash
mkdir myfolder
cd myfolder
create item.txt
cd ..
trash myfolder
```
*Expected*: Fails with error: `directory is not empty; use -r to trash recursively`.
```bash
trash -r myfolder
show_trash
```
*Expected*: Succeeded; `myfolder` is moved to trash.

### Case 3: Direct Permanent Delete
```bash
mkdir perm_dir
cd perm_dir
create temp.txt
cd ..
delete -r perm_dir
show_trash
```
*Expected*: `perm_dir` is permanently purged; it does NOT appear in `show_trash`.

### Case 4: Collision-Safe Renaming in `.trash/`
```bash
mkdir dirA dirB
cd dirA
create report.txt
cd ../dirB
create report.txt
cd ../dirA
trash report.txt
cd ../dirB
trash report.txt
show_trash
```
*Expected*: Both files exist in trash, one with its original name (`report.txt`) and one with its inode ID disambiguator (`report.txt__<inodeID>`).

### Case 5: Protection of `.trash/` Namespace
```bash
mkdir .trash
create .trash
```
*Expected*: FileServer rejects creation of items named `.trash`. Direct navigation (`cd .trash`) is likewise rejected.

### Case 6: Emptying Trash (`clear_trash`)
```bash
show_trash
clear_trash
show_trash
```
*Expected*: All trashed items are permanently unlinked; `show_trash` outputs `(trash is empty)`.

### Case 7: Selective Item Deletion from Trash (`delete -t`)
```bash
create fileA.txt
create fileB.txt
trash fileA.txt
trash fileB.txt
show_trash
delete -t fileA.txt
show_trash
```
*Expected*: Only `fileA.txt` is permanently purged; `fileB.txt` remains intact in `.trash/`.

### Case 8: Server Restart Limitation Verification
```bash
create test_restart.txt
trash test_restart.txt
show_trash
# In FileServer terminal, restart the fileserver process
# Back in client:
refresh
restore test_restart.txt
```
*Expected*: Fails with: `restore metadata not available (try restoring before restarting the server)`.

### Case 9: Shared-User Trash Scoping & ACL Isolation
1. User `alice` shares folder `proj` with user `bob`.
2. `alice` trashes a private file `secret.txt` in `mydrive`.
3. `bob` trashes `task.txt` inside `proj`.
4. When `bob` runs `show_trash`, `bob` sees `task.txt` but CANNOT see `secret.txt`.
5. `bob` attempting `restore secret.txt` is rejected with `permission denied`.

---

### Automated Unit & Integration Tests
Run the automated test suite for trash and restore:
```bash
go test ./internal/fileserver -run TestTrashRestore -v
```
