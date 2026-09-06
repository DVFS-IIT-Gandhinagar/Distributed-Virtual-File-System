package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogTail_MockSSHExecution(t *testing.T) {
	srv := &AdminServer{
		nodes: make(map[string]*NodeState),
	}

	node := &NodeState{
		FsID:        "0",
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "10.0.0.1:50052",
		Status:      StatusOnline,
	}
	srv.nodes["0"] = node

	mockSSH := NewMockSSHExecutor()
	mockSSH.Responses["10.0.0.1"] = MockSSHResponse{
		ExitCode: 0,
		Stdout:   "2026/09/06 18:00:00 [FILESERVER] Serving gRPC on :50052\n2026/09/06 18:00:01 [FILESERVER] Metrics on :9052\n",
	}

	orchestrator := NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "ubuntu", "", "")
	srv.SetOrchestrator(orchestrator)

	// Fetch logs
	resp, err := srv.FetchNodeLogs(context.Background(), "0", "fileserver", 50, "journalctl")
	if err != nil {
		t.Fatalf("FetchNodeLogs failed: %v", err)
	}

	if resp.NodeID != "0" {
		t.Errorf("expected NodeID '0', got %s", resp.NodeID)
	}
	if !strings.Contains(resp.Content, "Serving gRPC on :50052") {
		t.Errorf("expected log output to contain gRPC message, got: %s", resp.Content)
	}
}

func TestLogTail_HTTPHandler(t *testing.T) {
	srv := &AdminServer{
		nodes: make(map[string]*NodeState),
	}
	node := &NodeState{
		FsID:        "0",
		DisplayName: "FS-1",
		Address:     "10.0.0.1:50052",
		Status:      StatusOnline,
	}
	srv.nodes["0"] = node

	mockSSH := NewMockSSHExecutor()
	mockSSH.Responses["10.0.0.1"] = MockSSHResponse{
		ExitCode: 0,
		Stdout:   "[INFO] Log tail output",
	}
	srv.SetOrchestrator(NewOrchestrator(srv, mockSSH, NewCommandHistory(10, ""), "", "", ""))

	req := httptest.NewRequest(http.MethodGet, "/api/logs/tail?node=0&lines=50", nil)
	rec := httptest.NewRecorder()

	srv.handleLogTail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "[INFO] Log tail output") {
		t.Errorf("expected response to contain log content, got: %s", rec.Body.String())
	}
}

func TestLogTail_NodeNotFound(t *testing.T) {
	srv := &AdminServer{
		nodes: make(map[string]*NodeState),
	}
	_, err := srv.FetchNodeLogs(context.Background(), "nonexistent", "fileserver", 50, "journalctl")
	if err == nil {
		t.Fatalf("expected error for nonexistent node")
	}
}
