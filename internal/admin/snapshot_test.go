package admin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotPersistence_SaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()
	snapshotFile := filepath.Join(tempDir, "metrics_snapshot.json")

	srv1 := &AdminServer{
		nodes: make(map[string]*NodeState),
	}

	node1 := &NodeState{
		FsID:    "0",
		Status:  StatusOnline,
		History: NewRingBuffer(10),
	}
	snap1 := Snapshot{
		Timestamp: time.Now().Unix() - 10,
		WriteMbps: 12.5,
		ReadMbps:  45.0,
		Metrics: FileserverMetrics{
			DiskTotalBytes: 1000000,
			DiskUsedBytes:  500000,
			UptimeSeconds:  120,
		},
	}
	snap2 := Snapshot{
		Timestamp: time.Now().Unix(),
		WriteMbps: 15.0,
		ReadMbps:  50.0,
		Metrics: FileserverMetrics{
			DiskTotalBytes: 1000000,
			DiskUsedBytes:  520000,
			UptimeSeconds:  130,
		},
	}
	node1.History.Push(snap1)
	node1.History.Push(snap2)
	srv1.nodes["0"] = node1

	// Save snapshot to disk
	err := srv1.SaveMetricsSnapshot(snapshotFile)
	if err != nil {
		t.Fatalf("SaveMetricsSnapshot failed: %v", err)
	}

	// Create new server and load snapshot
	srv2 := &AdminServer{
		nodes: make(map[string]*NodeState),
	}
	err = srv2.LoadMetricsSnapshot(snapshotFile)
	if err != nil {
		t.Fatalf("LoadMetricsSnapshot failed: %v", err)
	}

	restoredNode, exists := srv2.nodes["0"]
	if !exists || restoredNode == nil {
		t.Fatalf("expected node 0 to be restored")
	}

	history := restoredNode.History.GetAll()
	if len(history) != 2 {
		t.Fatalf("expected 2 historical snapshots restored, got %d", len(history))
	}
	if history[1].WriteMbps != 15.0 || history[1].Metrics.UptimeSeconds != 130 {
		t.Errorf("restored snapshot content mismatch: %+v", history[1])
	}
}

func TestSnapshotPersistence_NonExistentAndCorruptFile(t *testing.T) {
	tempDir := t.TempDir()

	srv := &AdminServer{
		nodes: make(map[string]*NodeState),
	}

	// 1. Non-existent file should return nil without error
	err := srv.LoadMetricsSnapshot(filepath.Join(tempDir, "missing.json"))
	if err != nil {
		t.Errorf("expected nil error for missing snapshot file, got %v", err)
	}

	// 2. Corrupt file should return an error
	corruptFile := filepath.Join(tempDir, "corrupt.json")
	_ = os.WriteFile(corruptFile, []byte("{corrupted json"), 0644)

	err = srv.LoadMetricsSnapshot(corruptFile)
	if err == nil {
		t.Errorf("expected error for corrupted snapshot file, got nil")
	}
}
