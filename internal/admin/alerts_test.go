package admin

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAlertManager_NodeOfflineAndRecovery(t *testing.T) {
	tempDir := t.TempDir()
	alertsFile := filepath.Join(tempDir, "alerts.json")
	am := NewAlertManager(100, alertsFile)

	node := &NodeState{
		FsID:        "0",
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "10.0.0.1:50052",
		Status:      StatusOffline,
		LastSeen:    time.Now().Unix() - 40,
	}

	// 1. Initial check - should trigger Critical Node Offline alert
	am.CheckNodeHealth(node, 0, 0)

	alerts := am.GetAll(AlertFilters{})
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != SeverityCritical || alerts[0].Type != AlertTypeNodeOffline {
		t.Errorf("unexpected alert: %+v", alerts[0])
	}
	if alerts[0].Resolved {
		t.Errorf("expected alert to be unresolved")
	}

	// 2. Deduplication check - calling again should NOT create duplicate alert
	am.CheckNodeHealth(node, node.LastSeen, 0)
	alerts = am.GetAll(AlertFilters{})
	if len(alerts) != 1 {
		t.Errorf("expected still 1 alert (deduped), got %d", len(alerts))
	}

	// 3. Node recovers and comes back online
	node.Status = StatusOnline
	node.LastSeen = time.Now().Unix()
	node.Metrics = &FileserverMetrics{
		UptimeSeconds: 100,
	}
	am.CheckNodeHealth(node, node.LastSeen-40, 0)

	alerts = am.GetAll(AlertFilters{})
	// We should now have 2 alerts: the original (now resolved) + new recovery info alert (resolved)
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts after recovery, got %d", len(alerts))
	}

	summary := am.Summary()
	if summary.TotalUnresolved != 0 {
		t.Errorf("expected 0 unresolved alerts after recovery, got %d", summary.TotalUnresolved)
	}
}

func TestAlertManager_StorageThresholds(t *testing.T) {
	am := NewAlertManager(100, "")

	node := &NodeState{
		FsID:        "1",
		DisplayName: "FS-2",
		Address:     "10.0.0.2:50052",
		Status:      StatusOnline,
		LastSeen:    time.Now().Unix(),
		Metrics: &FileserverMetrics{
			DiskTotalBytes:   1000,
			DiskUsedBytes:    850,
			DiskUsagePercent: 85.0,
			UptimeSeconds:    500,
		},
	}

	// 1. Storage at 85% -> Warning
	am.CheckNodeHealth(node, node.LastSeen, 500)
	alerts := am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 1 || alerts[0].Severity != SeverityWarning {
		t.Fatalf("expected 1 warning alert for 85%% storage, got: %+v", alerts)
	}

	// 2. Storage escalates to 96% -> Critical
	node.Metrics.DiskUsedBytes = 960
	node.Metrics.DiskUsagePercent = 96.0
	am.CheckNodeHealth(node, node.LastSeen, 500)

	unresolved := am.GetAll(AlertFilters{Unresolved: true})
	if len(unresolved) != 1 || unresolved[0].Severity != SeverityCritical {
		t.Fatalf("expected 1 critical alert after escalation, got: %+v", unresolved)
	}

	// 3. Storage drops back down to 50% -> Clears alert
	node.Metrics.DiskUsedBytes = 500
	node.Metrics.DiskUsagePercent = 50.0
	am.CheckNodeHealth(node, node.LastSeen, 500)

	unresolved = am.GetAll(AlertFilters{Unresolved: true})
	if len(unresolved) != 0 {
		t.Errorf("expected 0 unresolved alerts after storage normalized, got %d", len(unresolved))
	}
}

func TestAlertManager_CPUTempThresholds(t *testing.T) {
	am := NewAlertManager(100, "")

	node := &NodeState{
		FsID:        "2",
		DisplayName: "FS-3",
		Address:     "10.0.0.3:50052",
		Status:      StatusOnline,
		LastSeen:    time.Now().Unix(),
		Metrics: &FileserverMetrics{
			CPUTempCelsius: 68.0,
			UptimeSeconds:  1000,
		},
	}

	// Elevated temp 68°C -> Warning
	am.CheckNodeHealth(node, node.LastSeen, 1000)
	alerts := am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 1 || alerts[0].Severity != SeverityWarning {
		t.Fatalf("expected 1 warning alert for 68C, got: %+v", alerts)
	}

	// Critical temp 88°C -> Critical
	node.Metrics.CPUTempCelsius = 88.0
	am.CheckNodeHealth(node, node.LastSeen, 1000)
	alerts = am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 1 || alerts[0].Severity != SeverityCritical {
		t.Fatalf("expected 1 critical alert for 88C, got: %+v", alerts)
	}

	// Normalizes to 45°C -> Cleared
	node.Metrics.CPUTempCelsius = 45.0
	am.CheckNodeHealth(node, node.LastSeen, 1000)
	alerts = am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 0 {
		t.Errorf("expected 0 unresolved alerts after temp normalized, got %d", len(alerts))
	}
}

func TestAlertManager_ServiceRestartAndErrorSpike(t *testing.T) {
	am := NewAlertManager(100, "")

	node := &NodeState{
		FsID:         "3",
		DisplayName:  "FS-4",
		Status:       StatusOnline,
		LastSeen:     time.Now().Unix(),
		ErrorRatePct: 25.0,
		Metrics: &FileserverMetrics{
			UptimeSeconds: 5, // reset from 1000
		},
	}

	// Restart detected (prev uptime 1000, current uptime 5) & error rate spike 25%
	am.CheckNodeHealth(node, node.LastSeen, 1000)

	all := am.GetAll(AlertFilters{})
	if len(all) < 2 {
		t.Fatalf("expected at least 2 alerts (restart + error spike), got %d", len(all))
	}

	hasRestart := false
	hasErrorSpike := false
	for _, a := range all {
		if a.Type == AlertTypeServiceRestart {
			hasRestart = true
		}
		if a.Type == AlertTypeErrorSpike && a.Severity == SeverityCritical {
			hasErrorSpike = true
		}
	}
	if !hasRestart {
		t.Errorf("expected service restart alert")
	}
	if !hasErrorSpike {
		t.Errorf("expected critical error spike alert")
	}
}

func TestAlertManager_UserQuotaAlertAndRecovery(t *testing.T) {
	am := NewAlertManager(100, "")

	// 1. Quota exceeded
	am.CheckUserQuota("alice", 1500, 1000, "0", "FS-1")
	alerts := am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 1 || alerts[0].Type != AlertTypeQuotaExceeded {
		t.Fatalf("expected 1 quota exceeded alert, got: %+v", alerts)
	}

	// 2. Dedup
	am.CheckUserQuota("alice", 1600, 1000, "0", "FS-1")
	alerts = am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 1 {
		t.Errorf("expected still 1 quota alert (deduped), got %d", len(alerts))
	}

	// 3. Recovery (user deleted files)
	am.CheckUserQuota("alice", 800, 1000, "0", "FS-1")
	alerts = am.GetAll(AlertFilters{Unresolved: true})
	if len(alerts) != 0 {
		t.Errorf("expected quota alert to be resolved, got %d unresolved", len(alerts))
	}
}

func TestAlertManager_ManualResolveAndPersistence(t *testing.T) {
	tempDir := t.TempDir()
	alertsFile := filepath.Join(tempDir, "alerts.json")
	am1 := NewAlertManager(100, alertsFile)

	am1.CheckUserQuota("bob", 2000, 1000, "1", "FS-2")
	unresolved := am1.GetAll(AlertFilters{Unresolved: true})
	if len(unresolved) != 1 {
		t.Fatalf("expected 1 unresolved alert, got %d", len(unresolved))
	}

	alertID := unresolved[0].ID

	// Create new manager from same file to test persistence load
	am2 := NewAlertManager(100, alertsFile)
	unresolved2 := am2.GetAll(AlertFilters{Unresolved: true})
	if len(unresolved2) != 1 || unresolved2[0].ID != alertID {
		t.Fatalf("expected persisted alert to reload, got: %+v", unresolved2)
	}

	// Resolve via am2
	if !am2.ResolveAlert(alertID) {
		t.Errorf("expected ResolveAlert to succeed")
	}

	summary := am2.Summary()
	if summary.TotalUnresolved != 0 {
		t.Errorf("expected 0 unresolved after resolve, got %d", summary.TotalUnresolved)
	}

	// Test ResolveAll
	am2.createAlertLocked(SeverityWarning, AlertTypeStorageWarning, "Test 1", "Msg 1", "0", "FS-1", "")
	am2.createAlertLocked(SeverityCritical, AlertTypeNodeOffline, "Test 2", "Msg 2", "1", "FS-2", "")
	if am2.Summary().TotalUnresolved != 2 {
		t.Fatalf("expected 2 unresolved alerts")
	}
	count := am2.ResolveAll()
	if count != 2 || am2.Summary().TotalUnresolved != 0 {
		t.Errorf("expected ResolveAll to resolve 2 alerts, resolved: %d", count)
	}
}
