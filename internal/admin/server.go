package admin

import (
	"net/http"
	"sort"
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
	FsID        string             `json:"fsID"`
	DisplayID   int                `json:"displayID"`
	DisplayName string             `json:"displayName"`
	MachineName string             `json:"machineName"`
	Address     string             `json:"address"`
	MetricsURL  string             `json:"metricsURL"`
	Status             NodeStatus         `json:"status"`
	LastSeen           int64              `json:"lastSeen"`
	Metrics            *FileserverMetrics `json:"metrics"`
	WriteThroughputBps float64            `json:"writeThroughputBps"`
	ReadThroughputBps  float64            `json:"readThroughputBps"`
	WriteMbps          float64            `json:"writeMbps"`
	ReadMbps           float64            `json:"readMbps"`
	WriteIOPS          float64            `json:"writeIOPS"`
	ReadIOPS           float64            `json:"readIOPS"`
	ErrorRatePct       float64            `json:"errorRatePct"`
	History            *RingBuffer        `json:"-"`
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
	alertManager *AlertManager
	snapshotPath string
}

// NewAdminServer creates a new AdminServer instance.
func NewAdminServer(stateFile, staticDir string) *AdminServer {
	snapshotPath := "./admin_metrics_snapshot.json"
	historyPath := "./command_history.json"
	alertsPath := "./admin_alerts.json"
	if stateFile == "" {
		snapshotPath = ""
		historyPath = ""
		alertsPath = ""
	}

	srv := &AdminServer{
		stateFile: stateFile,
		staticDir: staticDir,
		nodes:     make(map[string]*NodeState),
		users:     make(map[string]string),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopCh:       make(chan struct{}),
		snapshotPath: snapshotPath,
	}
	history := NewCommandHistory(100, historyPath)
	ssh := NewRemoteSSHExecutor()
	srv.history = history
	srv.orchestrator = NewOrchestrator(srv, ssh, history, "", "", "")
	srv.alertManager = NewAlertManager(500, alertsPath)
	_ = srv.LoadMetricsSnapshot(srv.snapshotPath)
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

// AlertManager returns the alert management engine.
func (a *AdminServer) AlertManager() *AlertManager {
	return a.alertManager
}

// SetAlertManager configures the alert manager (useful in tests).
func (a *AdminServer) SetAlertManager(am *AlertManager) {
	a.alertManager = am
}

// ClusterHistorySnapshot represents an aggregated time slice across all cluster nodes.
type ClusterHistorySnapshot struct {
	Timestamp         int64   `json:"timestamp"`
	WriteMbps         float64 `json:"write_mbps"`
	ReadMbps          float64 `json:"read_mbps"`
	WriteIOPS         float64 `json:"write_iops"`
	ReadIOPS          float64 `json:"read_iops"`
	ActiveConnections int     `json:"active_connections"`
	ErrorRatePct      float64 `json:"error_rate_pct"`
}

// aggregateClusterHistoryLocked merges historical node snapshots into an aggregated cluster timeline.
func (a *AdminServer) aggregateClusterHistoryLocked() []ClusterHistorySnapshot {
	bucketMap := make(map[int64]*ClusterHistorySnapshot)
	for _, node := range a.nodes {
		if node.History == nil {
			continue
		}
		for _, s := range node.History.GetAll() {
			bucket := (s.Timestamp / 5) * 5
			entry, exists := bucketMap[bucket]
			if !exists {
				entry = &ClusterHistorySnapshot{
					Timestamp: bucket,
				}
				bucketMap[bucket] = entry
			}
			entry.WriteMbps += s.WriteMbps
			entry.ReadMbps += s.ReadMbps
			entry.WriteIOPS += s.WriteIOPS
			entry.ReadIOPS += s.ReadIOPS
			entry.ActiveConnections += s.Metrics.ActiveConnections
			if s.ErrorRatePct > entry.ErrorRatePct {
				entry.ErrorRatePct = s.ErrorRatePct
			}
		}
	}

	keys := make([]int64, 0, len(bucketMap))
	for k := range bucketMap {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	result := make([]ClusterHistorySnapshot, 0, len(keys))
	for _, k := range keys {
		result = append(result, *bucketMap[k])
	}
	return result
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
