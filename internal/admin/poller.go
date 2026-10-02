package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
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

// refreshNodes reads cluster membership from the shared metadata store and
// registers any newly-discovered fileservers into the active node pool.
func (a *AdminServer) refreshNodes() {
	if a.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snap, err := a.store.LoadSnapshot(ctx)
	if err != nil {
		log.Printf("[ADMIN] Warning: failed to load cluster state from metadata store: %v", err)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// nodeID -> dense numeric key, so user records (which reference the stable
	// node identity) can be mapped onto the numeric keys the UI uses.
	numericByNodeID := make(map[string]string, len(snap.FileServers))
	for _, rec := range snap.FileServers {
		numericByNodeID[rec.NodeID] = strconv.FormatUint(rec.NumericID, 10)
	}

	a.users = make(map[string]string, len(snap.Users))
	for _, u := range snap.Users {
		numeric, ok := numericByNodeID[u.HomeNodeID]
		if !ok {
			// The account is real and still assigned; its node is just not in the cluster table right now.
			log.Printf("[ADMIN] User %s references unregistered node %q; omitting from the node view", u.Username, u.HomeNodeID)
			continue
		}
		a.users[u.Username] = numeric
	}

	presentIDs := make(map[string]struct{}, len(snap.FileServers))
	for _, rec := range snap.FileServers {
		fsID := strconv.FormatUint(rec.NumericID, 10)
		presentIDs[fsID] = struct{}{}
		// Do not resurrect a node the admin explicitly removed, unless it has
		// heartbeated since: that means it was redeployed.
		if a.isNodeRemovedLocked(fsID, rec.LastHeartbeatUnix) {
			continue
		}
		metricsURL := deriveMetricsURL(rec.Address)
		// Clamp rather than narrowing blindly: int is 32-bit on some builds, and
		// a display id of 0 or a negative number would render as "FS-0" and a
		// machine name matching no real host.
		displayID := 1
		if rec.NumericID < math.MaxInt32 {
			displayID = int(rec.NumericID) + 1
		}
		displayName := fmt.Sprintf("FS-%d", displayID)
		machineName := fmt.Sprintf("dvfs%d", displayID)

		// Check if cluster discovery resolves host IP to an explicit machine name (e.g. dvfs3)
		host, _, splitErr := net.SplitHostPort(rec.Address)
		if splitErr != nil {
			host = rec.Address
		}
		if a.resolver != nil && host != "" {
			if resolved := a.resolver.ResolveServerName(host); resolved != "" && resolved != host && resolved != "localhost" {
				machineName = resolved
				var machineNum int
				if _, scanErr := fmt.Sscanf(resolved, "dvfs%d", &machineNum); scanErr == nil && machineNum > 0 {
					displayID = machineNum
					displayName = fmt.Sprintf("FS-%d", machineNum)
				}
			}
		}

		if node, exists := a.nodes[fsID]; exists {
			node.NodeID = rec.NodeID
			node.Address = rec.Address
			node.MetricsURL = metricsURL
			node.DisplayID = displayID
			node.DisplayName = displayName
			node.MachineName = machineName
		} else {
			a.nodes[fsID] = &NodeState{
				FsID:        fsID,
				NodeID:      rec.NodeID,
				DisplayID:   displayID,
				DisplayName: displayName,
				MachineName: machineName,
				Address:     rec.Address,
				MetricsURL:  metricsURL,
				Status:      StatusOffline,
				LastSeen:    0,
				Metrics:     nil,
				History:     NewRingBuffer(720),
			}
			log.Printf("[ADMIN] Discovered fileserver %s (node=%s, %s / %s) at %s (metrics: %s)",
				fsID, rec.NodeID, displayName, machineName, rec.Address, metricsURL)
		}
	}

	// Prune nodes the admin removed, or that are no longer in the store.
	for fsID := range a.nodes {
		if a.isNodeRemovedLocked(fsID, 0) {
			delete(a.nodes, fsID)
			continue
		}
		if _, exists := presentIDs[fsID]; !exists {
			delete(a.nodes, fsID)
		}
	}

	if a.alertManager != nil {
		for username, homeFsID := range a.users {
			if homeNode, exists := a.nodes[homeFsID]; exists && homeNode.Metrics != nil {
				quota := uint64(16 * 1024 * 1024 * 1024)
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
