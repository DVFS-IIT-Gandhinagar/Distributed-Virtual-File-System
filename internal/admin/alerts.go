package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AlertSeverity defines the urgency levels for system alerts.
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

// AlertType categorizes the source or cause of the alert.
type AlertType string

const (
	AlertTypeNodeOffline    AlertType = "node_offline"
	AlertTypeNodeOnline     AlertType = "node_online"
	AlertTypeStorageWarning AlertType = "storage_warning"
	AlertTypeTempWarning    AlertType = "temp_warning"
	AlertTypeQuotaExceeded  AlertType = "quota_exceeded"
	AlertTypeErrorSpike     AlertType = "error_spike"
	AlertTypeServiceRestart AlertType = "service_restart"
)

// Alert represents an event record in the system alert log.
type Alert struct {
	ID         string        `json:"id"`
	Timestamp  int64         `json:"timestamp"`
	Severity   AlertSeverity `json:"severity"`
	Type       AlertType     `json:"type"`
	Title      string        `json:"title"`
	Message    string        `json:"message"`
	NodeID     string        `json:"node_id,omitempty"`
	NodeName   string        `json:"node_name,omitempty"`
	Username   string        `json:"username,omitempty"`
	Resolved   bool          `json:"resolved"`
	ResolvedAt int64         `json:"resolved_at,omitempty"`
}

// AlertSummary reports aggregate counts of active alerts.
type AlertSummary struct {
	CriticalCount   int `json:"critical_count"`
	WarningCount    int `json:"warning_count"`
	InfoCount       int `json:"info_count"`
	TotalUnresolved int `json:"total_unresolved"`
}

// AlertFilters specifies criteria for querying alerts.
type AlertFilters struct {
	Severity   string
	NodeID     string
	Unresolved bool
	Limit      int
}

// AlertManager detects system health events, tracks active condition states,
// avoids duplicate alert flooding, and persists alerts to disk.
type AlertManager struct {
	alerts           []Alert
	activeConditions map[string]*Alert // key -> pointer to active alert
	capacity         int
	filePath         string
	mu               sync.RWMutex
}

// NewAlertManager creates a new AlertManager instance.
func NewAlertManager(capacity int, filePath string) *AlertManager {
	if capacity <= 0 {
		capacity = 500
	}
	am := &AlertManager{
		alerts:           make([]Alert, 0, capacity),
		activeConditions: make(map[string]*Alert),
		capacity:         capacity,
		filePath:         filePath,
	}
	if filePath != "" {
		_ = am.load()
	}
	return am
}

// CheckNodeHealth evaluates telemetry for a single node and triggers/resolves alerts accordingly.
func (am *AlertManager) CheckNodeHealth(node *NodeState, prevLastSeen int64, prevUptime float64) {
	if node == nil {
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now().Unix()
	nodeLabel := node.DisplayName
	if nodeLabel == "" {
		nodeLabel = fmt.Sprintf("FS-%s", node.FsID)
	}
	if node.MachineName != "" {
		nodeLabel = fmt.Sprintf("%s (%s)", nodeLabel, node.MachineName)
	}

	// 1. Check Node Offline / Online Transition
	offlineKey := "node_offline:" + node.FsID
	if node.Status == StatusOffline {
		if _, exists := am.activeConditions[offlineKey]; !exists {
			msg := fmt.Sprintf("Node %s at %s has stopped responding to metrics polls (last seen: %ds ago).",
				nodeLabel, node.Address, now-node.LastSeen)
			if node.LastSeen == 0 {
				msg = fmt.Sprintf("Node %s at %s is unreachable and has never reported metrics.", nodeLabel, node.Address)
			}
			alert := am.createAlertLocked(SeverityCritical, AlertTypeNodeOffline,
				fmt.Sprintf("Node Offline: %s", nodeLabel), msg, node.FsID, nodeLabel, "")
			am.activeConditions[offlineKey] = alert
		}
	} else {
		// Node is online (or warning/degraded/critical but responding)
		if activeAlert, exists := am.activeConditions[offlineKey]; exists {
			activeAlert.Resolved = true
			activeAlert.ResolvedAt = now
			delete(am.activeConditions, offlineKey)

			// Emit recovery alert
			recoveryAlert := am.createAlertLocked(SeverityInfo, AlertTypeNodeOnline,
				fmt.Sprintf("Node Recovered: %s", nodeLabel),
				fmt.Sprintf("Node %s at %s is back online and responding to metrics polls.", nodeLabel, node.Address),
				node.FsID, nodeLabel, "")
			recoveryAlert.Resolved = true
			recoveryAlert.ResolvedAt = now
		}
	}

	// If node is offline, we skip secondary metric checks (storage, temp, etc.)
	if node.Metrics == nil || node.Status == StatusOffline {
		if am.filePath != "" {
			_ = am.saveLocked()
		}
		return
	}

	// 2. Storage Thresholds (>80% warning, >90% degraded, >95% critical)
	storageKey := "node_storage:" + node.FsID
	usagePct := node.Metrics.DiskUsagePercent
	var storageSev AlertSeverity
	var storageTitle string
	if usagePct >= 95.0 {
		storageSev = SeverityCritical
		storageTitle = fmt.Sprintf("Critical Storage Full: %s (%.1f%%)", nodeLabel, usagePct)
	} else if usagePct >= 90.0 {
		storageSev = SeverityWarning
		storageTitle = fmt.Sprintf("Storage Degraded: %s (%.1f%%)", nodeLabel, usagePct)
	} else if usagePct >= 80.0 {
		storageSev = SeverityWarning
		storageTitle = fmt.Sprintf("Storage Warning: %s (%.1f%%)", nodeLabel, usagePct)
	}

	if storageSev != "" {
		active, exists := am.activeConditions[storageKey]
		if !exists || active.Severity != storageSev {
			if exists {
				active.Resolved = true
				active.ResolvedAt = now
			}
			msg := fmt.Sprintf("Storage on %s has reached %.1f%% (%d of %d bytes used).",
				nodeLabel, usagePct, node.Metrics.DiskUsedBytes, node.Metrics.DiskTotalBytes)
			alert := am.createAlertLocked(storageSev, AlertTypeStorageWarning, storageTitle, msg, node.FsID, nodeLabel, "")
			am.activeConditions[storageKey] = alert
		}
	} else {
		// Storage cleared below 80%
		if active, exists := am.activeConditions[storageKey]; exists {
			active.Resolved = true
			active.ResolvedAt = now
			delete(am.activeConditions, storageKey)
		}
	}

	// 3. CPU Temperature Thresholds (>65°C warning, >75°C degraded, >85°C critical)
	tempKey := "node_temp:" + node.FsID
	tempC := node.Metrics.CPUTempCelsius
	var tempSev AlertSeverity
	var tempTitle string
	if tempC >= 85.0 {
		tempSev = SeverityCritical
		tempTitle = fmt.Sprintf("Critical CPU Temp: %s (%.1f°C)", nodeLabel, tempC)
	} else if tempC >= 75.0 {
		tempSev = SeverityWarning
		tempTitle = fmt.Sprintf("High CPU Temp: %s (%.1f°C)", nodeLabel, tempC)
	} else if tempC >= 65.0 {
		tempSev = SeverityWarning
		tempTitle = fmt.Sprintf("Elevated CPU Temp: %s (%.1f°C)", nodeLabel, tempC)
	}

	if tempSev != "" {
		active, exists := am.activeConditions[tempKey]
		if !exists || active.Severity != tempSev {
			if exists {
				active.Resolved = true
				active.ResolvedAt = now
			}
			msg := fmt.Sprintf("CPU temperature on %s exceeded threshold: %.1f°C.", nodeLabel, tempC)
			alert := am.createAlertLocked(tempSev, AlertTypeTempWarning, tempTitle, msg, node.FsID, nodeLabel, "")
			am.activeConditions[tempKey] = alert
		}
	} else {
		// Temp normalized below 65°C
		if active, exists := am.activeConditions[tempKey]; exists {
			active.Resolved = true
			active.ResolvedAt = now
			delete(am.activeConditions, tempKey)
		}
	}

	// 4. Error Rate Spike (>20% critical, >5% warning)
	errorKey := "node_error:" + node.FsID
	errPct := node.ErrorRatePct
	var errSev AlertSeverity
	if errPct >= 20.0 {
		errSev = SeverityCritical
	} else if errPct >= 5.0 {
		errSev = SeverityWarning
	}

	if errSev != "" {
		active, exists := am.activeConditions[errorKey]
		if !exists || active.Severity != errSev {
			if exists {
				active.Resolved = true
				active.ResolvedAt = now
			}
			alert := am.createAlertLocked(errSev, AlertTypeErrorSpike,
				fmt.Sprintf("Error Rate Spike: %s (%.1f%%)", nodeLabel, errPct),
				fmt.Sprintf("Node %s is experiencing an elevated operation error rate of %.1f%%.", nodeLabel, errPct),
				node.FsID, nodeLabel, "")
			am.activeConditions[errorKey] = alert
		}
	} else {
		if active, exists := am.activeConditions[errorKey]; exists {
			active.Resolved = true
			active.ResolvedAt = now
			delete(am.activeConditions, errorKey)
		}
	}

	// 5. Service Restart Detection (uptime reset)
	currentUptime := node.Metrics.UptimeSeconds
	if prevUptime > 10.0 && currentUptime < prevUptime {
		alert := am.createAlertLocked(SeverityInfo, AlertTypeServiceRestart,
			fmt.Sprintf("Process Restarted: %s", nodeLabel),
			fmt.Sprintf("Fileserver on %s restarted (uptime reset from %.0fs to %.0fs).", nodeLabel, prevUptime, currentUptime),
			node.FsID, nodeLabel, "")
		alert.Resolved = true
		alert.ResolvedAt = now
	}

	if am.filePath != "" {
		_ = am.saveLocked()
	}
}

// CheckUserQuota evaluates a user's storage usage against their quota limit.
func (am *AlertManager) CheckUserQuota(username string, used, quota uint64, homeNodeID, homeNodeLabel string) {
	if quota == 0 || username == "" {
		return
	}

	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now().Unix()
	quotaKey := "user_quota:" + username

	if used >= quota {
		if _, exists := am.activeConditions[quotaKey]; !exists {
			usagePct := float64(used) / float64(quota) * 100.0
			alert := am.createAlertLocked(SeverityWarning, AlertTypeQuotaExceeded,
				fmt.Sprintf("Quota Exceeded: %s", username),
				fmt.Sprintf("User '%s' has reached %.1f%% of their storage quota (%d / %d bytes). New writes are blocked.",
					username, usagePct, used, quota),
				homeNodeID, homeNodeLabel, username)
			am.activeConditions[quotaKey] = alert
			if am.filePath != "" {
				_ = am.saveLocked()
			}
		}
	} else {
		if active, exists := am.activeConditions[quotaKey]; exists {
			active.Resolved = true
			active.ResolvedAt = now
			delete(am.activeConditions, quotaKey)
			if am.filePath != "" {
				_ = am.saveLocked()
			}
		}
	}
}

// createAlertLocked instantiates and prepends an alert to the bounded log.
func (am *AlertManager) createAlertLocked(sev AlertSeverity, aType AlertType, title, message, nodeID, nodeName, username string) *Alert {
	alert := Alert{
		ID:        fmt.Sprintf("alt-%s", uuid.New().String()[:8]),
		Timestamp: time.Now().Unix(),
		Severity:  sev,
		Type:      aType,
		Title:     title,
		Message:   message,
		NodeID:    nodeID,
		NodeName:  nodeName,
		Username:  username,
		Resolved:  false,
	}

	// Prepend to newest-first slice
	am.alerts = append([]Alert{alert}, am.alerts...)
	if len(am.alerts) > am.capacity {
		am.alerts = am.alerts[:am.capacity]
	}

	// Return reference to the inserted element
	return &am.alerts[0]
}

// ResolveAlert marks a specific alert as resolved by ID.
func (am *AlertManager) ResolveAlert(id string) bool {
	am.mu.Lock()
	defer am.mu.Unlock()

	found := false
	now := time.Now().Unix()
	for i := range am.alerts {
		if am.alerts[i].ID == id {
			am.alerts[i].Resolved = true
			am.alerts[i].ResolvedAt = now
			found = true
			break
		}
	}

	// Also clear from active conditions if tracked
	for k, act := range am.activeConditions {
		if act.ID == id {
			delete(am.activeConditions, k)
			break
		}
	}

	if found && am.filePath != "" {
		_ = am.saveLocked()
	}
	return found
}

// ResolveAll marks all alerts as resolved.
func (am *AlertManager) ResolveAll() int {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now().Unix()
	count := 0
	for i := range am.alerts {
		if !am.alerts[i].Resolved {
			am.alerts[i].Resolved = true
			am.alerts[i].ResolvedAt = now
			count++
		}
	}
	am.activeConditions = make(map[string]*Alert)

	if count > 0 && am.filePath != "" {
		_ = am.saveLocked()
	}
	return count
}

// GetAll returns alerts matching the given filters.
func (am *AlertManager) GetAll(f AlertFilters) []Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var result []Alert
	for _, a := range am.alerts {
		if f.Unresolved && a.Resolved {
			continue
		}
		if f.Severity != "" && !strings.EqualFold(string(a.Severity), f.Severity) {
			continue
		}
		if f.NodeID != "" && a.NodeID != f.NodeID {
			continue
		}
		result = append(result, a)
		if f.Limit > 0 && len(result) >= f.Limit {
			break
		}
	}
	return result
}

// Summary returns the current count of alerts grouped by severity.
func (am *AlertManager) Summary() AlertSummary {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var s AlertSummary
	for _, a := range am.alerts {
		if !a.Resolved {
			s.TotalUnresolved++
			switch a.Severity {
			case SeverityCritical:
				s.CriticalCount++
			case SeverityWarning:
				s.WarningCount++
			case SeverityInfo:
				s.InfoCount++
			}
		}
	}
	return s
}

// saveLocked atomically persists the alert log to disk.
func (am *AlertManager) saveLocked() error {
	if am.filePath == "" {
		return nil
	}
	dir := filepath.Dir(am.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpFile := am.filePath + ".tmp"
	data, err := json.MarshalIndent(am.alerts, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpFile, am.filePath)
}

// load reads the alert log from disk.
func (am *AlertManager) load() error {
	am.mu.Lock()
	defer am.mu.Unlock()

	data, err := os.ReadFile(am.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var loaded []Alert
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[ADMIN ALERTS] Warning: failed to decode %s: %v", am.filePath, err)
		return err
	}

	// Sort newest first
	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].Timestamp > loaded[j].Timestamp
	})
	if len(loaded) > am.capacity {
		loaded = loaded[:am.capacity]
	}
	am.alerts = loaded

	// Rebuild active conditions for unresolved alerts
	for i := range am.alerts {
		a := &am.alerts[i]
		if !a.Resolved {
			switch a.Type {
			case AlertTypeNodeOffline:
				am.activeConditions["node_offline:"+a.NodeID] = a
			case AlertTypeStorageWarning:
				am.activeConditions["node_storage:"+a.NodeID] = a
			case AlertTypeTempWarning:
				am.activeConditions["node_temp:"+a.NodeID] = a
			case AlertTypeErrorSpike:
				am.activeConditions["node_error:"+a.NodeID] = a
			case AlertTypeQuotaExceeded:
				am.activeConditions["user_quota:"+a.Username] = a
			}
		}
	}

	return nil
}
