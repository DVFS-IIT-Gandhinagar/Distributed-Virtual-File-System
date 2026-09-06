package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ClusterResponse struct {
	Nodes               []*NodeState      `json:"nodes"`
	Users               map[string]string `json:"users"`
	NodeCount           int               `json:"node_count"`
	OnlineCount         int               `json:"online_count"`
	TotalStorageBytes   uint64            `json:"total_storage_bytes"`
	UsedStorageBytes    uint64            `json:"used_storage_bytes"`
	TotalUsers          int               `json:"total_users"`
	OnlineUsers         int               `json:"online_users"`
	ClusterWriteMbps    float64           `json:"cluster_write_mbps"`
	ClusterReadMbps     float64           `json:"cluster_read_mbps"`
	ClusterWriteIOPS    float64           `json:"cluster_write_iops"`
	ClusterReadIOPS     float64           `json:"cluster_read_iops"`
	ClusterErrorRatePct float64           `json:"cluster_error_rate_pct"`
}

func (a *AdminServer) handleCluster(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	a.mu.RLock()
	defer a.mu.RUnlock()

	nodes := make([]*NodeState, 0, len(a.nodes))
	onlineCount := 0
	var totalStorage uint64
	var usedStorage uint64
	onlineUsersMap := make(map[string]struct{})

	var clusterWriteMbps float64
	var clusterReadMbps float64
	var clusterWriteIOPS float64
	var clusterReadIOPS float64
	var totalErrorOps float64
	var totalOps float64

	for _, n := range a.nodes {
		nodes = append(nodes, n)
		if n.Status == StatusOnline || n.Status == StatusWarning || n.Status == StatusDegraded || n.Status == StatusCritical {
			onlineCount++
			if n.Metrics != nil {
				for _, u := range n.Metrics.ActiveUsers {
					onlineUsersMap[u] = struct{}{}
				}
			}
			clusterWriteMbps += n.WriteMbps
			clusterReadMbps += n.ReadMbps
			clusterWriteIOPS += n.WriteIOPS
			clusterReadIOPS += n.ReadIOPS
			nodeOps := n.WriteIOPS + n.ReadIOPS
			totalOps += nodeOps
			totalErrorOps += nodeOps * (n.ErrorRatePct / 100.0)
		}
		if n.Metrics != nil {
			totalStorage += n.Metrics.DiskTotalBytes
			usedStorage += n.Metrics.DiskUsedBytes
		}
	}

	var clusterErrorRatePct float64
	if totalOps > 0 {
		clusterErrorRatePct = (totalErrorOps / totalOps) * 100.0
	}

	// Deterministically sort nodes by numerical ID (0, 1, ... 8)
	sort.Slice(nodes, func(i, j int) bool {
		id1, err1 := strconv.Atoi(nodes[i].FsID)
		id2, err2 := strconv.Atoi(nodes[j].FsID)
		if err1 == nil && err2 == nil {
			return id1 < id2
		}
		return nodes[i].FsID < nodes[j].FsID
	})

	isAuthed := true
	if a.authManager != nil {
		isAuthed = a.authManager.IsAuthenticated(r)
	}

	var usersCopy map[string]string
	var totalUsersCount int
	var onlineUsersCount int
	var outputNodes []*NodeState

	if isAuthed {
		usersCopy = make(map[string]string, len(a.users))
		for u, id := range a.users {
			usersCopy[u] = id
		}
		totalUsersCount = len(usersCopy)
		onlineUsersCount = len(onlineUsersMap)
		outputNodes = nodes
	} else {
		usersCopy = make(map[string]string)
		totalUsersCount = 0
		onlineUsersCount = 0
		outputNodes = make([]*NodeState, len(nodes))
		for i, n := range nodes {
			nodeCopy := *n
			if n.Metrics != nil {
				mCopy := *n.Metrics
				mCopy.PerUserStorage = make(map[string]uint64)
				mCopy.PerUserQuota = make(map[string]uint64)
				mCopy.ActiveUsers = []string{}
				mCopy.UsersAssigned = 0
				nodeCopy.Metrics = &mCopy
			}
			outputNodes[i] = &nodeCopy
		}
	}

	resp := ClusterResponse{
		Nodes:               outputNodes,
		Users:               usersCopy,
		NodeCount:           len(nodes),
		OnlineCount:         onlineCount,
		TotalStorageBytes:   totalStorage,
		UsedStorageBytes:    usedStorage,
		TotalUsers:          totalUsersCount,
		OnlineUsers:         onlineUsersCount,
		ClusterWriteMbps:    clusterWriteMbps,
		ClusterReadMbps:     clusterReadMbps,
		ClusterWriteIOPS:    clusterWriteIOPS,
		ClusterReadIOPS:     clusterReadIOPS,
		ClusterErrorRatePct: clusterErrorRatePct,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[ADMIN] handleCluster encode error: %v", err)
	}
}

func (a *AdminServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	fsID := strings.TrimPrefix(r.URL.Path, "/api/history/")
	fsID = strings.TrimSpace(fsID)

	if fsID == "cluster" || fsID == "all" || fsID == "" {
		a.mu.RLock()
		clusterHistory := a.aggregateClusterHistoryLocked()
		a.mu.RUnlock()
		if err := json.NewEncoder(w).Encode(clusterHistory); err != nil {
			log.Printf("[ADMIN] handleHistory cluster encode error: %v", err)
		}
		return
	}

	a.mu.RLock()
	node, exists := a.nodes[fsID]
	if !exists {
		for _, n := range a.nodes {
			if strings.EqualFold(n.DisplayName, fsID) || fmt.Sprintf("%d", n.DisplayID) == fsID {
				node = n
				exists = true
				break
			}
		}
	}
	a.mu.RUnlock()

	if !exists || node == nil || node.History == nil {
		http.NotFound(w, r)
		return
	}

	history := node.History.GetAll()
	if err := json.NewEncoder(w).Encode(history); err != nil {
		log.Printf("[ADMIN] handleHistory encode error: %v", err)
	}
}

type spaHandler struct {
	staticDir string
	fs        http.Handler
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	relPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), string(filepath.Separator))
	fullPath := filepath.Join(h.staticDir, relPath)
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() {
		indexPath := filepath.Join(h.staticDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
	}

	h.fs.ServeHTTP(w, r)
}

type NodeUserStorage struct {
	FsID        string `json:"fs_id"`
	DisplayID   int    `json:"display_id"`
	DisplayName string `json:"display_name"`
	MachineName string `json:"machine_name"`
	Address     string `json:"address"`
	UsedBytes   uint64 `json:"used_bytes"`
	QuotaBytes  uint64 `json:"quota_bytes"`
}

type UserSummary struct {
	Username       string            `json:"username"`
	HomeFsID       string            `json:"home_fs_id"`
	HomeFsDisplay  string            `json:"home_fs_display"`
	HomeFsMachine  string            `json:"home_fs_machine"`
	HomeFsAddress  string            `json:"home_fs_address"`
	QuotaLimit     uint64            `json:"quota_limit"`
	QuotaUsed      uint64            `json:"quota_used"`
	UsagePercent   float64           `json:"usage_percent"`
	ActiveSessions int               `json:"active_sessions"`
	IsOnline       bool              `json:"is_online"`
	Nodes          []NodeUserStorage `json:"nodes"`
}

type SetQuotaPayload struct {
	QuotaBytes uint64 `json:"quota_bytes"`
}

func (a *AdminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	a.mu.RLock()
	defer a.mu.RUnlock()

	userList := make([]UserSummary, 0, len(a.users))
	for username, homeFsID := range a.users {
		homeDisplayID := 1
		if num, parseErr := strconv.Atoi(homeFsID); parseErr == nil {
			homeDisplayID = num + 1
		}
		summary := UserSummary{
			Username:      username,
			HomeFsID:      homeFsID,
			HomeFsDisplay: fmt.Sprintf("FS-%d", homeDisplayID),
			HomeFsMachine: fmt.Sprintf("dvfs%d", homeDisplayID),
			QuotaLimit:    1024 * 1024 * 1024, // 1 GB default
			Nodes:         make([]NodeUserStorage, 0),
		}

		if homeNode, exists := a.nodes[homeFsID]; exists {
			summary.HomeFsAddress = homeNode.Address
			if homeNode.DisplayName != "" {
				summary.HomeFsDisplay = homeNode.DisplayName
			}
			if homeNode.MachineName != "" {
				summary.HomeFsMachine = homeNode.MachineName
			}
			if homeNode.Metrics != nil {
				if q, ok := homeNode.Metrics.PerUserQuota[username]; ok && q > 0 {
					summary.QuotaLimit = q
				}
			}
		}

		var totalUsed uint64
		var activeSessions int
		for fsID, node := range a.nodes {
			if node.Metrics != nil {
				used, hasUsed := node.Metrics.PerUserStorage[username]
				quota, hasQuota := node.Metrics.PerUserQuota[username]
				if hasUsed || hasQuota {
					nodeDisplayID := 1
					if num, parseErr := strconv.Atoi(fsID); parseErr == nil {
						nodeDisplayID = num + 1
					}
					nodeDisplayName := node.DisplayName
					if nodeDisplayName == "" {
						nodeDisplayName = fmt.Sprintf("FS-%d", nodeDisplayID)
					}
					nodeMachineName := node.MachineName
					if nodeMachineName == "" {
						nodeMachineName = fmt.Sprintf("dvfs%d", nodeDisplayID)
					}

					summary.Nodes = append(summary.Nodes, NodeUserStorage{
						FsID:        fsID,
						DisplayID:   nodeDisplayID,
						DisplayName: nodeDisplayName,
						MachineName: nodeMachineName,
						Address:     node.Address,
						UsedBytes:   used,
						QuotaBytes:  quota,
					})
					totalUsed += used
				}

				// Check active sessions on online nodes
				if node.Status == StatusOnline || node.Status == StatusWarning || node.Status == StatusDegraded || node.Status == StatusCritical {
					for _, activeUser := range node.Metrics.ActiveUsers {
						if activeUser == username {
							activeSessions++
						}
					}
				}
			}
		}

		summary.QuotaUsed = totalUsed
		summary.ActiveSessions = activeSessions
		summary.IsOnline = (activeSessions > 0)
		if summary.QuotaLimit > 0 {
			summary.UsagePercent = float64(summary.QuotaUsed) / float64(summary.QuotaLimit) * 100.0
		}

		// Sort user's nodes list deterministically by numerical ID
		sort.Slice(summary.Nodes, func(i, j int) bool {
			id1, err1 := strconv.Atoi(summary.Nodes[i].FsID)
			id2, err2 := strconv.Atoi(summary.Nodes[j].FsID)
			if err1 == nil && err2 == nil {
				return id1 < id2
			}
			return summary.Nodes[i].FsID < summary.Nodes[j].FsID
		})

		userList = append(userList, summary)
	}

	// Sort users alphabetically
	sort.Slice(userList, func(i, j int) bool {
		return userList[i].Username < userList[j].Username
	})

	if err := json.NewEncoder(w).Encode(userList); err != nil {
		log.Printf("[ADMIN] handleUsers encode error: %v", err)
	}
}

func (a *AdminServer) handleUserQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Parse /api/users/{username}/quota
	path := strings.TrimPrefix(r.URL.Path, "/api/users/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "quota" || parts[0] == "" {
		http.Error(w, `{"error":"invalid URL path, expected /api/users/{username}/quota"}`, http.StatusBadRequest)
		return
	}
	username := parts[0]

	var payload SetQuotaPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if payload.QuotaBytes == 0 {
		http.Error(w, `{"error":"quota_bytes must be greater than 0"}`, http.StatusBadRequest)
		return
	}

	a.mu.RLock()
	homeFsID, exists := a.users[username]
	if !exists {
		a.mu.RUnlock()
		http.Error(w, fmt.Sprintf(`{"error":"user '%s' not found"}`, username), http.StatusNotFound)
		return
	}

	homeNode, nodeExists := a.nodes[homeFsID]
	if !nodeExists || homeNode.Address == "" {
		a.mu.RUnlock()
		http.Error(w, fmt.Sprintf(`{"error":"home fileserver for user '%s' not found"}`, username), http.StatusBadGateway)
		return
	}
	nodeAddr := homeNode.Address
	a.mu.RUnlock()

	// Invoke SetQuota RPC on fileserver
	if err := a.CallSetQuota(nodeAddr, username, payload.QuotaBytes); err != nil {
		log.Printf("[ADMIN] CallSetQuota error for user %s on %s: %v", username, nodeAddr, err)
		http.Error(w, fmt.Sprintf(`{"error":"failed to update quota: %v"}`, err), http.StatusBadGateway)
		return
	}

	// Update cached metrics immediately
	a.mu.Lock()
	if node, ok := a.nodes[homeFsID]; ok && node.Metrics != nil {
		if node.Metrics.PerUserQuota == nil {
			node.Metrics.PerUserQuota = make(map[string]uint64)
		}
		node.Metrics.PerUserQuota[username] = payload.QuotaBytes
	}
	a.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"username":    username,
		"quota_bytes": payload.QuotaBytes,
	})
}

// handleActionPresets returns pre-filled restart parameters for all cluster nodes.
func (a *AdminServer) handleActionPresets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if a.orchestrator == nil {
		http.Error(w, `{"error":"orchestrator not initialized"}`, http.StatusInternalServerError)
		return
	}
	presets := a.orchestrator.GetPresets()
	_ = json.NewEncoder(w).Encode(presets)
}

// handleActionHistory returns the recent bounded command execution records.
func (a *AdminServer) handleActionHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if a.history == nil {
		_ = json.NewEncoder(w).Encode([]CommandRecord{})
		return
	}
	history := a.history.GetAll()
	_ = json.NewEncoder(w).Encode(history)
}

// handleActionExecute allows headless REST invocation of orchestration commands.
func (a *AdminServer) handleActionExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if a.orchestrator == nil {
		http.Error(w, `{"error":"orchestrator not initialized"}`, http.StatusInternalServerError)
		return
	}

	var req ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid request: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	record, err := a.orchestrator.Execute(r.Context(), req, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"execution error: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(record)
}

// NodePerformance holds current throughput, IOPS, and latency stats for a fileserver node.
type NodePerformance struct {
	FsID              string  `json:"fsID"`
	DisplayID         int     `json:"display_id"`
	DisplayName       string  `json:"display_name"`
	MachineName       string  `json:"machine_name"`
	Address           string  `json:"address"`
	Status            string  `json:"status"`
	WriteMbps         float64 `json:"write_mbps"`
	ReadMbps          float64 `json:"read_mbps"`
	WriteIOPS         float64 `json:"write_iops"`
	ReadIOPS          float64 `json:"read_iops"`
	ErrorRatePct      float64 `json:"error_rate_pct"`
	LatencyWriteP50   float64 `json:"latency_write_p50"`
	LatencyWriteP95   float64 `json:"latency_write_p95"`
	LatencyWriteP99   float64 `json:"latency_write_p99"`
	LatencyReadP50    float64 `json:"latency_read_p50"`
	LatencyReadP95    float64 `json:"latency_read_p95"`
	LatencyReadP99    float64 `json:"latency_read_p99"`
	ActiveConnections int     `json:"active_connections"`
}

// PerformanceResponse summarizes cluster and per-node throughput, IOPS, and latencies.
type PerformanceResponse struct {
	ClusterWriteMbps    float64           `json:"cluster_write_mbps"`
	ClusterReadMbps     float64           `json:"cluster_read_mbps"`
	ClusterWriteIOPS    float64           `json:"cluster_write_iops"`
	ClusterReadIOPS     float64           `json:"cluster_read_iops"`
	ClusterErrorRatePct float64           `json:"cluster_error_rate_pct"`
	Nodes               []NodePerformance `json:"nodes"`
}

func (a *AdminServer) handlePerformance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	a.mu.RLock()
	defer a.mu.RUnlock()

	var clusterWriteMbps float64
	var clusterReadMbps float64
	var clusterWriteIOPS float64
	var clusterReadIOPS float64
	var totalErrorOps float64
	var totalOps float64

	perfNodes := make([]NodePerformance, 0, len(a.nodes))
	for _, n := range a.nodes {
		var wp50, wp95, wp99 float64
		var rp50, rp95, rp99 float64
		activeConns := 0
		if n.Metrics != nil {
			wp50 = n.Metrics.OpLatencyWriteMsP50
			wp95 = n.Metrics.OpLatencyWriteMsP95
			wp99 = n.Metrics.OpLatencyWriteMsP99
			rp50 = n.Metrics.OpLatencyReadMsP50
			rp95 = n.Metrics.OpLatencyReadMsP95
			rp99 = n.Metrics.OpLatencyReadMsP99
			activeConns = n.Metrics.ActiveConnections
		}

		if n.Status == StatusOnline || n.Status == StatusWarning || n.Status == StatusDegraded || n.Status == StatusCritical {
			clusterWriteMbps += n.WriteMbps
			clusterReadMbps += n.ReadMbps
			clusterWriteIOPS += n.WriteIOPS
			clusterReadIOPS += n.ReadIOPS
			nodeOps := n.WriteIOPS + n.ReadIOPS
			totalOps += nodeOps
			totalErrorOps += nodeOps * (n.ErrorRatePct / 100.0)
		}

		perfNodes = append(perfNodes, NodePerformance{
			FsID:              n.FsID,
			DisplayID:         n.DisplayID,
			DisplayName:       n.DisplayName,
			MachineName:       n.MachineName,
			Address:           n.Address,
			Status:            string(n.Status),
			WriteMbps:         n.WriteMbps,
			ReadMbps:          n.ReadMbps,
			WriteIOPS:         n.WriteIOPS,
			ReadIOPS:          n.ReadIOPS,
			ErrorRatePct:      n.ErrorRatePct,
			LatencyWriteP50:   wp50,
			LatencyWriteP95:   wp95,
			LatencyWriteP99:   wp99,
			LatencyReadP50:    rp50,
			LatencyReadP95:    rp95,
			LatencyReadP99:    rp99,
			ActiveConnections: activeConns,
		})
	}

	sort.Slice(perfNodes, func(i, j int) bool {
		id1, err1 := strconv.Atoi(perfNodes[i].FsID)
		id2, err2 := strconv.Atoi(perfNodes[j].FsID)
		if err1 == nil && err2 == nil {
			return id1 < id2
		}
		return perfNodes[i].FsID < perfNodes[j].FsID
	})

	var clusterErrorRatePct float64
	if totalOps > 0 {
		clusterErrorRatePct = (totalErrorOps / totalOps) * 100.0
	}

	resp := PerformanceResponse{
		ClusterWriteMbps:    clusterWriteMbps,
		ClusterReadMbps:     clusterReadMbps,
		ClusterWriteIOPS:    clusterWriteIOPS,
		ClusterReadIOPS:     clusterReadIOPS,
		ClusterErrorRatePct: clusterErrorRatePct,
		Nodes:               perfNodes,
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[ADMIN] handlePerformance encode error: %v", err)
	}
}

func (a *AdminServer) handlePerformanceExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	targetNodeID := r.URL.Query().Get("node_id")

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	filename := fmt.Sprintf("dvfs_performance_%d.csv", time.Now().Unix())
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	a.mu.RLock()
	var exportNodes []*NodeState
	for _, n := range a.nodes {
		if targetNodeID != "" && n.FsID != targetNodeID {
			continue
		}
		exportNodes = append(exportNodes, n)
	}

	sort.Slice(exportNodes, func(i, j int) bool {
		id1, err1 := strconv.Atoi(exportNodes[i].FsID)
		id2, err2 := strconv.Atoi(exportNodes[j].FsID)
		if err1 == nil && err2 == nil {
			return id1 < id2
		}
		return exportNodes[i].FsID < exportNodes[j].FsID
	})

	type rowItem struct {
		node *NodeState
		snap Snapshot
	}
	var allRows []rowItem
	for _, n := range exportNodes {
		snaps := n.History.GetAll()
		for _, s := range snaps {
			allRows = append(allRows, rowItem{node: n, snap: s})
		}
	}
	a.mu.RUnlock()

	sort.Slice(allRows, func(i, j int) bool {
		if allRows[i].snap.Timestamp == allRows[j].snap.Timestamp {
			return allRows[i].node.FsID < allRows[j].node.FsID
		}
		return allRows[i].snap.Timestamp < allRows[j].snap.Timestamp
	})

	writer := csv.NewWriter(w)
	defer writer.Flush()

	_ = writer.Write([]string{
		"timestamp",
		"iso_time",
		"node_id",
		"node_display",
		"machine",
		"address",
		"write_mbps",
		"read_mbps",
		"write_iops",
		"read_iops",
		"error_rate_pct",
		"latency_write_p50_ms",
		"latency_write_p95_ms",
		"latency_write_p99_ms",
		"latency_read_p50_ms",
		"latency_read_p95_ms",
		"latency_read_p99_ms",
		"bytes_written_total",
		"bytes_read_total",
		"write_ops_total",
		"read_ops_total",
		"errors_total",
		"active_connections",
	})

	for _, item := range allRows {
		s := item.snap
		m := s.Metrics
		isoTime := time.Unix(s.Timestamp, 0).UTC().Format(time.RFC3339)
		_ = writer.Write([]string{
			strconv.FormatInt(s.Timestamp, 10),
			isoTime,
			item.node.FsID,
			item.node.DisplayName,
			item.node.MachineName,
			item.node.Address,
			fmt.Sprintf("%.4f", s.WriteMbps),
			fmt.Sprintf("%.4f", s.ReadMbps),
			fmt.Sprintf("%.2f", s.WriteIOPS),
			fmt.Sprintf("%.2f", s.ReadIOPS),
			fmt.Sprintf("%.2f", s.ErrorRatePct),
			fmt.Sprintf("%.2f", m.OpLatencyWriteMsP50),
			fmt.Sprintf("%.2f", m.OpLatencyWriteMsP95),
			fmt.Sprintf("%.2f", m.OpLatencyWriteMsP99),
			fmt.Sprintf("%.2f", m.OpLatencyReadMsP50),
			fmt.Sprintf("%.2f", m.OpLatencyReadMsP95),
			fmt.Sprintf("%.2f", m.OpLatencyReadMsP99),
			strconv.FormatUint(m.BytesWrittenTotal, 10),
			strconv.FormatUint(m.BytesReadTotal, 10),
			strconv.FormatUint(m.WriteOpsTotal, 10),
			strconv.FormatUint(m.ReadOpsTotal, 10),
			strconv.FormatUint(m.ErrorsTotal, 10),
			strconv.Itoa(m.ActiveConnections),
		})
	}
}

func (a *AdminServer) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if a.alertManager == nil {
		_ = json.NewEncoder(w).Encode([]Alert{})
		return
	}

	q := r.URL.Query()
	f := AlertFilters{
		Severity:   q.Get("severity"),
		NodeID:     q.Get("node_id"),
		Unresolved: q.Get("unresolved") == "true",
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			f.Limit = limit
		}
	}

	alerts := a.alertManager.GetAll(f)
	if alerts == nil {
		alerts = []Alert{}
	}
	if a.authManager != nil && !a.authManager.IsAuthenticated(r) {
		sanitized := make([]Alert, 0, len(alerts))
		for _, al := range alerts {
			if al.Type == AlertTypeQuotaExceeded {
				continue
			}
			al.Username = ""
			sanitized = append(sanitized, al)
		}
		alerts = sanitized
	}
	if err := json.NewEncoder(w).Encode(alerts); err != nil {
		log.Printf("[ADMIN] handleAlerts encode error: %v", err)
	}
}

func (a *AdminServer) handleAlertSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if a.alertManager == nil {
		_ = json.NewEncoder(w).Encode(AlertSummary{})
		return
	}

	summary := a.alertManager.Summary()
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		log.Printf("[ADMIN] handleAlertSummary encode error: %v", err)
	}
}

type resolveAlertPayload struct {
	ID string `json:"id"`
}

func (a *AdminServer) handleResolveAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if a.alertManager == nil {
		http.Error(w, "alert manager not configured", http.StatusInternalServerError)
		return
	}

	var payload resolveAlertPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ID == "" {
		payload.ID = r.URL.Query().Get("id")
	}
	if payload.ID == "" {
		http.Error(w, "missing alert id", http.StatusBadRequest)
		return
	}

	success := a.alertManager.ResolveAlert(payload.ID)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": success,
		"id":      payload.ID,
	})
}

func (a *AdminServer) handleResolveAllAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if a.alertManager == nil {
		http.Error(w, "alert manager not configured", http.StatusInternalServerError)
		return
	}

	count := a.alertManager.ResolveAll()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"resolved_count": count,
	})
}

func (a *AdminServer) handleLogTail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	q := r.URL.Query()
	nodeID := q.Get("node")
	if nodeID == "" {
		nodeID = q.Get("node_id")
	}
	if nodeID == "" {
		a.mu.RLock()
		for id := range a.nodes {
			nodeID = id
			break
		}
		a.mu.RUnlock()
	}
	if nodeID == "" {
		http.Error(w, "no nodes available", http.StatusNotFound)
		return
	}

	lines := 100
	if lStr := q.Get("lines"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			lines = l
		}
	}
	service := q.Get("service")
	mode := q.Get("mode")
	user := q.Get("user")
	key := q.Get("key")
	port := q.Get("port")

	resp, err := a.FetchNodeLogs(r.Context(), nodeID, service, lines, mode, user, key, port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[ADMIN] handleLogTail encode error: %v", err)
	}
}

func (a *AdminServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	if a.authManager == nil {
		return next
	}
	return a.authManager.RequireAuth(next)
}

type authLoginRequest struct {
	Password string `json:"password"`
}

type authLoginResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (a *AdminServer) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req authLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(authLoginResponse{
			Success: false,
			Error:   "invalid request body",
		})
		return
	}

	if a.authManager == nil || !a.authManager.VerifyPassword(req.Password) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(authLoginResponse{
			Success: false,
			Error:   "invalid admin password",
		})
		return
	}

	token := a.authManager.CreateSession()
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	_ = json.NewEncoder(w).Encode(authLoginResponse{
		Success: true,
		Token:   token,
	})
}

func (a *AdminServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.authManager != nil {
		token := a.authManager.ExtractToken(r)
		a.authManager.RevokeSession(token)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func (a *AdminServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		return
	}

	authed := false
	if a.authManager != nil {
		authed = a.authManager.IsAuthenticated(r)
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"authenticated": authed,
	})
}

// Run starts the background pollers and the HTTP API/UI server.
func (a *AdminServer) Run(port int) error {
	a.refreshNodes()
	a.pollAllNodes()

	pollTicker := time.NewTicker(5 * time.Second)
	refreshTicker := time.NewTicker(10 * time.Second)
	snapshotTicker := time.NewTicker(60 * time.Second)

	go func() {
		for {
			select {
			case <-a.stopCh:
				pollTicker.Stop()
				refreshTicker.Stop()
				snapshotTicker.Stop()
				return
			case <-pollTicker.C:
				a.pollAllNodes()
			case <-refreshTicker.C:
				a.refreshNodes()
			case <-snapshotTicker.C:
				_ = a.SaveMetricsSnapshot(a.snapshotPath)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", a.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", a.handleAuthLogout)
	mux.HandleFunc("/api/auth/status", a.handleAuthStatus)
	mux.HandleFunc("/api/cluster", a.handleCluster)
	mux.HandleFunc("/api/cluster/summary", a.handleCluster)
	mux.HandleFunc("/api/performance", a.handlePerformance)
	mux.HandleFunc("/api/performance/export", a.handlePerformanceExport)
	mux.HandleFunc("/api/history/", a.handleHistory)
	mux.HandleFunc("/api/users", a.requireAuth(a.handleUsers))
	mux.HandleFunc("/api/users/", a.requireAuth(a.handleUserQuota))
	mux.HandleFunc("/api/actions/presets", a.requireAuth(a.handleActionPresets))
	mux.HandleFunc("/api/actions/history", a.requireAuth(a.handleActionHistory))
	mux.HandleFunc("/api/actions/execute", a.requireAuth(a.handleActionExecute))
	mux.HandleFunc("/api/alerts", a.handleAlerts)
	mux.HandleFunc("/api/alerts/summary", a.handleAlertSummary)
	mux.HandleFunc("/api/alerts/resolve", a.requireAuth(a.handleResolveAlert))
	mux.HandleFunc("/api/alerts/resolve-all", a.requireAuth(a.handleResolveAllAlerts))
	mux.HandleFunc("/api/logs/tail", a.requireAuth(a.handleLogTail))
	if a.orchestrator != nil {
		mux.Handle("/ws/actions", NewWebSocketHandler(a.orchestrator, a.authManager))
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	spa := spaHandler{
		staticDir: a.staticDir,
		fs:        http.FileServer(http.Dir(a.staticDir)),
	}
	mux.Handle("/", spa)

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("[ADMIN] Listening on %s, serving static from %s", addr, a.staticDir)
	return http.ListenAndServe(addr, mux)
}

// Stop shuts down background tickers and flushes metrics snapshot.
func (a *AdminServer) Stop() {
	select {
	case <-a.stopCh:
	default:
		close(a.stopCh)
		_ = a.SaveMetricsSnapshot(a.snapshotPath)
	}
}
