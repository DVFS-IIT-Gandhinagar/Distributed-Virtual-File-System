package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAuthManager_VerifyPassword(t *testing.T) {
	// SHA-256 of "secretpass"
	rawSum := sha256.Sum256([]byte("secretpass"))
	expectedHash := hex.EncodeToString(rawSum[:])

	am := &AuthManager{
		hash:     expectedHash,
		sessions: make(map[string]time.Time),
	}

	// Correct password
	if !am.VerifyPassword("secretpass") {
		t.Errorf("expected VerifyPassword to return true for correct password")
	}

	// Incorrect password
	if am.VerifyPassword("wrongpass") {
		t.Errorf("expected VerifyPassword to return false for incorrect password")
	}

	// Empty password
	if am.VerifyPassword("") {
		t.Errorf("expected VerifyPassword to return false for empty password")
	}

	// Unset hash
	emptyAm := &AuthManager{sessions: make(map[string]time.Time)}
	if emptyAm.VerifyPassword("secretpass") {
		t.Errorf("expected VerifyPassword to return false when hash is not set")
	}
}

func TestAuthManager_LoadEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := []byte("# Comment line\nTEST_VAR_AUTH=hello_world\nQUOTED_VAR=\"quoted_value\"\n")
	if err := os.WriteFile(envPath, content, 0644); err != nil {
		t.Fatalf("failed to write test .env: %v", err)
	}

	LoadEnv(envPath)

	if os.Getenv("TEST_VAR_AUTH") != "hello_world" {
		t.Errorf("expected TEST_VAR_AUTH=hello_world, got %s", os.Getenv("TEST_VAR_AUTH"))
	}
	if os.Getenv("QUOTED_VAR") != "quoted_value" {
		t.Errorf("expected QUOTED_VAR=quoted_value, got %s", os.Getenv("QUOTED_VAR"))
	}
}

func TestAuthManager_SessionLifecycle(t *testing.T) {
	am := &AuthManager{
		sessions: make(map[string]time.Time),
	}

	token := am.CreateSession()
	if len(token) != 64 {
		t.Fatalf("expected 64 hex char token, got %d", len(token))
	}

	// 1. Authenticate via Bearer header
	req1 := httptest.NewRequest("GET", "/api/users", nil)
	req1.Header.Set("Authorization", "Bearer "+token)
	if !am.IsAuthenticated(req1) {
		t.Errorf("expected IsAuthenticated to return true with Bearer header")
	}

	// 2. Authenticate via Cookie
	req2 := httptest.NewRequest("GET", "/api/users", nil)
	req2.AddCookie(&http.Cookie{Name: adminCookieName, Value: token})
	if !am.IsAuthenticated(req2) {
		t.Errorf("expected IsAuthenticated to return true with Cookie")
	}

	// 3. Authenticate via Query parameter
	req3 := httptest.NewRequest("GET", "/ws/actions?token="+token, nil)
	if !am.IsAuthenticated(req3) {
		t.Errorf("expected IsAuthenticated to return true with query token")
	}

	// 4. Invalidate session
	am.RevokeSession(token)
	if am.IsAuthenticated(req1) {
		t.Errorf("expected IsAuthenticated to return false after session revocation")
	}
}

func TestAuthManager_ClusterSanitization(t *testing.T) {
	srv := NewAdminServer("", "")
	sum := sha256.Sum256([]byte("adminpass"))
	srv.authManager.SetHash(hex.EncodeToString(sum[:]))

	// Add test node with users and active connections
	srv.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "127.0.0.1:50052",
		Status:  StatusOnline,
		Metrics: &FileserverMetrics{
			DiskTotalBytes:   1000000,
			DiskUsedBytes:    500000,
			UsersAssigned:    2,
			ActiveUsers:      []string{"alice", "bob"},
			PerUserStorage:   map[string]uint64{"alice": 200000, "bob": 300000},
			PerUserQuota:     map[string]uint64{"alice": 500000, "bob": 500000},
			UptimeSeconds:    3600,
			CPUTempCelsius:   45.5,
			CPUUsagePercent:  12.3,
		},
		WriteMbps: 15.5,
		ReadMbps:  42.0,
	}
	srv.users["alice"] = "0"
	srv.users["bob"] = "0"

	// 1. Unauthenticated request: user data MUST be stripped, health/perf intact
	unauthReq := httptest.NewRequest("GET", "/api/cluster", nil)
	unauthRec := httptest.NewRecorder()
	srv.handleCluster(unauthRec, unauthReq)

	if unauthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", unauthRec.Code)
	}

	var publicResp ClusterResponse
	if err := json.NewDecoder(unauthRec.Body).Decode(&publicResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify user data is stripped
	if len(publicResp.Users) != 0 {
		t.Errorf("expected public users map to be empty, got %v", publicResp.Users)
	}
	if publicResp.TotalUsers != 0 {
		t.Errorf("expected public total_users to be 0, got %d", publicResp.TotalUsers)
	}
	if publicResp.OnlineUsers != 0 {
		t.Errorf("expected public online_users to be 0, got %d", publicResp.OnlineUsers)
	}

	if len(publicResp.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(publicResp.Nodes))
	}
	nodeMetrics := publicResp.Nodes[0].Metrics
	if len(nodeMetrics.PerUserStorage) != 0 {
		t.Errorf("expected public per_user_storage to be empty, got %v", nodeMetrics.PerUserStorage)
	}
	if len(nodeMetrics.ActiveUsers) != 0 {
		t.Errorf("expected public active_users to be empty, got %v", nodeMetrics.ActiveUsers)
	}
	if nodeMetrics.UsersAssigned != 0 {
		t.Errorf("expected public users_assigned to be 0, got %d", nodeMetrics.UsersAssigned)
	}

	// Verify health and perf telemetry ARE intact
	if publicResp.Nodes[0].WriteMbps != 15.5 || publicResp.Nodes[0].ReadMbps != 42.0 {
		t.Errorf("expected throughputs preserved, got write=%.1f, read=%.1f", publicResp.Nodes[0].WriteMbps, publicResp.Nodes[0].ReadMbps)
	}
	if nodeMetrics.UptimeSeconds != 3600 {
		t.Errorf("expected uptime preserved, got %.0f", nodeMetrics.UptimeSeconds)
	}

	// 2. Authenticated request: full user data returned
	token := srv.authManager.CreateSession()
	authReq := httptest.NewRequest("GET", "/api/cluster", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	authRec := httptest.NewRecorder()
	srv.handleCluster(authRec, authReq)

	var adminResp ClusterResponse
	if err := json.NewDecoder(authRec.Body).Decode(&adminResp); err != nil {
		t.Fatalf("failed to decode admin response: %v", err)
	}

	if len(adminResp.Users) != 2 {
		t.Errorf("expected admin users map to have 2 entries, got %d", len(adminResp.Users))
	}
	if adminResp.TotalUsers != 2 {
		t.Errorf("expected admin total_users=2, got %d", adminResp.TotalUsers)
	}
	adminNodeMetrics := adminResp.Nodes[0].Metrics
	if len(adminNodeMetrics.PerUserStorage) != 2 {
		t.Errorf("expected admin per_user_storage to have 2 entries, got %d", len(adminNodeMetrics.PerUserStorage))
	}
	if len(adminNodeMetrics.ActiveUsers) != 2 {
		t.Errorf("expected admin active_users to have 2 entries, got %d", len(adminNodeMetrics.ActiveUsers))
	}
}

func TestAuthManager_ProtectedEndpoints401(t *testing.T) {
	srv := NewAdminServer("", "")
	sum := sha256.Sum256([]byte("mypassword"))
	srv.authManager.SetHash(hex.EncodeToString(sum[:]))

	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// 1. Unauthorized
	unauthReq := httptest.NewRequest("POST", "/api/actions/execute", nil)
	unauthRec := httptest.NewRecorder()
	handler(unauthRec, unauthReq)

	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", unauthRec.Code)
	}

	// 2. Authorized
	token := srv.authManager.CreateSession()
	authReq := httptest.NewRequest("POST", "/api/actions/execute", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	authRec := httptest.NewRecorder()
	handler(authRec, authReq)

	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", authRec.Code)
	}
}

func TestAuthManager_LoginLogoutAPI(t *testing.T) {
	srv := NewAdminServer("", "")
	sum := sha256.Sum256([]byte("secureadmin"))
	srv.authManager.SetHash(hex.EncodeToString(sum[:]))

	// 1. Bad password login
	badLoginBody := []byte(`{"password":"wrong"}`)
	badReq := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(badLoginBody))
	badRec := httptest.NewRecorder()
	srv.handleAuthLogin(badRec, badReq)

	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad password, got %d", badRec.Code)
	}

	// 2. Successful login
	goodLoginBody := []byte(`{"password":"secureadmin"}`)
	goodReq := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(goodLoginBody))
	goodRec := httptest.NewRecorder()
	srv.handleAuthLogin(goodRec, goodReq)

	if goodRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid login, got %d", goodRec.Code)
	}

	var loginResp authLoginResponse
	if err := json.NewDecoder(goodRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}
	if !loginResp.Success || loginResp.Token == "" {
		t.Fatalf("expected login success and non-empty token, got %+v", loginResp)
	}

	// Verify cookie was set
	cookies := goodRec.Result().Cookies()
	var foundCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == adminCookieName {
			foundCookie = c
			break
		}
	}
	if foundCookie == nil || foundCookie.Value != loginResp.Token {
		t.Errorf("expected admin cookie to match token, got %v", foundCookie)
	}

	// 3. Check auth status
	statusReq := httptest.NewRequest("GET", "/api/auth/status", nil)
	statusReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	statusRec := httptest.NewRecorder()
	srv.handleAuthStatus(statusRec, statusReq)

	if !strings.Contains(statusRec.Body.String(), `"authenticated":true`) {
		t.Errorf("expected authenticated:true in status, got %s", statusRec.Body.String())
	}

	// 4. Logout
	logoutReq := httptest.NewRequest("POST", "/api/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	logoutRec := httptest.NewRecorder()
	srv.handleAuthLogout(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for logout, got %d", logoutRec.Code)
	}

	// Verify token is no longer valid
	statusReq2 := httptest.NewRequest("GET", "/api/auth/status", nil)
	statusReq2.Header.Set("Authorization", "Bearer "+loginResp.Token)
	statusRec2 := httptest.NewRecorder()
	srv.handleAuthStatus(statusRec2, statusReq2)

	if !strings.Contains(statusRec2.Body.String(), `"authenticated":false`) {
		t.Errorf("expected authenticated:false after logout, got %s", statusRec2.Body.String())
	}
}

func TestWebSocket_CookieAuth(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.nodes["0"] = &NodeState{FsID: "0", Address: "10.0.0.1:50052"}

	mockSSH := NewMockSSHExecutor()
	mockSSH.Default = MockSSHResponse{Stdout: "Cookie Auth Success\n", ExitCode: 0}
	history := NewCommandHistory(10, "")
	orchestrator := NewOrchestrator(srv, mockSSH, history, "testuser", "testkey", "/repo")
	srv.SetOrchestrator(orchestrator)
	srv.SetHistory(history)

	am := NewAuthManager("dummyhash")
	wsHandler := NewWebSocketHandler(orchestrator, am)
	server := httptest.NewServer(wsHandler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// 1. Unauthenticated connection attempt should fail (401 Unauthorized)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel1()

	_, resp, err := websocket.Dial(ctx1, wsURL, nil)
	if err == nil {
		t.Fatalf("expected dial without auth cookie to fail, but it succeeded")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	// 2. Authenticated connection attempt with valid cookie should succeed
	token := am.CreateSession()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{adminCookieName + "=" + token},
		},
	}

	conn, _, err := websocket.Dial(ctx2, wsURL, opts)
	if err != nil {
		t.Fatalf("expected dial with valid cookie to succeed, got: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test completed")

	// Verify connection is functional by sending an action
	req := ActionRequest{
		ActionType:    ActionCustom,
		CustomCommand: "uptime",
		TargetNodeIDs: []string{"0"},
	}
	reqData, _ := json.Marshal(req)
	if err := conn.Write(ctx2, websocket.MessageText, reqData); err != nil {
		t.Fatalf("failed to write action request over authed ws: %v", err)
	}
}
