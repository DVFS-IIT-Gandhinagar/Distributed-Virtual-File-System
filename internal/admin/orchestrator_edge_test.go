package admin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOrchestratorAtomicBatchLockRollback tests that when a batch action fails to lock
// one of its target nodes because another action holds it, all previously acquired
// node locks in the batch are cleanly rolled back without deadlocking or leaving orphan locks.
func TestOrchestratorAtomicBatchLockRollback(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052", Status: StatusOnline}
	srv.nodes["1"] = &NodeState{FsID: "1", Address: "10.0.0.2:50052", Status: StatusOnline}
	srv.nodes["2"] = &NodeState{FsID: "2", Address: "10.0.0.3:50052", Status: StatusOnline}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{
		Stdout: "sleeping\n",
		Delay:  250 * time.Millisecond,
	}

	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "ubuntu", "~/.ssh/id_rsa", "/repo")

	// 1. Launch Action A locking node "1"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = orchestrator.Execute(context.Background(), ActionRequest{
			ActionType:    ActionPull,
			TargetNodeIDs: []string{"1"},
		}, nil)
	}()

	// Give Action A time to acquire lock on "1"
	time.Sleep(30 * time.Millisecond)

	// 2. Launch Action B attempting to lock nodes ["0", "1", "2"]
	// Node 0 should be acquired first, then Node 1 fails.
	// Orchestrator must rollback Node 0, leaving neither 0 nor 2 locked!
	_, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionBuild,
		TargetNodeIDs: []string{"0", "1", "2"},
	}, nil)

	if err == nil {
		t.Fatalf("expected batch lock conflict on node 1, but got nil error")
	}
	if !strings.Contains(err.Error(), "currently executing action") {
		t.Fatalf("expected concurrency error, got: %v", err)
	}

	// 3. Verify that Node 0 was rolled back and is NOT left locked
	_, busy0 := orchestrator.activeNodes.Load("0")
	if busy0 {
		t.Errorf("CRITICAL: Node 0 was left locked after batch failure on Node 1 (rollback failed!)")
	}

	_, busy2 := orchestrator.activeNodes.Load("2")
	if busy2 {
		t.Errorf("Node 2 was unexpectedly locked")
	}

	wg.Wait()

	// After Action A finishes, Node 1 must be unlocked too
	_, busy1 := orchestrator.activeNodes.Load("1")
	if busy1 {
		t.Errorf("Node 1 remained locked after action completion")
	}
}

// TestOrchestratorTimeoutLockRelease verifies that when an action times out,
// the deferred lock release ensures target nodes are freed for subsequent actions.
func TestOrchestratorTimeoutLockRelease(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052", Status: StatusOnline}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{
		Stdout: "long command\n",
		Delay:  500 * time.Millisecond,
	}

	orchestrator := NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "ubuntu", "~/.ssh/id_rsa", "/repo")

	// Execute with 50ms context timeout
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	record, err := orchestrator.Execute(shortCtx, ActionRequest{
		ActionType:     ActionCustom,
		CustomCommand:  "sleep 10",
		TargetNodeIDs:  []string{"0"},
		TimeoutSeconds: 1,
	}, nil)

	if err != nil {
		// execute error or context deadline exceeded
	}
	_ = record

	// Verify node 0 is completely unlocked
	_, busy := orchestrator.activeNodes.Load("0")
	if busy {
		t.Errorf("expected node 0 lock to be released following timeout")
	}

	// Immediately execute another action on node 0; should acquire lock successfully
	mockSSH.Default = MockSSHResponse{Stdout: "fast\n", ExitCode: 0}
	rec2, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionCustom,
		CustomCommand: "echo fast",
		TargetNodeIDs: []string{"0"},
	}, nil)

	if err != nil {
		t.Fatalf("subsequent action failed: %v", err)
	}
	if rec2.Status != "success" {
		t.Errorf("expected success for subsequent action, got %s", rec2.Status)
	}
}

// TestCommandHistoryCorruptedFile verifies that if command_history.json contains invalid JSON,
// NewCommandHistory falls back gracefully to an empty history without panicking.
func TestCommandHistoryCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	corruptFile := filepath.Join(tmpDir, "corrupted_history.json")

	if err := os.WriteFile(corruptFile, []byte("{invalid json truncated..."), 0644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	ch := NewCommandHistory(10, corruptFile)
	if ch.Count() != 0 {
		t.Errorf("expected 0 records after corrupted file load, got %d", ch.Count())
	}

	// Push should still succeed and write valid JSON
	ch.Push(CommandRecord{
		ID:        "act-recovered",
		Command:   "echo recovered",
		Timestamp: time.Now().Unix(),
		Status:    "success",
	})

	if ch.Count() != 1 {
		t.Errorf("expected 1 record after push, got %d", ch.Count())
	}
}

// TestCommandHistoryConcurrentStress verifies thread-safety of CommandHistory
// under simultaneous reads, updates, and pushes.
func TestCommandHistoryConcurrentStress(t *testing.T) {
	ch := NewCommandHistory(50, "")
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := fmt.Sprintf("act-%d-%d", workerID, j)
				ch.Push(CommandRecord{
					ID:        id,
					Command:   "echo test",
					Timestamp: time.Now().Unix(),
					Status:    "running",
				})
				ch.Update(CommandRecord{
					ID:         id,
					Status:     "success",
					DurationMs: 10,
				})
				_ = ch.GetAll()
			}
		}(i)
	}

	wg.Wait()

	if ch.Count() > 50 {
		t.Errorf("expected capacity capped at 50, got %d", ch.Count())
	}
}
