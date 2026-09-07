# Client CLI & Shell Reference

This document is the comprehensive reference manual for the DVFS interactive terminal shell (`dvfs>`) and client binary commands.

---

## 1. Starting the Client

The client binary connects to the cluster either through the MetaServer (recommended) or directly to an individual FileServer.

### Command-Line Flags

```bash
./bin/client [flags]
# or
go run ./cmd/client/main.go [flags]
```

| Flag | Type | Default | Description |
|---|---|---|---|
| `-username` | string | `"romit"` | Username for session authentication and root lookup |
| `-meta` | bool | `true` | When true, connects via MetaServer coordinator for root discovery |
| `-ip_addr` | string | `"127.0.0.1"` | IP address of target MetaServer or FileServer |
| `-port` | string | `""` | Port to connect to (`50051` for MetaServer; `50052` for direct FileServer) |
| `-use_gist` | bool | `true` | Enables dynamic discovery of node LAN IPs from GitHub Gist |
| `-gist_url` | string | `""` | Custom GitHub Gist URL (overrides default `machines.json` location) |
| `-insecure` | bool | `false` | Disables TLS verification (for local loopback testing only) |

### Interactive Root Selection
When starting with `-meta=true`, the client displays a numbered menu:
```text
Available roots:
  [1] mydrive (personal)
  [2] lab_data (shared by romit)
Select root [1-2] or 0 to exit: 1
```
Selecting a root establishes a gRPC connection to the hosting FileServer and drops into the interactive `dvfs>` prompt.

---

## 2. Shell Commands Reference

### Navigation & Inspection

#### `ls`
Lists the contents of the current working directory.
- **Syntax**: `ls`
- **Behavior**: Served instantaneously from local CNode cache without network calls.
- **Example**:
  ```text
  dvfs> ls
  Name                 Type             Size
  ----                 ----             ----
  .trash               dir                 0
  notes.txt            file             1024
  projects             dir                 0
  ```

#### `cd <dirname>`
Changes the current working directory.
- **Syntax**: `cd <dirname>`
- **Special paths**:
  - `cd /`: Navigates to the root of the active storage tree.
  - `cd ..`: Moves to the parent directory. Running `cd ..` from the top of any root safely exits to the MetaServer root selection menu.
- **Note**: Multi-segment paths (e.g., `cd dir1/dir2`) are not supported. Only immediate child directories can be navigated to in a single command.
- **Protection**: Direct navigation into `.trash` is strictly forbidden.

#### `pwd`
Prints the current virtual working directory path.
- **Syntax**: `pwd`
- **Example**:
  ```text
  dvfs> pwd
  mydrive/projects/dvfs
  ```

#### `info`
Displays metadata attributes for the current directory.
- **Syntax**: `info`
- **Example**:
  ```text
  dvfs> info
  Directory Information:
    Name: projects
    Type: dir
    Size: 4096 bytes
    FID:  fs1_5_1
  ```

#### `viscache`
Renders an indented tree visualization of the client's in-memory CNode cache.
- **Syntax**: `viscache`
- **Example**:
  ```text
  dvfs> viscache
  Cache Structure:
  - mydrive (directory)
    - .trash (directory)
    - report.txt (file (cached: true))
  ```

#### `refresh`
Forces an immediate re-fetch of current directory metadata from the FileServer, re-synchronizing the local cache tree.
- **Syntax**: `refresh`
- **Session Auto-Recovery**: Also re-establishes client session registration (`ReRegister()`) with the FileServer, restoring push notification callbacks without restarting the client if the FileServer had crashed and rebooted.

#### `clear`
Clears the terminal screen buffer.
- **Syntax**: `clear`

---

### File & Directory Management

#### `create <filename>`
Creates a new, empty file in the current directory.
- **Syntax**: `create <filename>`
- **Example**:
  ```text
  dvfs> create notes.txt
  File 'notes.txt' created successfully (FID: fs1_12_1)
  ```

#### `mkdir <dirname>`
Creates a new directory in the current working directory.
- **Syntax**: `mkdir <dirname>`
- **Example**:
  ```text
  dvfs> mkdir datasets
  Directory 'datasets' created successfully (FID: fs1_13_1)
  ```

#### `read <filename>`
Reads and displays the text content of a file.
- **Syntax**: `read <filename>`
- **Behavior**: If the file is already cached locally, reads from `./.cache/<UUID>` with zero network round trips. If not cached, streams the file from the FileServer, caches it, and displays the content.
- **Example**:
  ```text
  dvfs> read notes.txt
  Contents of 'notes.txt':
  Distributed systems project meeting notes.
  ```

#### `upload <local_path>`
Uploads a file or an entire directory tree from the host machine into the current DVFS directory.
- **Syntax**: `upload <path_to_local_file_or_directory>`
- **Behavior**: Splits files into 4 MB chunks and streams them via gRPC `UploadFile`. Automatically updates live streaming throughput telemetry.
- **Example**:
  ```text
  dvfs> upload C:\Users\user\Documents\dataset.csv
  Uploading 'C:\Users\user\Documents\dataset.csv'...
  'C:\Users\user\Documents\dataset.csv' uploaded successfully
  ```

#### `download <name>`
Downloads a remote file or folder into the local `./Download/` directory.
- **Syntax**: `download <filename_or_dirname>`
- **Behavior**: Streams chunks via gRPC `DownloadFile` into `./Download/<name>`.
- **Example**:
  ```text
  dvfs> download report.pdf
  Downloading 'report.pdf'...
  'report.pdf' downloaded successfully
  ```

---

### Deletion & Recycle Bin

#### `trash <name>`
Soft-deletes a file or directory, moving it into the user's hidden `.trash/` container.
- **Syntax**: `trash [-r] <name>`
- **Flags**: `-r` (recursive, required for non-empty directories).
- **Example**:
  ```text
  dvfs> trash old_notes.txt
  Moved 'old_notes.txt' to trash
  ```

#### `restore <name>`
Restores an item from `.trash/` back to its original parent directory.
- **Syntax**: `restore <name>`
- **Example**:
  ```text
  dvfs> restore old_notes.txt
  Restored 'old_notes.txt'
  ```

#### `show_trash`
Lists all items currently residing in `.trash/`.
- **Syntax**: `show_trash`
- **Example**:
  ```text
  dvfs> show_trash
  Name                 Type             Size
  ----                 ----             ----
  old_notes.txt        file             1024
  temp_data            dir                 0
  ```

#### `clear_trash`
Permanently destroys all files and directories currently present in `.trash/`.
- **Syntax**: `clear_trash`
- **Example**:
  ```text
  dvfs> clear_trash
  Permanently deleted 2 entr(y/ies) from trash
  ```

#### `delete <name>`
Permanently purges a file or directory immediately without sending it to `.trash/`.
- **Syntax**: `delete [-r] [-t] <name>`
- **Flags**:
  - `-r`: Recursive delete (required for directories).
  - `-t`: Permanently purges a single specific item directly from `.trash/`.
- **Example**:
  ```text
  dvfs> delete -r stale_experiment
  Successfully deleted 'stale_experiment' and all its contents
  ```

---

### Sharing & Collaboration

#### `sharewith <username>`
Shares the current working directory with another cluster user.
- **Syntax**: `sharewith <username>`
- **Behavior**: Updates the directory's ACL, propagates permissions down the subtree via recursive DFS, and registers the shared root with the MetaServer.
- **Example**:
  ```text
  dvfs> sharewith alice
  Sharing root directory with 'alice'...
  Root directory shared successfully with 'alice'
  ```

#### `unsharewith <username>`
Revokes sharing permissions for a user from the current working directory.
- **Syntax**: `unsharewith <username>`
- **Example**:
  ```text
  dvfs> unsharewith alice
  Unsharing root directory with 'alice'...
  Root directory unshared successfully with 'alice'
  ```

---

### Session Teardown

#### `exit`
Exits the client gracefully.
- **Syntax**: `exit`
- **Behavior**:
  1. Clears local UUID cache files via `ClearCache()`.
  2. Dispatches `UnregisterClient` gRPC RPC to the FileServer, immediately removing the active session and decrementing the active connections counter.
  3. Closes background callback listeners and exits cleanly.

## Diagrams

For visual architecture maps, sequence diagrams, and flowcharts describing the client shell, callback handlers, and interactive state lifecycle, please refer to:
- [Client System Architecture Diagrams](./diagrams/client_system.md)
