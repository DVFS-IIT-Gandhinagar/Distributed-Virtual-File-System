package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// NodeResult captures the output and status of a command on a specific node.
type NodeResult struct {
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// CommandRecord represents a single orchestration action execution across cluster nodes.
type CommandRecord struct {
	ID          string                 `json:"id"`
	Timestamp   int64                  `json:"timestamp"`
	ActionType  string                 `json:"action_type"`
	Command     string                 `json:"command"`
	TargetNodes []string               `json:"target_nodes"`
	Status      string                 `json:"status"` // "running", "success", "failed"
	DurationMs  int64                  `json:"duration_ms"`
	NodeResults map[string]*NodeResult `json:"node_results"`
}

// CommandHistory implements a fixed-size, bounded circular ring buffer for command executions
// to guarantee that memory and disk usage never grow unbounded.
type CommandHistory struct {
	data     []CommandRecord
	capacity int
	head     int
	count    int
	filePath string
	mu       sync.RWMutex
}

// NewCommandHistory creates a bounded command history buffer with atomic JSON persistence.
func NewCommandHistory(capacity int, filePath string) *CommandHistory {
	if capacity <= 0 {
		capacity = 100
	}
	ch := &CommandHistory{
		data:     make([]CommandRecord, capacity),
		capacity: capacity,
		filePath: filePath,
	}
	if filePath != "" {
		_ = ch.load()
	}
	return ch
}

// Push adds a record to the circular buffer and flushes to disk.
func (ch *CommandHistory) Push(r CommandRecord) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	ch.data[ch.head] = r
	ch.head = (ch.head + 1) % ch.capacity
	if ch.count < ch.capacity {
		ch.count++
	}

	if ch.filePath != "" {
		_ = ch.saveLocked()
	}
}

// Update updates an existing record in-place by ID (useful when transitioning from running to success/failed).
func (ch *CommandHistory) Update(r CommandRecord) {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	for i := 0; i < ch.count; i++ {
		var idx int
		if ch.count < ch.capacity {
			idx = i
		} else {
			idx = (ch.head + i) % ch.capacity
		}
		if ch.data[idx].ID == r.ID {
			ch.data[idx] = r
			if ch.filePath != "" {
				_ = ch.saveLocked()
			}
			return
		}
	}
}

// GetAll returns all stored records in reverse chronological order (newest first).
func (ch *CommandHistory) GetAll() []CommandRecord {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	out := make([]CommandRecord, ch.count)
	// Iterate backwards from newest to oldest
	for i := 0; i < ch.count; i++ {
		idx := (ch.head - 1 - i + ch.capacity) % ch.capacity
		out[i] = ch.data[idx]
	}
	return out
}

// GetByID finds a specific command record by ID.
func (ch *CommandHistory) GetByID(id string) (*CommandRecord, bool) {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	for i := 0; i < ch.count; i++ {
		if ch.data[i].ID == id {
			rec := ch.data[i]
			return &rec, true
		}
	}
	return nil, false
}

// Count returns the number of records currently held in the ring buffer.
func (ch *CommandHistory) Count() int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.count
}

// saveLocked persists the ring buffer to disk atomically via temporary file replacement.
func (ch *CommandHistory) saveLocked() error {
	dir := filepath.Dir(ch.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for history file: %w", err)
	}

	// Write newest-first records
	records := make([]CommandRecord, ch.count)
	for i := 0; i < ch.count; i++ {
		idx := (ch.head - 1 - i + ch.capacity) % ch.capacity
		records[i] = ch.data[idx]
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history records: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "command_history_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary history file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write history to temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp history file: %w", err)
	}

	if err := os.Rename(tmpPath, ch.filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to replace history file: %w", err)
	}

	return nil
}

// load loads existing records from JSON file on startup, populating up to capacity.
func (ch *CommandHistory) load() error {
	if _, err := os.Stat(ch.filePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(ch.filePath)
	if err != nil {
		return fmt.Errorf("failed to read history file: %w", err)
	}

	var records []CommandRecord
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("[ADMIN] Warning: corrupted history file %s: %v", ch.filePath, err)
		return err
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	// Records were saved newest-first. Reverse to insert chronologically.
	n := len(records)
	if n > ch.capacity {
		records = records[:ch.capacity]
		n = ch.capacity
	}

	for i := n - 1; i >= 0; i-- {
		ch.data[ch.head] = records[i]
		ch.head = (ch.head + 1) % ch.capacity
		if ch.count < ch.capacity {
			ch.count++
		}
	}

	log.Printf("[ADMIN] Loaded %d command history records from %s", ch.count, ch.filePath)
	return nil
}
