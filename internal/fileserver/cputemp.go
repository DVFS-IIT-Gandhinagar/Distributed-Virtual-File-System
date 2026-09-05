package fileserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tempSensorCache holds the cached file path pointer to the active CPU temperature sensor.
type tempSensorCache struct {
	path       string
	sourceTier int       // 1 = hwmon (coretemp/k10temp), 2 = thermal_zone, 3 = sensors CLI
	lastProbed time.Time
}

var (
	cpuTempCache   tempSensorCache
	cpuTempCacheMu sync.RWMutex

	// Default sysfs base directories (overridable in tests)
	defaultHwmonBase   = "/sys/class/hwmon"
	defaultThermalBase = "/sys/class/thermal"
)

// resetCPUTempCache cleans the in-memory cache (used for testing or forced resets).
func resetCPUTempCache() {
	cpuTempCacheMu.Lock()
	defer cpuTempCacheMu.Unlock()
	cpuTempCache = tempSensorCache{}
}

// readCPUTemp reads the current real-time CPU temperature in Celsius.
// The temperature value is read live from hardware on every single call.
// The sensor file path pointer is cached for fast-path reads (~10µs),
// with reactive eviction if reading fails or is out-of-bounds, and
// periodic proactive re-probing (10m for Tier 1, 1m for Tier 2/3).
func readCPUTemp() float64 {
	return readCPUTempWithBase(defaultHwmonBase, defaultThermalBase)
}

// readCPUTempWithBase is the parameterized core reader supporting mock sysfs directories.
func readCPUTempWithBase(hwmonBase, thermalBase string) float64 {
	cpuTempCacheMu.RLock()
	cached := cpuTempCache
	cpuTempCacheMu.RUnlock()

	// 1. Check if we have a valid cached path pointer that hasn't exceeded its proactive TTL
	if cached.path != "" {
		ttl := 10 * time.Minute
		if cached.sourceTier > 1 {
			ttl = 60 * time.Second
		}
		if time.Since(cached.lastProbed) < ttl {
			temp, err := readTempFile(cached.path)
			if err == nil && temp > 0.0 && temp <= 150.0 {
				return temp
			}
		}
	}

	// 2. Cache miss, expired, or read error: re-probe hardware sensors
	cpuTempCacheMu.Lock()
	defer cpuTempCacheMu.Unlock()

	// Double-check after acquiring write lock
	if cpuTempCache.path != "" {
		ttl := 10 * time.Minute
		if cpuTempCache.sourceTier > 1 {
			ttl = 60 * time.Second
		}
		if time.Since(cpuTempCache.lastProbed) < ttl {
			temp, err := readTempFile(cpuTempCache.path)
			if err == nil && temp > 0.0 && temp <= 150.0 {
				return temp
			}
		}
	}

	// Invalidate stale cache
	cpuTempCache = tempSensorCache{}

	// Tier 1: Probe /sys/class/hwmon/ (lm-sensors kernel sysfs)
	if path, temp := probeHwmon(hwmonBase); path != "" && temp > 0.0 && temp <= 150.0 {
		cpuTempCache = tempSensorCache{
			path:       path,
			sourceTier: 1,
			lastProbed: time.Now(),
		}
		return temp
	}

	// Tier 2: Probe /sys/class/thermal/thermal_zone*
	if path, temp := probeThermalZones(thermalBase); path != "" && temp > 0.0 && temp <= 150.0 {
		cpuTempCache = tempSensorCache{
			path:       path,
			sourceTier: 2,
			lastProbed: time.Now(),
		}
		return temp
	}

	// Tier 3: Probe sensors CLI (from lm-sensors package)
	if temp := probeSensorsCLI(); temp > 0.0 && temp <= 150.0 {
		cpuTempCache = tempSensorCache{
			sourceTier: 3,
			lastProbed: time.Now(),
		}
		return temp
	}

	return 0.0
}

// readTempFile reads an integer millidegree temperature file and converts to Celsius.
func readTempFile(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(data))
	milliDeg, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return float64(milliDeg) / 1000.0, nil
}

// probeHwmon scans /sys/class/hwmon/ for CPU hardware monitoring drivers created by lm-sensors.
func probeHwmon(baseDir string) (string, float64) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", 0
	}

	// Priority driver chip names populated by lm-sensors / kernel
	priorityDrivers := []string{
		"coretemp",        // Intel x86
		"k10temp",         // AMD x86
		"zenpower",        // AMD Zen
		"cpu_thermal",     // ARM Broadcom / Raspberry Pi
		"bcm2835_thermal", // RPi 3/4
		"soc_dts",         // Intel SoC
		"soc_thermal",     // Rockchip / Allwinner
	}

	// First pass: look for high-priority driver directories
	for _, entry := range entries {
		hwmonDir := filepath.Join(baseDir, entry.Name())
		nameData, err := os.ReadFile(filepath.Join(hwmonDir, "name"))
		if err != nil {
			continue
		}
		driverName := strings.TrimSpace(string(nameData))

		isPriority := false
		for _, p := range priorityDrivers {
			if strings.EqualFold(driverName, p) {
				isPriority = true
				break
			}
		}
		if !isPriority {
			continue
		}

		if path, temp := findBestTempInHwmonDir(hwmonDir); path != "" {
			return path, temp
		}
	}

	// Second pass: look for ANY hwmon directory with a label matching CPU/Package
	for _, entry := range entries {
		hwmonDir := filepath.Join(baseDir, entry.Name())
		if path, temp := findLabeledCPUTemp(hwmonDir); path != "" {
			return path, temp
		}
	}

	// Third pass: if any hwmon directory has temp1_input, use it as fallback
	for _, entry := range entries {
		hwmonDir := filepath.Join(baseDir, entry.Name())
		tempPath := filepath.Join(hwmonDir, "temp1_input")
		if temp, err := readTempFile(tempPath); err == nil && temp > 0.0 && temp <= 150.0 {
			return tempPath, temp
		}
	}

	return "", 0
}

// findBestTempInHwmonDir searches a specific hwmon directory for the best CPU temperature input.
func findBestTempInHwmonDir(hwmonDir string) (string, float64) {
	preferredLabels := []string{"package id 0", "tctl", "tdie", "cpu", "core 0", "temp1"}

	files, err := os.ReadDir(hwmonDir)
	if err != nil {
		return "", 0
	}

	labelMap := make(map[string]string)
	var firstInputPath string
	var firstInputTemp float64

	for _, f := range files {
		name := f.Name()
		if strings.HasPrefix(name, "temp") && strings.HasSuffix(name, "_label") {
			prefix := strings.TrimSuffix(name, "_label") // e.g. "temp1"
			labelBytes, err := os.ReadFile(filepath.Join(hwmonDir, name))
			if err == nil {
				label := strings.TrimSpace(string(labelBytes))
				inputPath := filepath.Join(hwmonDir, prefix+"_input")
				labelMap[strings.ToLower(label)] = inputPath
			}
		} else if strings.HasPrefix(name, "temp") && strings.HasSuffix(name, "_input") {
			if firstInputPath == "" {
				inputPath := filepath.Join(hwmonDir, name)
				if temp, err := readTempFile(inputPath); err == nil && temp > 0.0 && temp <= 150.0 {
					firstInputPath = inputPath
					firstInputTemp = temp
				}
			}
		}
	}

	// Check preferred labels in order
	for _, pref := range preferredLabels {
		for lbl, inputPath := range labelMap {
			if strings.Contains(lbl, pref) {
				if temp, err := readTempFile(inputPath); err == nil && temp > 0.0 && temp <= 150.0 {
					return inputPath, temp
				}
			}
		}
	}

	if firstInputPath != "" {
		return firstInputPath, firstInputTemp
	}

	return "", 0
}

// findLabeledCPUTemp searches any generic hwmon device for CPU-related labels.
func findLabeledCPUTemp(hwmonDir string) (string, float64) {
	files, err := os.ReadDir(hwmonDir)
	if err != nil {
		return "", 0
	}

	cpuKeywords := []string{"package", "cpu", "tctl", "tdie", "core 0"}
	for _, f := range files {
		name := f.Name()
		if strings.HasPrefix(name, "temp") && strings.HasSuffix(name, "_label") {
			labelBytes, err := os.ReadFile(filepath.Join(hwmonDir, name))
			if err != nil {
				continue
			}
			label := strings.ToLower(strings.TrimSpace(string(labelBytes)))
			for _, kw := range cpuKeywords {
				if strings.Contains(label, kw) {
					prefix := strings.TrimSuffix(name, "_label")
					inputPath := filepath.Join(hwmonDir, prefix+"_input")
					if temp, err := readTempFile(inputPath); err == nil && temp > 0.0 && temp <= 150.0 {
						return inputPath, temp
					}
				}
			}
		}
	}
	return "", 0
}

// probeThermalZones scans /sys/class/thermal/ for CPU thermal zones.
func probeThermalZones(baseDir string) (string, float64) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", 0
	}

	preferredTypes := []string{
		"cpu-thermal",
		"bcm2712", // Raspberry Pi 5 Broadcom CPU
		"x86_pkg_temp",
		"k10temp",
		"soc_thermal",
		"cpu",
	}

	for _, pref := range preferredTypes {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "thermal_zone") {
				continue
			}
			zoneDir := filepath.Join(baseDir, entry.Name())
			typeBytes, err := os.ReadFile(filepath.Join(zoneDir, "type"))
			if err != nil {
				continue
			}
			zoneType := strings.ToLower(strings.TrimSpace(string(typeBytes)))
			if strings.Contains(zoneType, pref) {
				tempPath := filepath.Join(zoneDir, "temp")
				if temp, err := readTempFile(tempPath); err == nil && temp > 0.0 && temp <= 150.0 {
					return tempPath, temp
				}
			}
		}
	}

	// Fallback: thermal_zone0/temp
	tz0Path := filepath.Join(baseDir, "thermal_zone0", "temp")
	if temp, err := readTempFile(tz0Path); err == nil && temp > 0.0 && temp <= 150.0 {
		return tz0Path, temp
	}

	return "", 0
}

// probeSensorsCLI executes the lm-sensors userspace tool `sensors -j` as a Tier 3 fallback.
func probeSensorsCLI() float64 {
	sensorsPath, err := exec.LookPath("sensors")
	if err != nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, sensorsPath, "-j")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	return parseSensorsJSON(out)
}

// parseSensorsJSON parses JSON output from `sensors -j` and returns the CPU temperature in Celsius.
func parseSensorsJSON(data []byte) float64 {
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return 0
	}

	priorityChips := []string{"coretemp", "k10temp", "zenpower", "cpu_thermal"}

	for _, p := range priorityChips {
		for chipName, chipVal := range root {
			if strings.Contains(strings.ToLower(chipName), p) {
				if temp := extractTempFromChipObj(chipVal); temp > 0 {
					return temp
				}
			}
		}
	}

	// Fallback to any chip in the JSON
	for _, chipVal := range root {
		if temp := extractTempFromChipObj(chipVal); temp > 0 {
			return temp
		}
	}

	return 0
}

func extractTempFromChipObj(val interface{}) float64 {
	chipMap, ok := val.(map[string]interface{})
	if !ok {
		return 0
	}

	preferredFeatures := []string{"package id 0", "tctl", "tdie", "cpu", "core 0", "temp1"}

	// First look for preferred feature labels
	for _, pref := range preferredFeatures {
		for featName, featVal := range chipMap {
			if strings.Contains(strings.ToLower(featName), pref) {
				if featMap, ok := featVal.(map[string]interface{}); ok {
					for k, v := range featMap {
						if strings.HasSuffix(k, "_input") {
							if num, ok := v.(float64); ok && num > 0 && num <= 150.0 {
								return num
							}
						}
					}
				}
			}
		}
	}

	// Otherwise, pick the first valid temp*_input found
	for _, featVal := range chipMap {
		if featMap, ok := featVal.(map[string]interface{}); ok {
			for k, v := range featMap {
				if strings.HasSuffix(k, "_input") {
					if num, ok := v.(float64); ok && num > 0 && num <= 150.0 {
						return num
					}
				}
			}
		}
	}

	return 0
}
