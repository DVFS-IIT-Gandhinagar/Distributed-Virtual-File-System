# DVFS Client System Architecture Diagrams

This document outlines the detailed system architecture and interactions of the DVFS Client Shell, Caching System, and Callback Listener.

## 1. Client System Component Map

```mermaid
graph TD
    A[cmd/client/main.go] -->|Initializes| B(Client)
    A -->|Initializes| C(CacheHandler)
    A -->|Initializes| D(CallbackServer)
    A -->|Starts| E(readline REPL)
    
    C -->|Contains| F["root CNode"]
    C -->|Contains| G["curr pointer"]
    C -->|Stores files in| H[cacheDir]
    
    B -->|gRPC Conn| I("metaConn (MetaServer)")
    B -->|gRPC Conn| J("serverConn (FileServer)")
    B -->|Contains| K[username]
    B -->|Contains| L[rootUser]
    
    D -->|Listens on| M["ephemeral port"]
    D -->|Receives| N["Invalidate RPCs"]
    
    O[CobraCompleter] -->|Reads children for tab completion| G
    
    P["SIGINT/SIGTERM"] -->|Intercepted| Q["Client.Disconnect"]
    Q -->|RPC| R[UnregisterClient]
```

## 2. CNode Tree Structure and State

```mermaid
classDiagram
    class CacheHandler {
        +*CNode root
        +*CNode curr
        +String cacheDir
        +*Client client
    }

    class CNode {
        +String Name
        +InodeType Type
        +*FID fid
        +uint64 Size
        +map~string, *CNode~ children
        +bool contentCached
        +String contentUID
        +*CNode parent
    }

    CacheHandler --> "1" CNode : root
    CacheHandler --> "1" CNode : curr
    CNode "1" *-- "many" CNode : children
    CNode --> "1" CNode : parent
```

## 3. Interactive Session Lifecycle

```mermaid
sequenceDiagram
    participant User
    participant MainLoop
    participant CacheHandler
    participant Client
    participant MetaServerConn
    participant FileServerConn
    participant CallbackServer

    User->>MainLoop: Launch client (flags)
    MainLoop->>Client: Dial MetaServer (mTLS)
    Client->>MetaServerConn: GetRoots
    MetaServerConn-->>Client: roots list
    MainLoop-->>User: Display numbered menu
    User->>MainLoop: Selects root
    MainLoop->>Client: Navigate (get FileServer address)
    Client->>FileServerConn: Dial FileServer (mTLS)
    MainLoop->>CallbackServer: Start listening (ephemeral port)
    Client->>FileServerConn: RegisterClient (send CallbackAddress)
    Client->>FileServerConn: ListDir(rootFID)
    FileServerConn-->>Client: Directory contents
    MainLoop->>CacheHandler: Populate CNode tree
    MainLoop-->>User: Enter readline REPL
    User->>MainLoop: Types exit
    MainLoop->>CacheHandler: ClearCache
    MainLoop->>Client: Disconnect
    Client->>FileServerConn: UnregisterClient
```

## 4. Cache Miss and Hit Flow

```mermaid
flowchart TD
    A["User runs: read filename"] --> B{"Look up CNode in curr.children"}
    B -->|Found| C{"contentCached == true?"}
    B -->|Not Found| Z["Error: File not found"]
    
    C -->|Yes| D["Read from .cache/UUID file (zero RPCs)"]
    D --> F["Display content to terminal"]
    
    C -->|No| G["DownloadFile(parentFID, name) stream"]
    G --> H["Receive chunks & assemble"]
    H --> I["Write to .cache/UUID"]
    I --> J["Set contentCached = true, contentUID = UUID"]
    J --> F
```

## 5. Push Invalidation Callback Handler

```mermaid
sequenceDiagram
    participant FileServer
    participant CallbackServer
    participant CacheHandler
    participant ReadlinePrompt

    FileServer->>CallbackServer: Invalidate RPC (new_version field multiplexing)
    
    alt Event 1 (FILE_UPDATED)
        CallbackServer->>CacheHandler: find CNode by FID
        CacheHandler->>CacheHandler: delete .cache/UUID file
        CacheHandler->>CacheHandler: set contentCached=false
        CallbackServer->>ReadlinePrompt: notify via SetNotifyWriter
    else Event 2 (DIR_NEW_FILE)
        CallbackServer->>ReadlinePrompt: log notice to notify writer
        CallbackServer-->>ReadlinePrompt: prompt user to refresh
    else Event 3 (FILE_DELETED)
        CallbackServer->>CacheHandler: evict CNode from curr.children
        CacheHandler->>CacheHandler: delete cache file
        CallbackServer->>ReadlinePrompt: notify
    end
```

## 6. cd Command Navigation State

```mermaid
stateDiagram-v2
    state "RootSelectionMenu" as rootmenu
    state "InsideRoot" as insideroot
    state "InsideSubDir" as insidesubdir

    [*] --> rootmenu
    rootmenu --> insideroot : user selects root number
    insideroot --> insidesubdir : cd dirname (single level)
    insidesubdir --> insidesubdir : cd dirname (deeper)
    insidesubdir --> insideroot : cd ..
    insideroot --> rootmenu : cd ..
    insidesubdir --> rootmenu : exit then reselect
    insideroot --> rootmenu : exit then reselect
    
    note right of insidesubdir
        Multi-segment paths like cd a/b 
        are NOT supported.
    end note
```

## 7. ReRegister Session Healing

```mermaid
sequenceDiagram
    participant User
    participant CacheHandler
    participant Client
    participant FileServer

    User->>CacheHandler: refresh
    CacheHandler->>Client: ReRegister
    Client->>FileServer: RegisterClient RPC(username, rootUser, rootPath, callbackAddress)
    FileServer-->>Client: returns rootFID (stable due to InodeStore)
    Client->>CacheHandler: Validate rootFID matches cached root
    CacheHandler->>CacheHandler: populateCurrentDirCache -> ListDir
```
