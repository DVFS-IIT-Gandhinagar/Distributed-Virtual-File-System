package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- RingBuffer ---

func TestRingBuffer_NewZeroOrNegative(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on negative size")
		}
	}()
	_ = NewRingBuffer(-1)
}

func TestRingBuffer_ConcurrentPushAndGetLast(t *testing.T) {
	rb := NewRingBuffer(10)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			rb.Push(Snapshot{Timestamp: int64(val)})
			_, _ = rb.GetLast()
		}(i)
	}
	wg.Wait()
	if rb.count != 10 {
		t.Errorf("expected count 10, got %d", rb.count)
	}
}

func TestRingBuffer_GetLastCorrectSnapshot(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Push(Snapshot{Timestamp: 1})
	rb.Push(Snapshot{Timestamp: 2})
	rb.Push(Snapshot{Timestamp: 3})
	rb.Push(Snapshot{Timestamp: 4}) // Overwrites 1

	last, ok := rb.GetLast()
	if !ok || last.Timestamp != 4 {
		t.Errorf("expected last to be 4, got %v (ok=%v)", last.Timestamp, ok)
	}
}

func TestRingBuffer_JSONRoundTrip(t *testing.T) {
	s := Snapshot{Timestamp: 123456789, WriteThroughputBps: 1.5}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if out.Timestamp != 123456789 || out.WriteThroughputBps != 1.5 {
		t.Errorf("mismatch after round trip: %v", out)
	}
}

// --- State ---

func TestState_LoadMetaState_FallbackPaths(t *testing.T) {
	tmpDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(cwd)

	// Create valid file at fallback location
	fbData := `{"next_fs_id": 99}`
	if err := os.WriteFile("./metaserver_state.json", []byte(fbData), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state, err := LoadMetaState("nonexistent.json")
	if err != nil {
		t.Fatalf("expected fallback to work, got err: %v", err)
	}
	if state.NextFsID != 99 {
		t.Errorf("expected NextFsID 99, got %d", state.NextFsID)
	}
}

func TestState_LoadMetaState_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.json")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := LoadMetaState(path)
	if err == nil {
		t.Fatal("expected decode error on empty file")
	}
}

// --- History ---

func TestHistory_CapacityDefaults(t *testing.T) {
	h0 := NewCommandHistory(0, "")
	if h0.capacity != 100 {
		t.Errorf("expected capacity 100, got %d", h0.capacity)
	}
	hNeg := NewCommandHistory(-1, "")
	if hNeg.capacity != 100 {
		t.Errorf("expected capacity 100, got %d", hNeg.capacity)
	}
}

func TestHistory_GetByID_Nonexistent(t *testing.T) {
	h := NewCommandHistory(10, "")
	_, ok := h.GetByID("nonexistent")
	if ok {
		t.Error("expected ok=false")
	}
}

func TestHistory_Update_Nonexistent(t *testing.T) {
	h := NewCommandHistory(10, "")
	// Should be no-op
	h.Update(CommandRecord{ID: "nonexistent"})
	if h.Count() != 0 {
		t.Errorf("expected count 0, got %d", h.Count())
	}
}

func TestHistory_Update_Wrapped(t *testing.T) {
	h := NewCommandHistory(2, "")
	h.Push(CommandRecord{ID: "1", Status: "running"})
	h.Push(CommandRecord{ID: "2", Status: "running"})
	h.Push(CommandRecord{ID: "3", Status: "running"}) // Overwrites 1

	h.Update(CommandRecord{ID: "3", Status: "success"})
	
	rec, ok := h.GetByID("3")
	if !ok || rec.Status != "success" {
		t.Errorf("update failed after wrap")
	}
}

func TestHistory_Load_MoreThanCapacity(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "history.json")
	
	records := []CommandRecord{
		{ID: "1"}, {ID: "2"}, {ID: "3"},
	}
	b, _ := json.Marshal(records)
	os.WriteFile(path, b, 0644)

	h := NewCommandHistory(2, path) // capacity 2, file has 3
	if h.Count() != 2 {
		t.Errorf("expected count 2, got %d", h.Count())
	}
}

// --- Server ---

func TestServer_NewAdminServer_Defaults(t *testing.T) {
	srv := NewAdminServer("", "")
	if srv.httpClient == nil {
		t.Fatal("expected non-nil httpClient")
	}
	if srv.httpClient.Timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", srv.httpClient.Timeout)
	}
	if srv.nodes == nil {
		t.Error("expected non-nil nodes map")
	}
	if srv.users == nil {
		t.Error("expected non-nil users map")
	}
}

func TestServer_Getters(t *testing.T) {
	srv := NewAdminServer("", "")
	if srv.Orchestrator() != srv.orchestrator {
		t.Error("Orchestrator() returned wrong value")
	}
	if srv.History() != srv.history {
		t.Error("History() returned wrong value")
	}
}

func TestServer_ComputeNodeStatus(t *testing.T) {
	tests := []struct{
		lastSeen int64
		cpu      float64
		mem      float64
		disk     float64
		expected NodeStatus
	}{
		{0, 0, 0, 0, StatusOffline},
		{time.Now().Unix() - 60, 0, 0, 0, StatusOffline},
		{time.Now().Unix(), 86, 0, 0, StatusCritical},
		{time.Now().Unix(), 0, 0, 96, StatusCritical},
		{time.Now().Unix(), 76, 0, 0, StatusDegraded},
		{time.Now().Unix(), 0, 0, 91, StatusDegraded},
		{time.Now().Unix(), 66, 0, 0, StatusWarning},
		{time.Now().Unix(), 0, 0, 81, StatusWarning},
		{time.Now().Unix(), 50, 0, 50, StatusOnline},
	}
	for i, tt := range tests {
		m := &FileserverMetrics{
			CPUTempCelsius: tt.cpu,
			MemUsagePercent: tt.mem,
			DiskUsagePercent: tt.disk,
		}
		got := computeNodeStatus(m, tt.lastSeen)
		if got != tt.expected {
			t.Errorf("test %d: expected %s, got %s", i, tt.expected, got)
		}
	}
}

// --- Poller ---

func TestPoller_deriveMetricsURL(t *testing.T) {
	url := deriveMetricsURL("127.0.0.1:50051")
	if url != "http://127.0.0.1:9051/metrics" {
		t.Errorf("unexpected url: %s", url)
	}
	
	url2 := deriveMetricsURL("127.0.0.1:65536") // Out of bounds > 65535
	if url2 != "" { 
		t.Errorf("unexpected url for out of bounds port: %s", url2)
	}
	
	url3 := deriveMetricsURL("bad")
	if url3 != "" {
		t.Errorf("unexpected url for bad addr: %s", url3)
	}
}

func TestPoller_pollNode_Errors(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["test"] = &NodeState{MetricsURL: ""}
	srv.pollNode(srv.nodes["test"])
	if srv.nodes["test"].Status != StatusOffline {
		t.Errorf("expected Offline, got %s", srv.nodes["test"].Status)
	}
	
	// HTTP 500
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()
	srv.nodes["test2"] = &NodeState{MetricsURL: ts.URL}
	srv.pollNode(srv.nodes["test2"])
	if srv.nodes["test2"].Status != StatusOffline {
		t.Errorf("expected Offline on 500, got %s", srv.nodes["test2"].Status)
	}

	// Malformed JSON
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{bad json"))
	}))
	defer ts2.Close()
	srv.nodes["test3"] = &NodeState{MetricsURL: ts2.URL}
	srv.pollNode(srv.nodes["test3"])
	if srv.nodes["test3"].Status != StatusOffline {
		t.Errorf("expected Offline on bad json, got %s", srv.nodes["test3"].Status)
	}
}

func TestPoller_updateNodeFailure(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["test"] = &NodeState{
		Status: StatusOnline,
		WriteThroughputBps: 100,
	}
	srv.updateNodeFailure(srv.nodes["test"])
	if srv.nodes["test"].Status != StatusOffline {
		t.Error("expected Offline")
	}
	if srv.nodes["test"].WriteThroughputBps != 0 {
		t.Error("expected zeroed rates")
	}
}

func TestPoller_refreshNodes(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")
	stateData := `{"fileservers": {"1": {"address": "127.0.0.1:8000"}}, "users": {"u1": 1}}`
	os.WriteFile(statePath, []byte(stateData), 0644)
	
srv := NewAdminServer(statePath, "")
	srv.refreshNodes()
	
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	if len(srv.nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(srv.nodes))
	}
	if len(srv.users) != 1 {
		t.Errorf("expected 1 user, got %d", len(srv.users))
	}
}

// --- Handlers ---

func checkMethodAllowed(t *testing.T, handler http.HandlerFunc, method, path string, expectStatus int) {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != expectStatus {
		t.Errorf("%s %s: expected %d, got %d", method, path, expectStatus, rec.Code)
	}
}

func TestHandlers_MethodsAllowed(t *testing.T) {
	srv := NewAdminServer("", "")
	
	checkMethodAllowed(t, srv.handleHistory, "POST", "/api/history", http.StatusMethodNotAllowed)
	checkMethodAllowed(t, srv.handleUsers, "POST", "/api/users", http.StatusMethodNotAllowed)
	checkMethodAllowed(t, srv.handlePerformance, "POST", "/api/performance", http.StatusMethodNotAllowed)
	checkMethodAllowed(t, srv.handlePerformanceExport, "POST", "/api/performance/export", http.StatusMethodNotAllowed)
	checkMethodAllowed(t, srv.handleActionExecute, "GET", "/api/actions/execute", http.StatusMethodNotAllowed)
}

func TestHandlers_ActionExecute_Errors(t *testing.T) {
	srv := NewAdminServer("", "")
	
	// invalid json
	req := httptest.NewRequest("POST", "/api/actions/execute", bytes.NewBufferString("{bad"))
	rec := httptest.NewRecorder()
	srv.handleActionExecute(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad json, got %d", rec.Code)
	}

	// nil orchestrator
	srv.SetOrchestrator(nil)
	req2 := httptest.NewRequest("POST", "/api/actions/execute", bytes.NewBufferString(`{"action":"logs"}`))
	rec2 := httptest.NewRecorder()
	srv.handleActionExecute(rec2, req2)
	if rec2.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for nil orchestrator, got %d", rec2.Code)
	}
}

func TestHandlers_ActionPresets_NilOrchestrator(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.SetOrchestrator(nil)
	req := httptest.NewRequest("GET", "/api/actions/presets", nil)
	rec := httptest.NewRecorder()
	srv.handleActionPresets(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestHandlers_ActionHistory_NilHistory(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.SetHistory(nil)
	req := httptest.NewRequest("GET", "/api/actions/history", nil)
	rec := httptest.NewRecorder()
	srv.handleActionHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "[]\n" {
		t.Errorf("expected empty array JSON, got %s", string(body))
	}
}

// --- SSH ---

func TestSSH_ExpandPath(t *testing.T) {
	// mocking user home for test
	home, err := os.UserHomeDir()
	if err == nil {
		exp1, _ := expandPath("~/foo")
		if exp1 != filepath.Join(home, "foo") {
			t.Errorf("expected %s, got %s", filepath.Join(home, "foo"), exp1)
		}
		
		exp2, _ := expandPath("~")
		if exp2 != home {
			t.Errorf("expected %s, got %s", home, exp2)
		}
	}
	
	exp3, _ := expandPath("/absolute/path")
	if exp3 != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %s", exp3)
	}
}

func TestSSH_MockExecutor_ContextCancel(t *testing.T) {
	mock := NewMockSSHExecutor()
	mock.Default = MockSSHResponse{Delay: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	
	_, err := mock.Run(ctx, "host", 22, "user", "key", "cmd", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- Actions ---

func TestActions_FormatCommand(t *testing.T) {
	orch := NewOrchestrator(NewAdminServer("", ""), nil, nil, "", "", "/repo", 22)
	req := ActionRequest{ActionType: "unknown"}
	cmd := orch.FormatCommand(&req, "1", nil)
	if cmd != "" {
		t.Errorf("expected empty cmd for unknown action, got %s", cmd)
	}
	
	req2 := ActionRequest{ActionType: ActionRestart, CustomCommand: "echo ok"}
	cmd2 := orch.FormatCommand(&req2, "1", nil)
	if cmd2 != "echo ok" {
		t.Errorf("expected fallback command, got %s", cmd2)
	}
	
	req3 := ActionRequest{ActionType: ActionLogs, LogLines: 0}
	cmd3 := orch.FormatCommand(&req3, "1", nil)
	if !strings.Contains(cmd3, "journalctl") {
		t.Errorf("expected journalctl, got %s", cmd3)
	}
}

func TestActions_Execute_NilSSH(t *testing.T) {
	orch := NewOrchestrator(NewAdminServer("", ""), nil, nil, "", "", "/repo") // nil ssh executor
	_, err := orch.Execute(context.Background(), ActionRequest{TargetNodeIDs: []string{"1"}}, func(e ActionEvent){})
	if err == nil {
		t.Errorf("expected error for nil ssh, got nil")
	}
}

func TestActions_EventWriter_NilCallback(t *testing.T) {
	// Should not panic
	ew := &eventWriter{onEvent: nil, nodeID: "node1", actionID: "act1"}
	n, err := ew.Write([]byte("test"))
	if err != nil || n != 4 {
		t.Errorf("expected 4 and nil err, got %d, %v", n, err)
	}
}
