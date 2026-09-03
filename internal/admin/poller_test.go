package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

	// Warning: Disk > 70%
	mWarnDisk := &FileserverMetrics{DiskUsagePercent: 75.0, CPUTempCelsius: 50.0}
	if st := computeNodeStatus(mWarnDisk, now); st != StatusWarning {
		t.Errorf("expected StatusWarning for disk > 70%%, got %s", st)
	}

	// Warning: Temp > 60
	mWarnTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 62.0}
	if st := computeNodeStatus(mWarnTemp, now); st != StatusWarning {
		t.Errorf("expected StatusWarning for temp > 60, got %s", st)
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
