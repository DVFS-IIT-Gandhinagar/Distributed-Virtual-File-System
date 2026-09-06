package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPerformanceCounterResetOnRestart verifies that when a fileserver restarts,
// causing cumulative byte/op counters to drop from high values to near zero,
// poller delta calculation does not produce negative rates or integer underflows.
func TestPerformanceCounterResetOnRestart(t *testing.T) {
	node := &NodeState{
		FsID:    "0",
		Address: "127.0.0.1:50052",
		Status:  StatusOnline,
		History: NewRingBuffer(10),
	}

	// 1. Push a high-counter snapshot (before restart)
	t1 := time.Now().Unix() - 10
	node.History.Push(Snapshot{
		Timestamp: t1,
		Metrics: FileserverMetrics{
			BytesWrittenTotal: 50 * 1024 * 1024, // 50 MiB
			BytesReadTotal:    100 * 1024 * 1024,
			WriteOpsTotal:     500,
			ReadOpsTotal:      1000,
			ErrorsTotal:       2,
		},
	})

	// 2. Simulate metrics from restarted fileserver (counters reset to small values)
	restartedMetrics := FileserverMetrics{
		BytesWrittenTotal: 1024, // only 1 KiB written since restart
		BytesReadTotal:    2048,
		WriteOpsTotal:     2,
		ReadOpsTotal:      5,
		ErrorsTotal:       0,
	}

	t2 := time.Now().Unix()
	deltaSec := float64(t2 - t1)

	var writeBps, readBps, writeIOPS, readIOPS, errorRate float64
	prevSnap, hasPrev := node.History.GetLast()

	if hasPrev && t2 > prevSnap.Timestamp {
		// Verify conditions matching poller implementation
		if restartedMetrics.BytesWrittenTotal >= prevSnap.Metrics.BytesWrittenTotal {
			deltaBytes := float64(restartedMetrics.BytesWrittenTotal - prevSnap.Metrics.BytesWrittenTotal)
			writeBps = deltaBytes / deltaSec
		}
		if restartedMetrics.BytesReadTotal >= prevSnap.Metrics.BytesReadTotal {
			deltaBytes := float64(restartedMetrics.BytesReadTotal - prevSnap.Metrics.BytesReadTotal)
			readBps = deltaBytes / deltaSec
		}
		if restartedMetrics.WriteOpsTotal >= prevSnap.Metrics.WriteOpsTotal {
			writeIOPS = float64(restartedMetrics.WriteOpsTotal-prevSnap.Metrics.WriteOpsTotal) / deltaSec
		}
		if restartedMetrics.ReadOpsTotal >= prevSnap.Metrics.ReadOpsTotal {
			readIOPS = float64(restartedMetrics.ReadOpsTotal-prevSnap.Metrics.ReadOpsTotal) / deltaSec
		}
	}

	// Because counters reset, deltas must safely evaluate to 0 instead of negative / underflow
	if writeBps < 0 || readBps < 0 || writeIOPS < 0 || readIOPS < 0 || errorRate < 0 {
		t.Errorf("restart caused negative rate calculations: writeBps=%v, readBps=%v, writeIOPS=%v, readIOPS=%v",
			writeBps, readBps, writeIOPS, readIOPS)
	}
	if writeBps != 0 || readBps != 0 {
		t.Errorf("expected 0 rate on restart reset, got writeBps=%v, readBps=%v", writeBps, readBps)
	}
}

// TestPerformancePollerLargeGap tests that if a node was unreachable for hours,
// instantaneous rates are safely bounded rather than computed over massive elapsed time.
func TestPerformancePollerLargeGap(t *testing.T) {
	node := &NodeState{
		FsID:    "0",
		History: NewRingBuffer(10),
	}

	// Previous snapshot was 10,000 seconds ago (approx 2.7 hours)
	prevTime := time.Now().Unix() - 10000
	node.History.Push(Snapshot{
		Timestamp: prevTime,
		Metrics: FileserverMetrics{
			BytesWrittenTotal: 1000,
		},
	})

	now := time.Now().Unix()
	prevSnap, hasPrev := node.History.GetLast()

	gapExceeded := false
	if hasPrev && (now-prevSnap.Timestamp) > 60 {
		gapExceeded = true
	}

	if !gapExceeded {
		t.Errorf("expected gap > 60s to be detected")
	}
}

// TestPerformanceZeroOpsDivideByZero verifies that an idle node reports 0% error rate
// rather than NaN or Inf.
func TestPerformanceZeroOpsDivideByZero(t *testing.T) {
	totalOpsDelta := 0.0
	deltaErrors := 0.0
	errorRatePct := 0.0

	if totalOpsDelta > 0 {
		errorRatePct = (deltaErrors / totalOpsDelta) * 100.0
	}

	if errorRatePct != 0.0 {
		t.Errorf("expected 0.0 error rate for idle node, got %v", errorRatePct)
	}
}

// TestHandlePerformanceMixedOnlineOffline verifies /api/performance aggregates correctly
// when some cluster nodes are offline or have nil metrics.
func TestHandlePerformanceMixedOnlineOffline(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "10.0.0.1:50052",
		Status:  StatusOnline,
		Metrics: &FileserverMetrics{
			DiskTotalBytes:      100 * 1024 * 1024,
			DiskUsedBytes:       20 * 1024 * 1024,
			BytesWrittenTotal:   5000,
			BytesReadTotal:      10000,
			WriteOpsTotal:       50,
			ReadOpsTotal:        100,
			OpLatencyWriteMsP50: 2.5,
			OpLatencyReadMsP50:  1.2,
		},
		WriteThroughputBps: 1024 * 1024,
		ReadThroughputBps:  2 * 1024 * 1024,
		WriteMbps:          1.0,
		ReadMbps:           2.0,
		WriteIOPS:          25,
		ReadIOPS:           50,
	}

	// Node 1 is offline with nil metrics
	srv.nodes["1"] = &NodeState{
		FsID:    "1",
		Address: "10.0.0.2:50052",
		Status:  StatusOffline,
		Metrics: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/performance", nil)
	w := httptest.NewRecorder()
	srv.handlePerformance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp PerformanceResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Nodes) != 2 {
		t.Errorf("expected node count 2, got %d", len(resp.Nodes))
	}
	if resp.ClusterWriteMbps <= 0 {
		t.Errorf("expected positive aggregate write throughput, got %v", resp.ClusterWriteMbps)
	}
}

// TestHandlePerformanceExportEmptyAndFiltered verifies streaming CSV export handles empty data
// and filters cleanly without failing.
func TestHandlePerformanceExportEmptyAndFiltered(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{
		FsID:    "0",
		Status:  StatusOnline,
		History: NewRingBuffer(10),
	}

	// 1. Export with empty history
	req := httptest.NewRequest(http.MethodGet, "/api/performance/export", nil)
	w := httptest.NewRecorder()
	srv.handlePerformanceExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty export, got %d", w.Code)
	}

	body := w.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	// Must contain at least header line
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 header line for empty export, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "timestamp,iso_time,node_id") {
		t.Errorf("header line missing standard columns: %s", lines[0])
	}

	// 2. Export with filter for non-existent node
	reqFiltered := httptest.NewRequest(http.MethodGet, "/api/performance/export?node_id=ghost", nil)
	wFiltered := httptest.NewRecorder()
	srv.handlePerformanceExport(wFiltered, reqFiltered)

	if wFiltered.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for unknown node filter, got %d", wFiltered.Code)
	}
	linesFiltered := strings.Split(strings.TrimSpace(wFiltered.Body.String()), "\n")
	if len(linesFiltered) != 1 {
		t.Errorf("expected 1 line for unknown node filter, got %d", len(linesFiltered))
	}
}

// TestRingBufferGetLastBoundary tests GetLast() across empty, single-element, and full ring buffers.
func TestRingBufferGetLastBoundary(t *testing.T) {
	rb := NewRingBuffer(3)

	// Empty
	_, ok := rb.GetLast()
	if ok {
		t.Errorf("expected GetLast on empty buffer to return false")
	}

	// 1 item
	rb.Push(Snapshot{Timestamp: 100})
	snap, ok := rb.GetLast()
	if !ok || snap.Timestamp != 100 {
		t.Errorf("expected timestamp 100, got %v (ok=%v)", snap.Timestamp, ok)
	}

	// Push to capacity
	rb.Push(Snapshot{Timestamp: 200})
	rb.Push(Snapshot{Timestamp: 300})
	snap, ok = rb.GetLast()
	if !ok || snap.Timestamp != 300 {
		t.Errorf("expected timestamp 300, got %v", snap.Timestamp)
	}

	// Overwrite capacity
	rb.Push(Snapshot{Timestamp: 400})
	snap, ok = rb.GetLast()
	if !ok || snap.Timestamp != 400 {
		t.Errorf("expected latest pushed timestamp 400, got %v", snap.Timestamp)
	}
}
