package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/client"
)

func TestDeriveMetricsURL(t *testing.T) {
	tests := []struct {
		address  string
		expected string
	}{
		{"10.7.52.85:50052", "http://10.7.52.85:9052/metrics"},
		{"127.0.0.1:50051", "http://127.0.0.1:9051/metrics"},
		{"localhost:50000", "http://localhost:9000/metrics"},
		{"invalid-no-port", ""},
		{"host:abc", ""},
	}

	for _, tt := range tests {
		got := deriveMetricsURL(tt.address)
		if got != tt.expected {
			t.Errorf("deriveMetricsURL(%q) = %q, want %q", tt.address, got, tt.expected)
		}
	}
}

func TestComputeNodeStatus(t *testing.T) {
	now := time.Now().Unix()

	// Nil metrics -> offline
	if st := computeNodeStatus(nil, now); st != StatusOffline {
		t.Errorf("expected StatusOffline for nil metrics, got %s", st)
	}

	// Last seen > 30s ago -> offline
	mNormal := &FileserverMetrics{
		DiskUsagePercent: 50.0,
		CPUTempCelsius:   45.0,
	}
	if st := computeNodeStatus(mNormal, now-35); st != StatusOffline {
		t.Errorf("expected StatusOffline for stale heartbeat, got %s", st)
	}

	// Critical: Disk > 95%
	mCritDisk := &FileserverMetrics{DiskUsagePercent: 96.0, CPUTempCelsius: 45.0}
	if st := computeNodeStatus(mCritDisk, now); st != StatusCritical {
		t.Errorf("expected StatusCritical for disk > 95%%, got %s", st)
	}

	// Critical: Temp > 85
	mCritTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 86.0}
	if st := computeNodeStatus(mCritTemp, now); st != StatusCritical {
		t.Errorf("expected StatusCritical for temp > 85, got %s", st)
	}

	// Degraded: Disk > 90%
	mDegraded := &FileserverMetrics{DiskUsagePercent: 92.0, CPUTempCelsius: 50.0}
	if st := computeNodeStatus(mDegraded, now); st != StatusDegraded {
		t.Errorf("expected StatusDegraded for disk > 90%%, got %s", st)
	}

	// Degraded: Temp > 75
	mDegradedTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 78.0}
	if st := computeNodeStatus(mDegradedTemp, now); st != StatusDegraded {
		t.Errorf("expected StatusDegraded for temp > 75, got %s", st)
	}

	// Warning: Disk > 80%
	mWarnDisk := &FileserverMetrics{DiskUsagePercent: 85.0, CPUTempCelsius: 50.0}
	if st := computeNodeStatus(mWarnDisk, now); st != StatusWarning {
		t.Errorf("expected StatusWarning for disk > 80%%, got %s", st)
	}

	// Warning: Temp > 65
	mWarnTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 68.0}
	if st := computeNodeStatus(mWarnTemp, now); st != StatusWarning {
		t.Errorf("expected StatusWarning for temp > 65, got %s", st)
	}

	// Online: Normal
	if st := computeNodeStatus(mNormal, now); st != StatusOnline {
		t.Errorf("expected StatusOnline for normal stats, got %s", st)
	}
}

func TestPollNode(t *testing.T) {
	mockMetrics := FileserverMetrics{
		DiskTotalBytes:   1000000,
		DiskUsedBytes:    200000,
		DiskUsagePercent: 20.0,
		CPUTempCelsius:   48.0,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockMetrics)
	}))
	defer server.Close()

	admin := NewAdminServer("", "")
	node := &NodeState{
		FsID:       "0",
		Address:    "127.0.0.1:50052",
		MetricsURL: server.URL + "/metrics",
		Status:     StatusOffline,
		History:    NewRingBuffer(10),
	}

	admin.pollNode(node)

	if node.Status != StatusOnline {
		t.Errorf("expected node status Online after poll, got %s", node.Status)
	}
	if node.Metrics == nil || node.Metrics.DiskTotalBytes != 1000000 {
		t.Errorf("unexpected metrics content after poll: %+v", node.Metrics)
	}
	if len(node.History.GetAll()) != 1 {
		t.Errorf("expected 1 history snapshot, got %d", len(node.History.GetAll()))
	}
}

func TestPoller_MachineDiscoveryResolutionAndShiftFix(t *testing.T) {
	// Create mock metaserver state file where dvfs2 was skipped:
	// Node 0 -> 10.0.171.38:50052
	// Node 1 -> 10.0.171.40:50052
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "metaserver_state.json")

	mockState := struct {
		FileServers map[string]struct {
			Address string `json:"address"`
		} `json:"fileservers"`
		Users map[string]uint64 `json:"users"`
	}{
		FileServers: map[string]struct {
			Address string `json:"address"`
		}{
			"0": {Address: "10.0.171.38:50052"},
			"1": {Address: "10.0.171.40:50052"},
		},
		Users: map[string]uint64{"alice": 0, "bob": 1},
	}

	data, err := json.Marshal(mockState)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	admin := NewAdminServer(statePath, "")

	// Set discovery resolver with pre-seeded nodes
	resolver := client.NewDiscoveryResolver()
	resolver.SetNodes([]client.GistNode{
		{Username: "dvfs1", IP: "10.0.171.38"},
		{Username: "dvfs3", IP: "10.0.171.40"},
	})
	admin.SetDiscoveryResolver(resolver)

	admin.refreshNodes()

	admin.mu.RLock()
	defer admin.mu.RUnlock()

	node0, ok0 := admin.nodes["0"]
	if !ok0 {
		t.Fatalf("expected node 0 to be registered")
	}
	if node0.MachineName != "dvfs1" || node0.DisplayName != "FS-1" || node0.DisplayID != 1 {
		t.Errorf("unexpected node 0: name=%s display=%s id=%d", node0.MachineName, node0.DisplayName, node0.DisplayID)
	}

	node1, ok1 := admin.nodes["1"]
	if !ok1 {
		t.Fatalf("expected node 1 to be registered")
	}
	// Crucial check: node 1 should be resolved to dvfs3 / FS-3, NOT dvfs2 / FS-2!
	if node1.MachineName != "dvfs3" {
		t.Errorf("expected node 1 MachineName to be dvfs3, got %s", node1.MachineName)
	}
	if node1.DisplayName != "FS-3" {
		t.Errorf("expected node 1 DisplayName to be FS-3, got %s", node1.DisplayName)
	}
	if node1.DisplayID != 3 {
		t.Errorf("expected node 1 DisplayID to be 3, got %d", node1.DisplayID)
	}
}
