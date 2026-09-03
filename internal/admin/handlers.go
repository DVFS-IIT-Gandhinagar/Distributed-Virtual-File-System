package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	for _, n := range a.nodes {
		nodes = append(nodes, n)
		if n.Status == StatusOnline || n.Status == StatusWarning || n.Status == StatusDegraded || n.Status == StatusCritical {
			onlineCount++
		}
		if n.Metrics != nil {
			totalStorage += n.Metrics.DiskTotalBytes
			usedStorage += n.Metrics.DiskUsedBytes
		}
	}

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
	a.mu.RUnlock()

	if !exists || node.History == nil {
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
	mux.HandleFunc("/api/history/", a.handleHistory)
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
