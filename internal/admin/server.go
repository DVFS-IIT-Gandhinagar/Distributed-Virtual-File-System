package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/client"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
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
	FsID               string             `json:"fsID"`
	NodeID             string             `json:"nodeID"`
	DisplayID          int                `json:"displayID"`
	DisplayName        string             `json:"displayName"`
	MachineName        string             `json:"machineName"`
	Address            string             `json:"address"`
	MetricsURL         string             `json:"metricsURL"`
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
	store            storage.MetaStore
	staticDir        string
	msAddr           string                // MetaServer gRPC address for administrative commands (e.g. 10.0.171.40:50051)
	nodes            map[string]*NodeState // fsID -> NodeState
	users            map[string]string     // username -> fsID string
	mu               sync.RWMutex
	httpClient       *http.Client
	stopCh           chan struct{}
	history          *CommandHistory
	orchestrator     *Orchestrator
	alertManager     *AlertManager
	snapshotPath     string
	authManager      *AuthManager
	tlsCertFile      string
	tlsKeyFile       string
	isTLS            bool
	resolver         *client.DiscoveryResolver
	removedNodes     map[string]int64 // fsID -> removal timestamp (tombstone)
	removedNodesFile string
	loggedOrphans    map[string]string // username -> unregistered home node already reported in the log
}

// NewAdminServer creates a new AdminServer instance.
func NewAdminServer(store storage.MetaStore, staticDir string) *AdminServer {
	snapshotPath := "./bin/admin_metrics_snapshot.json"
	historyPath := "./bin/command_history.json"
	alertsPath := "./bin/admin_alerts.json"
	removedNodesPath := "./bin/admin_removed_nodes.json"
	if store == nil {
		snapshotPath = ""
		historyPath = ""
		alertsPath = ""
		removedNodesPath = ""
	}

	srv := &AdminServer{
		store:     store,
		staticDir: staticDir,
		nodes:     make(map[string]*NodeState),
		users:     make(map[string]string),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopCh:           make(chan struct{}),
		snapshotPath:     snapshotPath,
		authManager:      NewAuthManager(".env", "../.env", "../../.env"),
		resolver:         client.NewDiscoveryResolver(),
		removedNodes:     make(map[string]int64),
		removedNodesFile: removedNodesPath,
	}
	history := NewCommandHistory(100, historyPath)
	ssh := NewRemoteSSHExecutor()
	srv.history = history
	srv.orchestrator = NewOrchestrator(srv, ssh, history, "", "", "")
	srv.alertManager = NewAlertManager(500, alertsPath)
	_ = srv.LoadMetricsSnapshot(srv.snapshotPath)
	srv.loadRemovedNodes()
	return srv
}

// RecordNodeRemoval flags a node as explicitly removed by admin to prevent resurrection.
func (a *AdminServer) RecordNodeRemoval(fsID string, timestamp int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.removedNodes == nil {
		a.removedNodes = make(map[string]int64)
	}
	a.removedNodes[fsID] = timestamp
	a.saveRemovedNodesLocked()
}

// IsNodeRemoved checks whether a node was removed by admin and has not sent a newer heartbeat since.
func (a *AdminServer) IsNodeRemoved(fsID string, lastHeartbeatUnix int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isNodeRemovedLocked(fsID, lastHeartbeatUnix)
}

func (a *AdminServer) isNodeRemovedLocked(fsID string, lastHeartbeatUnix int64) bool {
	if a.removedNodes == nil {
		return false
	}
	removalTime, removed := a.removedNodes[fsID]
	if !removed {
		return false
	}
	// If the node produced a legitimate heartbeat AFTER removal time, it has been redeployed.
	if lastHeartbeatUnix > removalTime {
		delete(a.removedNodes, fsID)
		a.saveRemovedNodesLocked()
		return false
	}
	return true
}

func (a *AdminServer) saveRemovedNodesLocked() {
	if a.removedNodesFile == "" {
		return
	}
	data, err := json.Marshal(a.removedNodes)
	if err != nil {
		return
	}
	_ = os.WriteFile(a.removedNodesFile, data, 0644)
}

func (a *AdminServer) loadRemovedNodes() {
	if a.removedNodesFile == "" {
		return
	}
	data, err := os.ReadFile(a.removedNodesFile)
	if err != nil {
		return
	}
	var loaded map[string]int64
	if err := json.Unmarshal(data, &loaded); err == nil {
		a.mu.Lock()
		for k, v := range loaded {
			a.removedNodes[k] = v
		}
		a.mu.Unlock()
	}
}

// SetDiscoveryResolver sets the discovery resolver (useful for testing or custom Gist URLs).
func (a *AdminServer) SetDiscoveryResolver(r *client.DiscoveryResolver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resolver = r
}

// DiscoveryResolver returns the discovery resolver.
func (a *AdminServer) DiscoveryResolver() *client.DiscoveryResolver {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.resolver
}

// SetMetaServerAddr configures the metaserver gRPC address for cluster operations.
func (a *AdminServer) SetMetaServerAddr(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.msAddr = addr
}

// MetaServerAddr returns the configured metaserver gRPC address.
func (a *AdminServer) MetaServerAddr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.msAddr
}

// SetAuthManager sets the authentication manager (useful for testing).
func (a *AdminServer) SetAuthManager(am *AuthManager) {
	a.authManager = am
}

// SetTLS configures direct TLS certificates for the admin console.
func (a *AdminServer) SetTLS(certFile, keyFile string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tlsCertFile = certFile
	a.tlsKeyFile = keyFile
	a.isTLS = true
}

// IsTLS reports whether direct TLS is active on the admin server.
func (a *AdminServer) IsTLS() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.isTLS
}

// AuthManager returns the current authentication manager.
func (a *AdminServer) AuthManager() *AuthManager {
	return a.authManager
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
