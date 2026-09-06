package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPerformanceRateCalculation(t *testing.T) {
	node := &NodeState{
		FsID:        "0",
		DisplayID:   1,
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "127.0.0.1:50052",
		Status:      StatusOnline,
		History:     NewRingBuffer(720),
	}

	srv := &AdminServer{
		nodes: map[string]*NodeState{
			"0": node,
		},
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}

	t1 := time.Now().Unix() - 5

	// Push initial snapshot at t1
	m1 := FileserverMetrics{
		BytesWrittenTotal: 10 * 1024 * 1024,
		BytesReadTotal:    20 * 1024 * 1024,
		WriteOpsTotal:     100,
		ReadOpsTotal:      200,
		ErrorsTotal:       5,
	}
	node.History.Push(Snapshot{
		Timestamp: t1,
		Metrics:   m1,
	})

	// Create test HTTP server returning m2 at t2
	// Delta: 10 MiB written in 5s = 2.0 MiB/s
	// Delta: 20 MiB read in 5s = 4.0 MiB/s
	// Delta: 500 write ops in 5s = 100 IOPS
	// Delta: 1000 read ops in 5s = 200 IOPS
	// Delta: 15 errors in 1500 ops = 1.0% error rate
	m2 := FileserverMetrics{
		BytesWrittenTotal: 20 * 1024 * 1024,
		BytesReadTotal:    40 * 1024 * 1024,
		WriteOpsTotal:     600,
		ReadOpsTotal:      1200,
		ErrorsTotal:       20,
	}

	mockFS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(m2)
	}))
	defer mockFS.Close()

	node.MetricsURL = mockFS.URL

	srv.pollNode(node)

	lastSnap, ok := node.History.GetLast()
	if !ok {
		t.Fatalf("expected last snapshot to exist")
	}

	// Verify node rates were computed
	if node.WriteMbps <= 0 {
		t.Errorf("expected positive WriteMbps, got %f", node.WriteMbps)
	}
	if node.ReadMbps <= 0 {
		t.Errorf("expected positive ReadMbps, got %f", node.ReadMbps)
	}
	if node.WriteIOPS <= 0 {
		t.Errorf("expected positive WriteIOPS, got %f", node.WriteIOPS)
	}
	if node.ReadIOPS <= 0 {
		t.Errorf("expected positive ReadIOPS, got %f", node.ReadIOPS)
	}
	if lastSnap.WriteMbps != node.WriteMbps {
		t.Errorf("snapshot WriteMbps (%f) != node WriteMbps (%f)", lastSnap.WriteMbps, node.WriteMbps)
	}
}

func TestHandlePerformance(t *testing.T) {
	node := &NodeState{
		FsID:        "0",
		DisplayID:   1,
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "127.0.0.1:50052",
		Status:      StatusOnline,
		WriteMbps:   5.5,
		ReadMbps:    12.0,
		WriteIOPS:   50.0,
		ReadIOPS:    120.0,
		Metrics: &FileserverMetrics{
			OpLatencyWriteMsP50: 1.2,
			OpLatencyWriteMsP95: 4.5,
			OpLatencyWriteMsP99: 10.0,
			OpLatencyReadMsP50:  0.5,
			OpLatencyReadMsP95:  1.2,
			OpLatencyReadMsP99:  3.0,
			ActiveConnections:   3,
		},
		History: NewRingBuffer(720),
	}

	srv := &AdminServer{
		nodes: map[string]*NodeState{
			"0": node,
		},
		users:      map[string]string{"alice": "0"},
		httpClient: &http.Client{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/performance", nil)
	w := httptest.NewRecorder()

	srv.handlePerformance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp PerformanceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ClusterWriteMbps != 5.5 {
		t.Errorf("expected cluster write mbps 5.5, got %f", resp.ClusterWriteMbps)
	}
	if resp.ClusterReadMbps != 12.0 {
		t.Errorf("expected cluster read mbps 12.0, got %f", resp.ClusterReadMbps)
	}
	if len(resp.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(resp.Nodes))
	}
	if resp.Nodes[0].LatencyWriteP50 != 1.2 {
		t.Errorf("expected LatencyWriteP50=1.2, got %f", resp.Nodes[0].LatencyWriteP50)
	}
}

func TestHandlePerformanceExportCSV(t *testing.T) {
	node := &NodeState{
		FsID:        "0",
		DisplayID:   1,
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "10.7.52.85:50052",
		Status:      StatusOnline,
		History:     NewRingBuffer(720),
	}

	now := time.Now().Unix()
	node.History.Push(Snapshot{
		Timestamp: now,
		Metrics: FileserverMetrics{
			OpLatencyWriteMsP50: 1.1,
			OpLatencyWriteMsP95: 3.2,
			OpLatencyWriteMsP99: 8.5,
			OpLatencyReadMsP50:  0.4,
			OpLatencyReadMsP95:  1.0,
			OpLatencyReadMsP99:  2.5,
			BytesWrittenTotal:   5242880,
			BytesReadTotal:      10485760,
			WriteOpsTotal:       50,
			ReadOpsTotal:        100,
			ErrorsTotal:         1,
			ActiveConnections:   2,
		},
		WriteMbps:    2.5,
		ReadMbps:     5.0,
		WriteIOPS:    25.0,
		ReadIOPS:     50.0,
		ErrorRatePct: 0.67,
	})

	srv := &AdminServer{
		nodes: map[string]*NodeState{
			"0": node,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/performance/export", nil)
	w := httptest.NewRecorder()

	srv.handlePerformanceExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/csv" {
		t.Errorf("expected text/csv, got %s", contentType)
	}

	csvBody := w.Body.String()
	lines := strings.Split(strings.TrimSpace(csvBody), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least header + 1 row, got %d lines: %s", len(lines), csvBody)
	}

	header := lines[0]
	if !strings.Contains(header, "timestamp") || !strings.Contains(header, "write_mbps") || !strings.Contains(header, "latency_write_p50_ms") {
		t.Errorf("unexpected header: %s", header)
	}

	dataRow := lines[1]
	if !strings.Contains(dataRow, "FS-1") || !strings.Contains(dataRow, "dvfs1") || !strings.Contains(dataRow, "2.5000") {
		t.Errorf("unexpected data row: %s", dataRow)
	}
}
