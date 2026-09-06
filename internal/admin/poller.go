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
	if err != nil || port <= 41000 || port > 65535 {
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

	if a.alertManager != nil {
		for username, homeFsID := range a.users {
			if homeNode, exists := a.nodes[homeFsID]; exists && homeNode.Metrics != nil {
				quota := uint64(1024 * 1024 * 1024)
				if q, ok := homeNode.Metrics.PerUserQuota[username]; ok && q > 0 {
					quota = q
				}
				used := homeNode.Metrics.PerUserStorage[username]
				a.alertManager.CheckUserQuota(username, used, quota, homeNode.FsID, homeNode.DisplayName)
			}
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
	var writeThroughputBps, readThroughputBps float64
	var writeMbps, readMbps float64
	var writeIOPS, readIOPS float64
	var errorRatePct float64

	prevSnap, hasPrev := node.History.GetLast()
	if hasPrev && now > prevSnap.Timestamp && (now-prevSnap.Timestamp) <= 60 {
		deltaSec := float64(now - prevSnap.Timestamp)
		if m.BytesWrittenTotal >= prevSnap.Metrics.BytesWrittenTotal {
			deltaBytesWritten := float64(m.BytesWrittenTotal - prevSnap.Metrics.BytesWrittenTotal)
			writeThroughputBps = deltaBytesWritten / deltaSec
			writeMbps = deltaBytesWritten / deltaSec / (1024 * 1024)
		}
		if m.BytesReadTotal >= prevSnap.Metrics.BytesReadTotal {
			deltaBytesRead := float64(m.BytesReadTotal - prevSnap.Metrics.BytesReadTotal)
			readThroughputBps = deltaBytesRead / deltaSec
			readMbps = deltaBytesRead / deltaSec / (1024 * 1024)
		}
		if m.WriteOpsTotal >= prevSnap.Metrics.WriteOpsTotal {
			deltaWriteOps := float64(m.WriteOpsTotal - prevSnap.Metrics.WriteOpsTotal)
			writeIOPS = deltaWriteOps / deltaSec
		}
		if m.ReadOpsTotal >= prevSnap.Metrics.ReadOpsTotal {
			deltaReadOps := float64(m.ReadOpsTotal - prevSnap.Metrics.ReadOpsTotal)
			readIOPS = deltaReadOps / deltaSec
		}
		totalOpsDelta := 0.0
		if m.WriteOpsTotal >= prevSnap.Metrics.WriteOpsTotal && m.ReadOpsTotal >= prevSnap.Metrics.ReadOpsTotal {
			totalOpsDelta = float64((m.WriteOpsTotal - prevSnap.Metrics.WriteOpsTotal) + (m.ReadOpsTotal - prevSnap.Metrics.ReadOpsTotal))
		}
		if totalOpsDelta > 0 && m.ErrorsTotal >= prevSnap.Metrics.ErrorsTotal {
			deltaErrors := float64(m.ErrorsTotal - prevSnap.Metrics.ErrorsTotal)
			errorRatePct = (deltaErrors / totalOpsDelta) * 100.0
		}
	}

	snap := Snapshot{
		Timestamp:          now,
		Metrics:            m,
		WriteThroughputBps: writeThroughputBps,
		ReadThroughputBps:  readThroughputBps,
		WriteMbps:          writeMbps,
		ReadMbps:           readMbps,
		WriteIOPS:          writeIOPS,
		ReadIOPS:           readIOPS,
		ErrorRatePct:       errorRatePct,
	}

	prevLastSeen := node.LastSeen
	var prevUptime float64
	if node.Metrics != nil {
		prevUptime = node.Metrics.UptimeSeconds
	}

	node.Metrics = &m
	node.LastSeen = now
	node.Status = computeNodeStatus(&m, now)
	node.WriteThroughputBps = writeThroughputBps
	node.ReadThroughputBps = readThroughputBps
	node.WriteMbps = writeMbps
	node.ReadMbps = readMbps
	node.WriteIOPS = writeIOPS
	node.ReadIOPS = readIOPS
	node.ErrorRatePct = errorRatePct
	node.History.Push(snap)

	if a.alertManager != nil {
		a.alertManager.CheckNodeHealth(node, prevLastSeen, prevUptime)
	}
	a.mu.Unlock()
}

func (a *AdminServer) updateNodeFailure(node *NodeState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	prevLastSeen := node.LastSeen
	node.Status = computeNodeStatus(node.Metrics, node.LastSeen)
	node.WriteThroughputBps = 0
	node.ReadThroughputBps = 0
	node.WriteMbps = 0
	node.ReadMbps = 0
	node.WriteIOPS = 0
	node.ReadIOPS = 0
	node.ErrorRatePct = 0

	if a.alertManager != nil {
		a.alertManager.CheckNodeHealth(node, prevLastSeen, 0)
	}
}
