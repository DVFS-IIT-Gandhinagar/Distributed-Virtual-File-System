package admin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadMetaStateCorruptedFile verifies that LoadMetaState returns a clean error
// when given corrupted or non-existent files.
func TestLoadMetaStateCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	corruptFile := filepath.Join(tmpDir, "corrupt_meta.json")

	if err := os.WriteFile(corruptFile, []byte("{not valid json..."), 0644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	state, err := LoadMetaState(corruptFile)
	if err == nil {
		t.Errorf("expected error loading corrupt JSON state file, but got state: %+v", state)
	}

	// Non-existent file
	stateNonExistent, err := LoadMetaState(filepath.Join(tmpDir, "does_not_exist.json"))
	if err == nil {
		t.Errorf("expected error loading non-existent state file, but got: %+v", stateNonExistent)
	}
}

// TestComputeNodeStatusThresholds tests the status calculation state machine across all boundary conditions.
func TestComputeNodeStatusThresholds(t *testing.T) {
	now := time.Now().Unix()

	// 1. Nil metrics -> StatusOffline
	if s := computeNodeStatus(nil, now); s != StatusOffline {
		t.Errorf("expected StatusOffline for nil metrics, got %s", s)
	}

	// 2. Stale heartbeat (> 30s ago) -> StatusOffline
	nominalMetrics := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 45.0}
	if s := computeNodeStatus(nominalMetrics, now-35); s != StatusOffline {
		t.Errorf("expected StatusOffline for lastSeen > 30s ago, got %s", s)
	}

	// 3. Critical Disk (> 95%) -> StatusCritical
	critDisk := &FileserverMetrics{DiskUsagePercent: 96.0, CPUTempCelsius: 50.0}
	if s := computeNodeStatus(critDisk, now); s != StatusCritical {
		t.Errorf("expected StatusCritical for disk > 95%%, got %s", s)
	}

	// 4. Critical Temp (> 85°C) -> StatusCritical
	critTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 86.0}
	if s := computeNodeStatus(critTemp, now); s != StatusCritical {
		t.Errorf("expected StatusCritical for temp > 85°C, got %s", s)
	}

	// 5. Degraded Disk (> 90%) -> StatusDegraded
	degDisk := &FileserverMetrics{DiskUsagePercent: 91.0, CPUTempCelsius: 50.0}
	if s := computeNodeStatus(degDisk, now); s != StatusDegraded {
		t.Errorf("expected StatusDegraded for disk > 90%%, got %s", s)
	}

	// 6. Degraded Temp (> 75°C) -> StatusDegraded
	degTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 76.0}
	if s := computeNodeStatus(degTemp, now); s != StatusDegraded {
		t.Errorf("expected StatusDegraded for temp > 75°C, got %s", s)
	}

	// 7. Warning Disk (> 80%) -> StatusWarning
	warnDisk := &FileserverMetrics{DiskUsagePercent: 82.0, CPUTempCelsius: 50.0}
	if s := computeNodeStatus(warnDisk, now); s != StatusWarning {
		t.Errorf("expected StatusWarning for disk > 80%%, got %s", s)
	}

	// 8. Warning Temp (> 65°C) -> StatusWarning
	warnTemp := &FileserverMetrics{DiskUsagePercent: 50.0, CPUTempCelsius: 66.0}
	if s := computeNodeStatus(warnTemp, now); s != StatusWarning {
		t.Errorf("expected StatusWarning for temp > 65°C, got %s", s)
	}

	// 9. Nominal -> StatusOnline
	if s := computeNodeStatus(nominalMetrics, now); s != StatusOnline {
		t.Errorf("expected StatusOnline for nominal metrics, got %s", s)
	}
}

// TestDeriveMetricsURLMalformed tests port extraction and edge cases for deriveMetricsURL.
func TestDeriveMetricsURLMalformed(t *testing.T) {
	cases := []struct {
		address  string
		expected string
	}{
		{"192.168.1.100:50052", "http://192.168.1.100:9052/metrics"},
		{"", ""},
		{"invalid_no_port", ""},
		{"127.0.0.1:abc", ""},
		{"10.0.0.1:2000", ""}, // 2000 - 41000 is negative!
	}

	for _, tc := range cases {
		got := deriveMetricsURL(tc.address)
		if got != tc.expected {
			t.Errorf("deriveMetricsURL(%q) = %q, expected %q", tc.address, got, tc.expected)
		}
	}
}
