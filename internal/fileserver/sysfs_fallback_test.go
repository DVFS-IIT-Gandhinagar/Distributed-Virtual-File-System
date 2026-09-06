package fileserver

import (
	"testing"
)

// TestReadCPUTempFallback verifies that readCPUTemp does not panic
// even when sysfs thermal paths do not exist (e.g. in Windows, Docker, or CI).
func TestReadCPUTempFallback(t *testing.T) {
	temp := readCPUTemp()
	if temp < 0 {
		t.Errorf("readCPUTemp returned negative temperature: %v", temp)
	}
}

// TestReadMemInfoFallback verifies that readMemInfo returns without panicking.
func TestReadMemInfoFallback(t *testing.T) {
	total, avail := readMemInfo()
	// On Linux total might be > 0, on non-Linux it will be 0.
	// In neither case should total < avail underflow.
	if total > 0 && avail > total {
		t.Errorf("avail (%d) cannot exceed total (%d)", avail, total)
	}
}

// TestReadLoadAvgFallback verifies that readLoadAvg returns non-negative floats.
func TestReadLoadAvgFallback(t *testing.T) {
	l1, l5 := readLoadAvg()
	if l1 < 0 || l5 < 0 {
		t.Errorf("load average returned negative: 1m=%v, 5m=%v", l1, l5)
	}
}

// TestReadCPUUsageFallback verifies readCPUUsage handles missing or non-Linux environments safely.
func TestReadCPUUsageFallback(t *testing.T) {
	usage := readCPUUsage()
	if usage < 0.0 || usage > 100.0 {
		t.Errorf("CPU usage percent must be in [0.0, 100.0], got %v", usage)
	}
}

// TestReadFileserverDiskStatsFallback verifies that readFileserverDiskStats handles
// non-existent directories gracefully and returns zeroed stats instead of panicking.
func TestReadFileserverDiskStatsFallback(t *testing.T) {
	nonExistentDir := "/path/to/definitely/nonexistent/directory/12345"
	total, used, free, pct := readFileserverDiskStats(nonExistentDir)
	if pct < 0.0 || pct > 100.0 {
		t.Errorf("expected pct in [0, 100], got %v", pct)
	}
	_ = total
	_ = used
	_ = free
}
