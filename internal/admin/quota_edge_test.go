package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAdminUserQuotaEdgeCases tests edge cases and boundary conditions for PUT /api/users/{username}/quota.
func TestAdminUserQuotaEdgeCases(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.users["alice"] = "0"
	srv.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "127.0.0.1:50052",
		Status:  StatusOnline,
	}

	// 1. Invalid method (GET instead of PUT/POST)
	reqGet := httptest.NewRequest(http.MethodGet, "/api/users/alice/quota", nil)
	wGet := httptest.NewRecorder()
	srv.handleUserQuota(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 MethodNotAllowed for GET, got %d", wGet.Code)
	}

	// 2. Malformed URL path (missing quota suffix)
	reqBadPath := httptest.NewRequest(http.MethodPut, "/api/users/alice", strings.NewReader(`{"quota_bytes":1000}`))
	wBadPath := httptest.NewRecorder()
	srv.handleUserQuota(wBadPath, reqBadPath)
	if wBadPath.Code != http.StatusBadRequest {
		t.Errorf("expected 400 BadRequest for malformed path, got %d", wBadPath.Code)
	}

	// 3. Malformed JSON body
	reqBadJSON := httptest.NewRequest(http.MethodPut, "/api/users/alice/quota", strings.NewReader(`{invalid json`))
	wBadJSON := httptest.NewRecorder()
	srv.handleUserQuota(wBadJSON, reqBadJSON)
	if wBadJSON.Code != http.StatusBadRequest {
		t.Errorf("expected 400 BadRequest for malformed JSON, got %d", wBadJSON.Code)
	}

	// 4. Zero quota_bytes
	reqZero := httptest.NewRequest(http.MethodPut, "/api/users/alice/quota", strings.NewReader(`{"quota_bytes":0}`))
	wZero := httptest.NewRecorder()
	srv.handleUserQuota(wZero, reqZero)
	if wZero.Code != http.StatusBadRequest {
		t.Errorf("expected 400 BadRequest for zero quota, got %d", wZero.Code)
	}

	// 5. Non-existent user
	reqUnknown := httptest.NewRequest(http.MethodPut, "/api/users/ghost/quota", strings.NewReader(`{"quota_bytes":1048576}`))
	wUnknown := httptest.NewRecorder()
	srv.handleUserQuota(wUnknown, reqUnknown)
	if wUnknown.Code != http.StatusNotFound {
		t.Errorf("expected 404 NotFound for unknown user, got %d", wUnknown.Code)
	}

	// 6. Options pre-flight
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/users/alice/quota", nil)
	wOptions := httptest.NewRecorder()
	srv.handleUserQuota(wOptions, reqOptions)
	if wOptions.Code != http.StatusOK {
		t.Errorf("expected 200 OK for OPTIONS pre-flight, got %d", wOptions.Code)
	}
}

// TestAdminUsersListEmptyCluster tests GET /api/users when no users or nodes exist.
func TestAdminUsersListEmptyCluster(t *testing.T) {
	srv := NewAdminServer("", "")

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()
	srv.handleUsers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty users list, got %d", w.Code)
	}
	// Should return []
	trimmed := strings.TrimSpace(w.Body.String())
	if trimmed != "null" && trimmed != "[]" {
		t.Errorf("expected empty user list JSON, got: %s", trimmed)
	}
}

// TestAdminUserQuotaMissingHomeNode verifies error handling when user has no home fileserver.
func TestAdminUserQuotaMissingHomeNode(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.users["orphan"] = "99" // Node 99 does not exist in srv.nodes

	body := bytes.NewBufferString(`{"quota_bytes":1048576}`)
	req := httptest.NewRequest(http.MethodPut, "/api/users/orphan/quota", body)
	w := httptest.NewRecorder()
	srv.handleUserQuota(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 BadGateway for user whose node is missing, got %d", w.Code)
	}
}
