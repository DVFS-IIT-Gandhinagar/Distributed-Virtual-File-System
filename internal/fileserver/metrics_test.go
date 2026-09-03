package fileserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestCollectMetrics(t *testing.T) {
	fs := newTestFileServer(t)

	// Create user root
	_, err := fs.GetUserRoot("alice", "alice")
	if err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	m := fs.CollectMetrics()

	if m.UsersAssigned != 1 {
		t.Errorf("expected UsersAssigned=1, got %d", m.UsersAssigned)
	}

	if quota, ok := m.PerUserQuota["alice"]; !ok || quota != storageQuota {
		t.Errorf("expected PerUserQuota['alice']=%d, got %d (exists=%v)", storageQuota, quota, ok)
	}

	if _, ok := m.PerUserStorage["alice"]; !ok {
		t.Errorf("expected PerUserStorage for alice to exist")
	}

	if m.LastRestartUnix == 0 {
		t.Errorf("expected non-zero LastRestartUnix")
	}

	if m.UptimeSeconds < 0 {
		t.Errorf("expected positive UptimeSeconds, got %f", m.UptimeSeconds)
	}
}

func TestMetricsHTTP(t *testing.T) {
	fs := newTestFileServer(t)

	// Get a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	fs.StartMetricsHTTP(port)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait briefly for HTTP server to become ready
	var ready bool
	for i := 0; i < 20; i++ {
		resp, err := http.Get(baseURL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Metrics HTTP server failed to start within timeout")
	}

	// Test /metrics endpoint
	resp, err := http.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var m Metrics
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("failed to decode metrics JSON: %v", err)
	}

	if m.LastRestartUnix == 0 {
		t.Errorf("expected valid LastRestartUnix in metrics")
	}

	// Test /health endpoint
	healthResp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("expected /health 200, got %d", healthResp.StatusCode)
	}

	// Test method not allowed on /metrics
	postResp, err := http.Post(baseURL+"/metrics", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /metrics failed: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", postResp.StatusCode)
	}
}
