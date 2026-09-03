package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestActionRESTEndpoints(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{Stdout: "mock output\n", ExitCode: 0}
	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "testuser", "testkey", "/repo")
	srv.SetOrchestrator(orchestrator)
	srv.SetHistory(history)

	// 1. GET /api/actions/presets
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/actions/presets", nil)
	srv.handleActionPresets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var presets map[string]NodeRestartParams
	if err := json.Unmarshal(rec.Body.Bytes(), &presets); err != nil {
		t.Fatalf("failed to decode presets: %v", err)
	}
	if presets["0"].FsID != "0" {
		t.Errorf("expected FsID 0, got %s", presets["0"].FsID)
	}

	// 2. POST /api/actions/execute
	execPayload := ActionRequest{
		ActionType:    ActionCustom,
		CustomCommand: "echo hello",
		TargetNodeIDs: []string{"0"},
	}
	payloadBytes, _ := json.Marshal(execPayload)

	recExec := httptest.NewRecorder()
	reqExec := httptest.NewRequest("POST", "/api/actions/execute", bytes.NewReader(payloadBytes))
	srv.handleActionExecute(recExec, reqExec)
	if recExec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recExec.Code, recExec.Body.String())
	}

	var recResult CommandRecord
	if err := json.Unmarshal(recExec.Body.Bytes(), &recResult); err != nil {
		t.Fatalf("failed to decode execution record: %v", err)
	}
	if recResult.Status != "success" {
		t.Errorf("expected success status, got %s", recResult.Status)
	}

	// 3. GET /api/actions/history
	recHist := httptest.NewRecorder()
	reqHist := httptest.NewRequest("GET", "/api/actions/history", nil)
	srv.handleActionHistory(recHist, reqHist)
	if recHist.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recHist.Code)
	}
	var historyList []CommandRecord
	if err := json.Unmarshal(recHist.Body.Bytes(), &historyList); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(historyList) != 1 || historyList[0].ID != recResult.ID {
		t.Errorf("expected 1 history entry with matching ID, got %d", len(historyList))
	}
}

func TestWebSocketStreaming(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{Stdout: "Streaming chunk 1\n", ExitCode: 0}
	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "testuser", "testkey", "/repo")
	srv.SetOrchestrator(orchestrator)
	srv.SetHistory(history)

	wsHandler := NewWebSocketHandler(orchestrator)
	server := httptest.NewServer(wsHandler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket at %s: %v", wsURL, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test completed")

	// Send ActionRequest JSON
	req := ActionRequest{
		ActionType:    ActionCustom,
		CustomCommand: "uptime",
		TargetNodeIDs: []string{"0"},
	}
	reqData, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, reqData); err != nil {
		t.Fatalf("failed to write ActionRequest: %v", err)
	}

	// Read events until action_finished
	receivedOutput := false
	receivedFinished := false

	for !receivedFinished {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("error reading from websocket: %v", err)
		}

		var event ActionEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			t.Fatalf("failed to unmarshal event: %v", err)
		}

		if event.Type == "node_output" && strings.Contains(event.Chunk, "Streaming chunk 1") {
			receivedOutput = true
		}
		if event.Type == "action_finished" && event.Status == "success" {
			receivedFinished = true
		}
	}

	if !receivedOutput {
		t.Errorf("expected node_output event with streaming text")
	}
	if !receivedFinished {
		t.Errorf("expected action_finished event")
	}
}
