package admin

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOrchestratorExecuteConcurrent(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "10.0.0.1:50052",
		Status:  StatusOnline,
	}
	srv.nodes["1"] = &NodeState{
		FsID:    "1",
		Address: "10.0.0.2:50052",
		Status:  StatusOnline,
	}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{
		Stdout:   "Command finished successfully\n",
		ExitCode: 0,
	}

	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "ubuntu", "~/.ssh/id_rsa", "~/dvfs")
	srv.SetOrchestrator(orchestrator)
	srv.SetHistory(history)

	var events []ActionEvent
	var eventsMu sync.Mutex
	onEvent := func(e ActionEvent) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
	}

	req := ActionRequest{
		ActionType:    ActionPull,
		TargetNodeIDs: []string{"0", "1"},
	}

	record, err := orchestrator.Execute(context.Background(), req, onEvent)
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}

	if record.Status != "success" {
		t.Errorf("expected success, got %s", record.Status)
	}
	if len(record.NodeResults) != 2 {
		t.Fatalf("expected 2 node results, got %d", len(record.NodeResults))
	}

	// Verify both nodes executed
	if record.NodeResults["0"].ExitCode != 0 || !strings.Contains(record.NodeResults["0"].Output, "Command finished successfully") {
		t.Errorf("unexpected result on node 0: %+v", record.NodeResults["0"])
	}
	if record.NodeResults["1"].ExitCode != 0 || !strings.Contains(record.NodeResults["1"].Output, "Command finished successfully") {
		t.Errorf("unexpected result on node 1: %+v", record.NodeResults["1"])
	}

	// Verify history updated
	if history.Count() != 1 {
		t.Errorf("expected 1 history record, got %d", history.Count())
	}

	// Verify event stream
	eventsMu.Lock()
	defer eventsMu.Unlock()
	hasActionStarted := false
	hasActionFinished := false
	nodeStarts := 0
	nodeOutputs := 0

	for _, e := range events {
		switch e.Type {
		case "action_started":
			hasActionStarted = true
		case "action_finished":
			hasActionFinished = true
		case "node_started":
			nodeStarts++
		case "node_output":
			nodeOutputs++
		}
	}

	if !hasActionStarted || !hasActionFinished {
		t.Errorf("missing action lifecycle events: started=%v, finished=%v", hasActionStarted, hasActionFinished)
	}
	if nodeStarts != 2 {
		t.Errorf("expected 2 node_started events, got %d", nodeStarts)
	}
	if nodeOutputs < 2 {
		t.Errorf("expected at least 2 node_output events, got %d", nodeOutputs)
	}
}

func TestOrchestratorValidation(t *testing.T) {
	srv := NewAdminServer("", "")
	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, NewMockSSHExecutor(), history, "", "", "")

	// 1. Empty targets
	_, err := orchestrator.Execute(context.Background(), ActionRequest{ActionType: ActionPull}, nil)
	if err == nil || !strings.Contains(err.Error(), "no target nodes") {
		t.Errorf("expected no target nodes error, got %v", err)
	}

	// 2. Unknown targets
	_, err = orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionPull,
		TargetNodeIDs: []string{"nonexistent"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "none of the specified target nodes were found") {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestOrchestratorPresets(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "192.168.1.100:50052"}
	srv.nodes["1"] = &NodeState{FsID: "1", Address: "192.168.1.101:50053"}

	orchestrator := NewOrchestrator(srv, NewMockSSHExecutor(), NewCommandHistory(10, ""), "", "", "")
	presets := orchestrator.GetPresets()

	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
	if presets["0"].Port != 50052 || presets["0"].Host != "192.168.1.100" {
		t.Errorf("unexpected preset 0: %+v", presets["0"])
	}
	if presets["1"].Port != 50053 || presets["1"].Host != "192.168.1.101" {
		t.Errorf("unexpected preset 1: %+v", presets["1"])
	}
}

func TestOrchestratorConcurrencyLock(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	// Add delay so action stays active
	mockSSH.Default = MockSSHResponse{
		Stdout: "sleeping\n",
		Delay:  200 * time.Millisecond,
	}

	orchestrator := NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "", "", "")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = orchestrator.Execute(context.Background(), ActionRequest{
			ActionType:    ActionPull,
			TargetNodeIDs: []string{"0"},
		}, nil)
	}()

	// Give goroutine a moment to acquire lock
	time.Sleep(20 * time.Millisecond)

	// Attempt concurrent action on node 0
	_, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionBuild,
		TargetNodeIDs: []string{"0"},
	}, nil)

	if err == nil || !strings.Contains(err.Error(), "currently executing action") {
		t.Errorf("expected concurrency lock error, got %v", err)
	}

	wg.Wait()
}

func TestOrchestratorTimeout(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{
		Stdout: "long running\n",
		Delay:  500 * time.Millisecond,
	}

	orchestrator := NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "", "", "")

	// Set timeout to 1 second, but use a cancelled/short context or TimeoutSeconds
	record, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:     ActionPull,
		TargetNodeIDs:  []string{"0"},
		TimeoutSeconds: 1, // 1s timeout
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Status != "success" {
		t.Errorf("expected success since delay 500ms < timeout 1s, got %s", record.Status)
	}
}

func TestOrchestratorCommandStringPopulation(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "ubuntu", "~/.ssh/id_rsa", "/repo")

	// Execute ActionPull with empty CustomCommand
	record, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionPull,
		TargetNodeIDs: []string{"0"},
		GitBranch:     "develop",
	}, nil)

	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	// Verify record.Command is NOT empty, but populated with actual git pull command
	if !strings.Contains(record.Command, "git -C /repo pull origin develop") {
		t.Errorf("expected record.Command to contain git pull develop, got '%s'", record.Command)
	}

	// Verify history stored the populated command
	all := history.GetAll()
	if len(all) != 1 || !strings.Contains(all[0].Command, "git -C /repo pull origin develop") {
		t.Errorf("expected history to contain formatted command, got '%s'", all[0].Command)
	}
}

func TestOrchestratorCustomSSHPort(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	orchestrator := NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "ubuntu", "~/.ssh/id_rsa", "/repo", 2222)

	_, err := orchestrator.Execute(context.Background(), ActionRequest{
		ActionType:    ActionCustom,
		CustomCommand: "uptime",
		TargetNodeIDs: []string{"0"},
		SSHPort:       22022,
	}, nil)

	if err != nil {
		t.Fatalf("execute error: %v", err)
	}

	if len(mockSSH.Calls) != 1 || mockSSH.Calls[0].Port != 22022 {
		t.Errorf("expected SSH call with port 22022, got %+v", mockSSH.Calls)
	}
}

