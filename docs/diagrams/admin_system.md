# Admin System Diagrams

This document contains architectural and behavioral diagrams for the DVFS Admin Console and Authentication systems. The Admin Console acts as the central control plane, polling metrics, processing telemetry, and dispatching orchestration commands.

## Component Overview

```mermaid
graph TD
    subgraph "DVFS Admin Console"
        AdminServer["AdminServer"]
        
        AuthMgr["AuthManager"]
        Orch["Orchestrator"]
        AlertMgr["AlertManager"]
        CmdHist["CommandHistory"]
        MetricsPoller["MetricsPoller (Goroutine)"]
        
        AdminServer --> AuthMgr
        AdminServer --> Orch
        AdminServer --> AlertMgr
        AdminServer --> CmdHist
        AdminServer --> MetricsPoller
    end
    
    subgraph "HTTP Router"
        Router["ServeMux Router"]
        Router -->|"/api/auth/login"| H_Login["handleAuthLogin (Public)"]
        Router -->|"/api/cluster"| H_Cluster["handleCluster (Public)"]
        Router -->|"/api/actions/execute"| H_ActionExec["handleActionExecute (RequireAuth)"]
        Router -->|"/api/alerts"| H_Alerts["handleAlerts (Public)"]
        Router -->|"/api/users"| H_Users["handleUsers (RequireAuth)"]
    end
    
    AdminServer --> Router
```

## Authentication Flow

```mermaid
sequenceDiagram
    participant Browser
    participant AdminServer
    participant AuthManager
    
    %% Login Flow
    Browser->>AdminServer: POST /api/auth/login (password)
    AdminServer->>AuthManager: VerifyPassword(candidate)
    AuthManager->>AuthManager: sha256(candidate)
    AuthManager->>AuthManager: subtle.ConstantTimeCompare(hash, expected)
    AuthManager-->>AdminServer: isValid
    
    alt is valid
        AdminServer->>AuthManager: CreateSession()
        AuthManager-->>AdminServer: sessionToken
        AdminServer-->>Browser: Set-Cookie: dvfs_admin_token (HttpOnly) & 200 OK
    else invalid
        AdminServer-->>Browser: 401 Unauthorized
    end
    
    %% Authenticated Request
    Browser->>AdminServer: GET /api/users (with credentials)
    AdminServer->>AuthManager: ExtractToken(request)
    note over AuthManager: 1. Bearer Header<br/>2. Cookie<br/>3. Query Param
    AuthManager->>AuthManager: Check expiry in sessions map
    AuthManager-->>AdminServer: isAuthenticated
    
    alt Authenticated
        AdminServer->>Browser: 200 OK (User Data)
    else Unauthenticated
        AdminServer->>Browser: 401 Unauthorized
    end
```

## Alert State Machine

```mermaid
stateDiagram-v2
    [*] --> Monitoring
    
    Monitoring --> Warning : Storage >80% \n Temp >65C \n ErrorRate >5%
    Monitoring --> Critical : Storage >95% \n Temp >85C \n ErrorRate >20% \n Quota >=100%
    
    Warning --> Critical : Metric crosses Critical threshold
    Critical --> Warning : Metric drops below Critical but stays Warning
    
    Warning --> Resolved : Metric drops below Warning threshold
    Critical --> Resolved : Metric drops below Warning threshold
    
    Resolved --> Monitoring : Auto-resolve logic
```

## Admin HTTP Routes Table

| Method | Path | Auth Required | Handler Function |
|--------|------|---------------|------------------|
| POST | `/api/auth/login` | No | `handleAuthLogin` |
| POST | `/api/auth/logout` | No | `handleAuthLogout` |
| GET | `/api/auth/status` | No | `handleAuthStatus` |
| GET | `/api/cluster` | No | `handleCluster` |
| GET | `/api/cluster/summary` | No | `handleCluster` |
| GET | `/api/performance` | No | `handlePerformance` |
| GET | `/api/performance/export`| No | `handlePerformanceExport` |
| GET | `/api/history/` | No | `handleHistory` |
| GET | `/api/users` | Yes | `handleUsers` |
| PUT/POST | `/api/users/` | Yes | `handleUserQuota` |
| GET | `/api/actions/presets` | Yes | `handleActionPresets` |
| GET | `/api/actions/history` | Yes | `handleActionHistory` |
| POST | `/api/actions/execute` | Yes | `handleActionExecute` |
| GET | `/api/alerts` | No | `handleAlerts` |
| GET | `/api/alerts/summary` | No | `handleAlertSummary` |
| POST | `/api/alerts/resolve` | Yes | `handleResolveAlert` |
| POST | `/api/alerts/resolve-all`| Yes | `handleResolveAllAlerts` |
| GET | `/api/logs/tail` | Yes | `handleLogTail` |
| WS | `/ws/actions` | Yes | `NewWebSocketHandler` |

## Metrics Polling Pipeline

```mermaid
sequenceDiagram
    participant BrowserClient
    participant WebSocketHub
    participant AdminServer
    participant MetricsPoller
    participant RingBuffer
    participant FileServerHTTP
    
    loop Every 5 Seconds
        MetricsPoller->>AdminServer: Trigger poll tick
        AdminServer->>FileServerHTTP: GET /metrics
        FileServerHTTP-->>AdminServer: JSON Metrics
        AdminServer->>AdminServer: Parse FileserverMetrics
        AdminServer->>RingBuffer: Store Snapshot (capacity 720 points)
        AdminServer->>WebSocketHub: Broadcast Update
        WebSocketHub-->>BrowserClient: WebSocket Message (Metrics)
    end
```

## SSH Remote Orchestration Flow

```mermaid
sequenceDiagram
    participant AdminConsoleUI
    participant AdminServer
    participant Orchestrator
    participant RemoteSSHExecutor
    participant RemoteNode
    participant CommandHistory
    
    AdminConsoleUI->>AdminServer: POST /api/actions/run
    AdminServer->>Orchestrator: Execute(ActionRequest)
    Orchestrator->>RemoteSSHExecutor: Dial with Key Auth
    RemoteSSHExecutor->>RemoteNode: SSH Connect
    RemoteSSHExecutor->>RemoteNode: Run command (systemctl/journalctl)
    
    loop While command runs
        RemoteNode-->>RemoteSSHExecutor: Stream stdout/stderr
        RemoteSSHExecutor-->>Orchestrator: Chunked output
        Orchestrator-->>AdminServer: Stream to client
        AdminServer-->>AdminConsoleUI: Response Body / WS Events
    end
    
    RemoteNode-->>RemoteSSHExecutor: Command Exit Code
    RemoteSSHExecutor-->>Orchestrator: Return Result
    Orchestrator->>CommandHistory: Record(execution details)
    Orchestrator-->>AdminServer: ActionRecord
    AdminServer-->>AdminConsoleUI: 200 OK (Record)
```
