package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// deriveMetricsURL extracts the host and port from a fileserver's gRPC address
// and computes the HTTP metrics URL (port - 41000).
func deriveMetricsURL(address string) string {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return ""
	}
	metricsPort := port - 41000
	return fmt.Sprintf("http://%s:%d/metrics", host, metricsPort)
}

// refreshNodes reads the metaserver state file, updates the user map,
// and registers any new fileservers into the active node pool.
func (a *AdminServer) refreshNodes() {
	state, err := LoadMetaState(a.stateFile)
	if err != nil {
		log.Printf("[ADMIN] Warning: failed to load metaserver state from %s: %v", a.stateFile, err)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Update user mapping
	a.users = make(map[string]string, len(state.Users))
	for user, fsID := range state.Users {
		a.users[user] = strconv.FormatUint(fsID, 10)
	}

	// Update or add fileservers
	for fsID, fsInfo := range state.FileServers {
		metricsURL := deriveMetricsURL(fsInfo.Address)
		displayID := 1
		if num, parseErr := strconv.Atoi(fsID); parseErr == nil {
			displayID = num + 1
		}
		displayName := fmt.Sprintf("FS-%d", displayID)
		machineName := fmt.Sprintf("dvfs%d", displayID)

		if node, exists := a.nodes[fsID]; exists {
			node.Address = fsInfo.Address
			node.MetricsURL = metricsURL
			node.DisplayID = displayID
			node.DisplayName = displayName
			node.MachineName = machineName
		} else {
			a.nodes[fsID] = &NodeState{
				FsID:        fsID,
				DisplayID:   displayID,
				DisplayName: displayName,
				MachineName: machineName,
				Address:     fsInfo.Address,
				MetricsURL:  metricsURL,
				Status:      StatusOffline,
				LastSeen:    0,
				Metrics:     nil,
				History:     NewRingBuffer(720),
			}
			log.Printf("[ADMIN] Discovered fileserver %s (%s / %s) at %s (metrics: %s)", fsID, displayName, machineName, fsInfo.Address, metricsURL)
		}
	}
}

// pollAllNodes triggers concurrent HTTP requests to scrape /metrics on all registered fileservers.
func (a *AdminServer) pollAllNodes() {
	a.mu.RLock()
	nodesCopy := make([]*NodeState, 0, len(a.nodes))
	for _, n := range a.nodes {
		nodesCopy = append(nodesCopy, n)
	}
	a.mu.RUnlock()

	var wg sync.WaitGroup
	for _, node := range nodesCopy {
		wg.Add(1)
		go func(n *NodeState) {
			defer wg.Done()
			a.pollNode(n)
		}(node)
	}
	wg.Wait()
}

// pollNode fetches /metrics from a single node and updates its state and history.
func (a *AdminServer) pollNode(node *NodeState) {
	if node.MetricsURL == "" {
		a.mu.Lock()
		node.Status = StatusOffline
		a.mu.Unlock()
		return
	}

	req, err := http.NewRequest(http.MethodGet, node.MetricsURL, nil)
	if err != nil {
		a.updateNodeFailure(node)
		return
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.updateNodeFailure(node)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		a.updateNodeFailure(node)
		return
	}

	var m FileserverMetrics
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		log.Printf("[ADMIN] Failed to decode metrics from node %s (%s): %v", node.FsID, node.MetricsURL, err)
		a.updateNodeFailure(node)
		return
	}

	now := time.Now().Unix()

	a.mu.Lock()
	node.Metrics = &m
	node.LastSeen = now
	node.Status = computeNodeStatus(&m, now)
	node.History.Push(Snapshot{
		Timestamp: now,
		Metrics:   m,
	})
	a.mu.Unlock()
}

func (a *AdminServer) updateNodeFailure(node *NodeState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	node.Status = computeNodeStatus(node.Metrics, node.LastSeen)
}
