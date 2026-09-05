package admin

import (
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
	Nodes             []*NodeState      `json:"nodes"`
	Users             map[string]string `json:"users"`
	NodeCount         int               `json:"node_count"`
	OnlineCount       int               `json:"online_count"`
	TotalStorageBytes uint64            `json:"total_storage_bytes"`
	UsedStorageBytes  uint64            `json:"used_storage_bytes"`
	TotalUsers        int               `json:"total_users"`
	OnlineUsers       int               `json:"online_users"`
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

	for _, n := range a.nodes {
		nodes = append(nodes, n)
		if n.Status == StatusOnline || n.Status == StatusWarning || n.Status == StatusDegraded || n.Status == StatusCritical {
			onlineCount++
			if n.Metrics != nil {
				for _, u := range n.Metrics.ActiveUsers {
					onlineUsersMap[u] = struct{}{}
				}
			}
		}
		if n.Metrics != nil {
			totalStorage += n.Metrics.DiskTotalBytes
			usedStorage += n.Metrics.DiskUsedBytes
		}
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

	usersCopy := make(map[string]string, len(a.users))
	for u, id := range a.users {
		usersCopy[u] = id
	}

	resp := ClusterResponse{
		Nodes:             nodes,
		Users:             usersCopy,
		NodeCount:         len(nodes),
		OnlineCount:       onlineCount,
		TotalStorageBytes: totalStorage,
		UsedStorageBytes:  usedStorage,
		TotalUsers:        len(usersCopy),
		OnlineUsers:       len(onlineUsersMap),
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

// Run starts the background pollers and the HTTP API/UI server.
func (a *AdminServer) Run(port int) error {
	a.refreshNodes()
	a.pollAllNodes()

	pollTicker := time.NewTicker(5 * time.Second)
	refreshTicker := time.NewTicker(10 * time.Second)

	go func() {
		for {
			select {
			case <-a.stopCh:
				pollTicker.Stop()
				refreshTicker.Stop()
				return
			case <-pollTicker.C:
				a.pollAllNodes()
			case <-refreshTicker.C:
				a.refreshNodes()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster", a.handleCluster)
	mux.HandleFunc("/api/cluster/summary", a.handleCluster)
	mux.HandleFunc("/api/history/", a.handleHistory)
	mux.HandleFunc("/api/users", a.handleUsers)
	mux.HandleFunc("/api/users/", a.handleUserQuota)
	mux.HandleFunc("/api/actions/presets", a.handleActionPresets)
	mux.HandleFunc("/api/actions/history", a.handleActionHistory)
	mux.HandleFunc("/api/actions/execute", a.handleActionExecute)
	if a.orchestrator != nil {
		mux.Handle("/ws/actions", NewWebSocketHandler(a.orchestrator))
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

// Stop shuts down background tickers.
func (a *AdminServer) Stop() {
	select {
	case <-a.stopCh:
	default:
		close(a.stopCh)
	}
}
