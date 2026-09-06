package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// NodeHistoryStore captures historical snapshot records for a single fileserver node.
type NodeHistoryStore struct {
	FsID      string     `json:"fs_id"`
	Snapshots []Snapshot `json:"snapshots"`
}

// MetricsSnapshotFile captures the persisted state of all node ring buffers.
type MetricsSnapshotFile struct {
	SavedAt  int64                        `json:"saved_at"`
	NodeData map[string]NodeHistoryStore  `json:"node_data"`
}

// SaveMetricsSnapshot serializes all node ring-buffer histories to disk atomically.
func (a *AdminServer) SaveMetricsSnapshot(filePath string) error {
	if filePath == "" {
		return nil
	}

	a.mu.RLock()
	data := MetricsSnapshotFile{
		SavedAt:  time.Now().Unix(),
		NodeData: make(map[string]NodeHistoryStore, len(a.nodes)),
	}
	for id, node := range a.nodes {
		if node.History != nil {
			snaps := node.History.GetAll()
			if len(snaps) > 0 {
				data.NodeData[id] = NodeHistoryStore{
					FsID:      id,
					Snapshots: snaps,
				}
			}
		}
	}
	a.mu.RUnlock()

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for snapshot: %w", err)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics snapshot: %w", err)
	}

	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0644); err != nil {
		return fmt.Errorf("failed to write metrics snapshot tmp file: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("failed to rename metrics snapshot: %w", err)
	}

	return nil
}

// LoadMetricsSnapshot restores historical ring-buffer data from disk.
func (a *AdminServer) LoadMetricsSnapshot(filePath string) error {
	if filePath == "" {
		return nil
	}

	raw, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read snapshot file: %w", err)
	}

	var file MetricsSnapshotFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("failed to parse metrics snapshot file: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for id, store := range file.NodeData {
		node, exists := a.nodes[id]
		if !exists {
			node = &NodeState{
				FsID:    id,
				Status:  StatusOffline,
				History: NewRingBuffer(720),
			}
			a.nodes[id] = node
		}
		if node.History == nil {
			node.History = NewRingBuffer(720)
		}
		for _, s := range store.Snapshots {
			node.History.Push(s)
		}
		if len(store.Snapshots) > 0 {
			last := store.Snapshots[len(store.Snapshots)-1]
			node.LastSeen = last.Timestamp
			node.Metrics = &last.Metrics
			node.WriteMbps = last.WriteMbps
			node.ReadMbps = last.ReadMbps
			node.WriteIOPS = last.WriteIOPS
			node.ReadIOPS = last.ReadIOPS
			node.ErrorRatePct = last.ErrorRatePct
		}
	}

	log.Printf("[ADMIN] Restored historical metrics for %d nodes from %s", len(file.NodeData), filePath)
	return nil
}
