package admin

import (
	"net/http"
	"sync"
	"time"
)

type NodeStatus string

const (
	StatusOnline   NodeStatus = "online"
	StatusWarning  NodeStatus = "warning"
	StatusDegraded NodeStatus = "degraded"
	StatusCritical NodeStatus = "critical"
	StatusOffline  NodeStatus = "offline"
)

// NodeState represents the tracked state and latest telemetry of a single fileserver.
type NodeState struct {
	FsID       string             `json:"fsID"`
	Address    string             `json:"address"`
	MetricsURL string             `json:"metricsURL"`
	Status     NodeStatus         `json:"status"`
	LastSeen   int64              `json:"lastSeen"`
	Metrics    *FileserverMetrics `json:"metrics"`
	History    *RingBuffer        `json:"-"`
}

// AdminServer coordinates fileserver discovery, metrics polling, and serves the REST API + UI.
type AdminServer struct {
	stateFile    string
	staticDir    string
	nodes        map[string]*NodeState // fsID -> NodeState
	users        map[string]string     // username -> fsID string
	mu           sync.RWMutex
	httpClient   *http.Client
	stopCh       chan struct{}
	history      *CommandHistory
	orchestrator *Orchestrator
}

// NewAdminServer creates a new AdminServer instance.
func NewAdminServer(stateFile, staticDir string) *AdminServer {
	srv := &AdminServer{
		stateFile: stateFile,
		staticDir: staticDir,
		nodes:     make(map[string]*NodeState),
		users:     make(map[string]string),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopCh: make(chan struct{}),
	}
	history := NewCommandHistory(100, "./command_history.json")
	ssh := NewRemoteSSHExecutor()
	srv.history = history
	srv.orchestrator = NewOrchestrator(srv, ssh, history, "", "", "")
	return srv
}

// SetOrchestrator configures the orchestration engine (useful for injecting mock SSH executors in tests).
func (a *AdminServer) SetOrchestrator(o *Orchestrator) {
	a.orchestrator = o
}

// Orchestrator returns the orchestration engine.
func (a *AdminServer) Orchestrator() *Orchestrator {
	return a.orchestrator
}

// SetHistory configures the command history storage.
func (a *AdminServer) SetHistory(h *CommandHistory) {
	a.history = h
}

// History returns the command history storage.
func (a *AdminServer) History() *CommandHistory {
	return a.history
}

// computeNodeStatus evaluates the health of a node given its metrics and last seen timestamp.
func computeNodeStatus(m *FileserverMetrics, lastSeen int64) NodeStatus {
	if m == nil || lastSeen == 0 {
		return StatusOffline
	}
	now := time.Now().Unix()
	if now-lastSeen > 30 {
		return StatusOffline
	}
	if m.DiskUsagePercent > 95 || m.CPUTempCelsius > 85 {
		return StatusCritical
	}
	if m.DiskUsagePercent > 90 || m.CPUTempCelsius > 75 {
		return StatusDegraded
	}
	if m.DiskUsagePercent > 80 || m.CPUTempCelsius > 65 {
		return StatusWarning
	}
	return StatusOnline
}
