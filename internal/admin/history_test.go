package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandHistoryRingBuffer(t *testing.T) {
	tmpDir := t.TempDir()
	historyFile := filepath.Join(tmpDir, "test_command_history.json")

	// Capacity of 5
	ch := NewCommandHistory(5, historyFile)

	if ch.Count() != 0 {
		t.Fatalf("expected initial count 0, got %d", ch.Count())
	}

	// Push 3 items
	for i := 1; i <= 3; i++ {
		ch.Push(CommandRecord{
			ID:        fmt.Sprintf("act-%d", i),
			Command:   fmt.Sprintf("echo %d", i),
			Timestamp: int64(1000 + i),
			Status:    "success",
		})
	}

	if ch.Count() != 3 {
		t.Fatalf("expected count 3, got %d", ch.Count())
	}

	all := ch.GetAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 items, got %d", len(all))
	}
	// Newest first
	if all[0].ID != "act-3" || all[2].ID != "act-1" {
		t.Errorf("expected newest first order, got first=%s, last=%s", all[0].ID, all[2].ID)
	}

	// Push 4 more items (total 7 > capacity 5)
	for i := 4; i <= 7; i++ {
		ch.Push(CommandRecord{
			ID:        fmt.Sprintf("act-%d", i),
			Command:   fmt.Sprintf("echo %d", i),
			Timestamp: int64(1000 + i),
			Status:    "success",
		})
	}

	// Bounded capacity check
	if ch.Count() != 5 {
		t.Fatalf("expected count capped at 5, got %d", ch.Count())
	}

	all = ch.GetAll()
	if len(all) != 5 {
		t.Fatalf("expected 5 items in GetAll(), got %d", len(all))
	}

	// The remaining items should be act-7 down to act-3 (act-1 and act-2 overwritten)
	if all[0].ID != "act-7" {
		t.Errorf("expected newest item act-7, got %s", all[0].ID)
	}
	if all[4].ID != "act-3" {
		t.Errorf("expected oldest item act-3, got %s", all[4].ID)
	}

	// Test persistence reload
	chReloaded := NewCommandHistory(5, historyFile)
	if chReloaded.Count() != 5 {
		t.Fatalf("expected reloaded count 5, got %d", chReloaded.Count())
	}
	reloadedAll := chReloaded.GetAll()
	if reloadedAll[0].ID != "act-7" || reloadedAll[4].ID != "act-3" {
		t.Errorf("reload corrupted order or data: first=%s, last=%s", reloadedAll[0].ID, reloadedAll[4].ID)
	}
}

func TestCommandHistoryUpdate(t *testing.T) {
	ch := NewCommandHistory(10, "")
	ch.Push(CommandRecord{
		ID:     "act-update",
		Status: "running",
	})

	rec, ok := ch.GetByID("act-update")
	if !ok || rec.Status != "running" {
		t.Fatalf("expected running record")
	}

	ch.Update(CommandRecord{
		ID:         "act-update",
		Status:     "success",
		DurationMs: 350,
	})

	rec, ok = ch.GetByID("act-update")
	if !ok || rec.Status != "success" || rec.DurationMs != 350 {
		t.Fatalf("expected updated record with success status and duration")
	}
}

func TestCommandHistoryFileNotFound(t *testing.T) {
	nonExistent := filepath.Join(os.TempDir(), "non_existent_history_file.json")
	_ = os.Remove(nonExistent)
	ch := NewCommandHistory(10, nonExistent)
	if ch.Count() != 0 {
		t.Errorf("expected 0 records for nonexistent file, got %d", ch.Count())
	}
}
