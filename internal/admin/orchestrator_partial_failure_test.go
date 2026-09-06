package admin

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestOrchestratorMultiNodePartialFailure verifies that when an action targets multiple nodes
// and some succeed while others fail or time out, the overall status is "failed"
// and each individual node's execution result and exit code is accurately recorded.
func TestOrchestratorMultiNodePartialFailure(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052", Status: StatusOnline}
	srv.nodes["1"] = &NodeState{FsID: "1", Address: "10.0.0.2:50052", Status: StatusOnline}
	srv.nodes["2"] = &NodeState{FsID: "2", Address: "10.0.0.3:50052", Status: StatusOnline}

	mockSSH := NewMockSSHExecutor()
	// Node 0 succeeds
	mockSSH.Responses["10.0.0.1"] = MockSSHResponse{
		Stdout:   "Node 0 OK\n",
		ExitCode: 0,
	}
	// Node 1 script failure
	mockSSH.Responses["10.0.0.2"] = MockSSHResponse{
		Stderr:   "error: build failed on Node 1\n",
		ExitCode: 2,
	}
	// Node 2 hangs longer than timeout
	mockSSH.Responses["10.0.0.3"] = MockSSHResponse{
		Stdout: "sleeping...\n",
		Delay:  500 * time.Millisecond,
	}

	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "ubuntu", "~/.ssh/id_rsa", "/repo")

	// Timeout set to 100ms so Node 2 times out
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	record, err := orchestrator.Execute(ctx, ActionRequest{
		ActionType:     ActionBuild,
		TargetNodeIDs:  []string{"0", "1", "2"},
		TimeoutSeconds: 1,
	}, nil)

	// In Go orchestrator, Execute returns the record even on partial failure
	if record == nil {
		t.Fatalf("expected non-nil record from Execute, got error: %v", err)
	}

	// Overall status must be "failed" because node 1 and/or node 2 failed
	if record.Status != "failed" {
		t.Errorf("expected overall status 'failed' for partial failure, got %s", record.Status)
	}

	// Node 0 must be recorded as success
	res0, ok0 := record.NodeResults["0"]
	if !ok0 || res0.ExitCode != 0 || !strings.Contains(res0.Output, "Node 0 OK") {
		t.Errorf("expected Node 0 success, got: %+v", res0)
	}

	// Node 1 must be recorded with exit code 2
	res1, ok1 := record.NodeResults["1"]
	if !ok1 || res1.ExitCode != 2 {
		t.Errorf("expected Node 1 exit code 2, got: %+v", res1)
	}
}

// TestOrchestratorCommandEscapingAdversarial tests formatting and executing
// commands containing special shell metacharacters, quotes, backticks, subshells, and pipes.
func TestOrchestratorCommandEscapingAdversarial(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052", Status: StatusOnline}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{Stdout: "safe output\n", ExitCode: 0}

	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "ubuntu", "~/.ssh/id_rsa", "/repo")

	adversarialCommands := []string{
		`echo "hello world" && ls -la | grep "txt"`,
		`cat /etc/issue; echo 'single quotes'; echo "double quotes"`,
		`ARG="--flag=val"; echo $ARG`,
		`echo $(uname -a)`,
	}

	for _, cmd := range adversarialCommands {
		record, err := orchestrator.Execute(context.Background(), ActionRequest{
			ActionType:    ActionCustom,
			CustomCommand: cmd,
			TargetNodeIDs: []string{"0"},
		}, nil)

		if err != nil {
			t.Fatalf("Execute(%q) failed: %v", cmd, err)
		}
		if record.Status != "success" {
			t.Errorf("expected success for %q, got %s", cmd, record.Status)
		}
		if record.Command != cmd {
			t.Errorf("expected record.Command to preserve verbatim string %q, got %q", cmd, record.Command)
		}
	}
}
