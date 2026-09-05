package fileserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCPUTemp_IntelCoretemp(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	tmpDir := t.TempDir()
	hwmonBase := filepath.Join(tmpDir, "hwmon")
	hwmon0 := filepath.Join(hwmonBase, "hwmon0")
	if err := os.MkdirAll(hwmon0, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(hwmon0, "name"), []byte("coretemp\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmon0, "temp1_label"), []byte("Package id 0\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmon0, "temp1_input"), []byte("48250\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	thermalBase := filepath.Join(tmpDir, "thermal")

	// First call probes and caches
	temp := readCPUTempWithBase(hwmonBase, thermalBase)
	if temp != 48.25 {
		t.Errorf("expected 48.25, got %f", temp)
	}

	// Second call uses cached path
	tempCached := readCPUTempWithBase(hwmonBase, thermalBase)
	if tempCached != 48.25 {
		t.Errorf("expected 48.25 from cache, got %f", tempCached)
	}
}

func TestCPUTemp_AMDK10temp(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	tmpDir := t.TempDir()
	hwmonBase := filepath.Join(tmpDir, "hwmon")
	hwmon1 := filepath.Join(hwmonBase, "hwmon1")
	if err := os.MkdirAll(hwmon1, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(hwmon1, "name"), []byte("k10temp\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmon1, "temp1_label"), []byte("Tctl\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmon1, "temp1_input"), []byte("53000\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	thermalBase := filepath.Join(tmpDir, "thermal")

	temp := readCPUTempWithBase(hwmonBase, thermalBase)
	if temp != 53.0 {
		t.Errorf("expected 53.0, got %f", temp)
	}
}

func TestCPUTemp_RPi5MultiZone(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	tmpDir := t.TempDir()
	emptyHwmon := filepath.Join(tmpDir, "hwmon")
	thermalBase := filepath.Join(tmpDir, "thermal")

	// Zone 0 is PMIC
	tz0 := filepath.Join(thermalBase, "thermal_zone0")
	if err := os.MkdirAll(tz0, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tz0, "type"), []byte("rp1_pmic\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tz0, "temp"), []byte("61000\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Zone 1 is Broadcom CPU
	tz1 := filepath.Join(thermalBase, "thermal_zone1")
	if err := os.MkdirAll(tz1, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tz1, "type"), []byte("bcm2712\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tz1, "temp"), []byte("42500\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	temp := readCPUTempWithBase(emptyHwmon, thermalBase)
	if temp != 42.5 {
		t.Errorf("expected 42.5 (from bcm2712 zone 1), got %f", temp)
	}
}

func TestCPUTemp_ThermalZone0Fallback(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	tmpDir := t.TempDir()
	emptyHwmon := filepath.Join(tmpDir, "hwmon")
	thermalBase := filepath.Join(tmpDir, "thermal")

	tz0 := filepath.Join(thermalBase, "thermal_zone0")
	if err := os.MkdirAll(tz0, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tz0, "temp"), []byte("39000\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	temp := readCPUTempWithBase(emptyHwmon, thermalBase)
	if temp != 39.0 {
		t.Errorf("expected 39.0 from thermal_zone0 fallback, got %f", temp)
	}
}

func TestCPUTemp_ParseSensorsJSON(t *testing.T) {
	// Test Intel coretemp JSON format
	intelJSON := []byte(`{
		"coretemp-isa-0000": {
			"Adapter": "ISA adapter",
			"Package id 0": {
				"temp1_input": 45.0,
				"temp1_max": 100.0,
				"temp1_crit": 100.0
			},
			"Core 0": {
				"temp2_input": 43.0,
				"temp2_max": 100.0,
				"temp2_crit": 100.0
			}
		}
	}`)
	temp := parseSensorsJSON(intelJSON)
	if temp != 45.0 {
		t.Errorf("expected 45.0 from Intel JSON, got %f", temp)
	}

	// Test AMD k10temp JSON format
	amdJSON := []byte(`{
		"k10temp-pci-00c3": {
			"Adapter": "PCI adapter",
			"Tctl": {
				"temp1_input": 51.25
			}
		}
	}`)
	temp = parseSensorsJSON(amdJSON)
	if temp != 51.25 {
		t.Errorf("expected 51.25 from AMD JSON, got %f", temp)
	}

	// Test invalid JSON returns 0
	if parseSensorsJSON([]byte("not-json")) != 0 {
		t.Errorf("expected 0 for invalid JSON")
	}

	// Test empty JSON returns 0
	if parseSensorsJSON([]byte("{}")) != 0 {
		t.Errorf("expected 0 for empty JSON")
	}
}

func TestCPUTemp_CacheEvictionOnReadFailure(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	tmpDir := t.TempDir()
	hwmonBase := filepath.Join(tmpDir, "hwmon")
	hwmon0 := filepath.Join(hwmonBase, "hwmon0")
	if err := os.MkdirAll(hwmon0, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hwmon0, "name"), []byte("coretemp\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	tempFile := filepath.Join(hwmon0, "temp1_input")
	if err := os.WriteFile(tempFile, []byte("50000\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	thermalBase := filepath.Join(tmpDir, "thermal")

	// 1. Initial read should succeed and cache the path
	temp := readCPUTempWithBase(hwmonBase, thermalBase)
	if temp != 50.0 {
		t.Errorf("expected 50.0, got %f", temp)
	}

	// 2. Corrupt or delete the file to simulate sensor disconnect / error
	if err := os.Remove(tempFile); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// 3. Next read should reactively evict cache and return 0.0 (no thermal fallback configured)
	tempAfterError := readCPUTempWithBase(hwmonBase, thermalBase)
	if tempAfterError != 0.0 {
		t.Errorf("expected 0.0 after sensor file removal, got %f", tempAfterError)
	}
}

func TestCPUTemp_EmptyDirectory(t *testing.T) {
	resetCPUTempCache()
	defer resetCPUTempCache()

	temp := readCPUTempWithBase("/nonexistent/hwmon", "/nonexistent/thermal")
	if temp != 0.0 {
		t.Errorf("expected 0.0 for nonexistent paths, got %f", temp)
	}
}
