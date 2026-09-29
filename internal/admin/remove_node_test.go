package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/metaserver"
	"google.golang.org/grpc"
)

func TestHandleRemoveNode_MethodsAndValidation(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.authManager = nil // bypass auth for basic method tests

	// 1. GET not allowed
	req := httptest.NewRequest(http.MethodGet, "/api/nodes/1", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoveNode(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rec.Code)
	}

	// 2. OPTIONS preflight allowed
	reqOpt := httptest.NewRequest(http.MethodOptions, "/api/nodes/1", nil)
	recOpt := httptest.NewRecorder()
	srv.handleRemoveNode(recOpt, reqOpt)
	if recOpt.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", recOpt.Code)
	}

	// 3. Missing fsID
	reqEmpty := httptest.NewRequest(http.MethodDelete, "/api/nodes/", nil)
	recEmpty := httptest.NewRecorder()
	srv.handleRemoveNode(recEmpty, reqEmpty)
	if recEmpty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty fsID, got %d", recEmpty.Code)
	}

	// 4. Node not found
	reqNotFound := httptest.NewRequest(http.MethodDelete, "/api/nodes/nonexistent", nil)
	recNotFound := httptest.NewRecorder()
	srv.handleRemoveNode(recNotFound, reqNotFound)
	if recNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent node, got %d", recNotFound.Code)
	}
}

func TestHandleRemoveNode_AuthEnforcement(t *testing.T) {
	srv := NewAdminServer("", "")
	srv.authManager.SetHash("8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918") // "admin"

	srv.nodes["3"] = &NodeState{
		FsID:        "3",
		DisplayName: "FS-4",
		Address:     "10.0.171.41:50052",
		Status:      StatusOffline,
	}

	handler := srv.requireAuth(srv.handleRemoveNode)

	// 1. Without auth -> 401
	unauthReq := httptest.NewRequest(http.MethodDelete, "/api/nodes/3", nil)
	unauthRec := httptest.NewRecorder()
	handler(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", unauthRec.Code)
	}

	// Node should still exist
	if _, exists := srv.nodes["3"]; !exists {
		t.Fatalf("node should not have been removed without auth")
	}

	// 2. With auth -> 200
	token := srv.authManager.CreateSession()
	authReq := httptest.NewRequest(http.MethodDelete, "/api/nodes/3", nil)
	authReq.Header.Set("Authorization", "Bearer "+token)
	authRec := httptest.NewRecorder()
	handler(authRec, authReq)

	if authRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d (body: %s)", authRec.Code, authRec.Body.String())
	}

	// Node should now be removed from srv.nodes
	if _, exists := srv.nodes["3"]; exists {
		t.Fatalf("node 3 should have been removed from admin server")
	}
}

func TestHandleRemoveNode_WithMetaServerDeregister(t *testing.T) {
	// Start a real in-process metaserver gRPC server
	tempDir := t.TempDir()
	ms, err := metaserver.NewMetaServer(tempDir + "/ms_state.json")
	if err != nil {
		t.Fatalf("failed to create metaserver: %v", err)
	}
	grpcHandler := metaserver.NewGRPCHandler(ms)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer()
	mspb.RegisterMetaServerServer(grpcServer, grpcHandler)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	msAddr := lis.Addr().String()

	// Register a fileserver on the metaserver
	fsAddr := "10.0.171.41:50052"
	_, err = grpcHandler.RegisterFileServer(context.Background(), &mspb.RegisterFileServerRequest{
		Address: fsAddr,
		Users:   []string{"olduser"},
	})
	if err != nil {
		t.Fatalf("failed to register fileserver: %v", err)
	}

	// Configure admin server
	srv := NewAdminServer("", "")
	srv.authManager = nil // bypass auth for this integration test
	srv.SetMetaServerAddr(msAddr)

	srv.nodes["3"] = &NodeState{
		FsID:        "3",
		DisplayName: "FS-4",
		MachineName: "dvfs4",
		Address:     fsAddr,
		Status:      StatusOffline,
	}
	srv.users["olduser"] = "3"
	srv.users["otheruser"] = "2"

	// Delete node via admin handler
	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/3", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoveNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["success"] != true {
		t.Fatalf("expected success: true, got %v", resp)
	}
	if resp["removed_fs_id"] != "3" {
		t.Errorf("expected removed_fs_id: 3, got %v", resp["removed_fs_id"])
	}

	// Verify Admin state
	if _, exists := srv.nodes["3"]; exists {
		t.Errorf("node 3 still exists in admin nodes")
	}
	if _, exists := srv.users["olduser"]; exists {
		t.Errorf("olduser still exists in admin users")
	}
	if _, exists := srv.users["otheruser"]; !exists {
		t.Errorf("otheruser should still exist in admin users")
	}

	// Verify MetaServer state file: fileserver should be gone
	state, err := LoadMetaState(tempDir + "/ms_state.json")
	if err != nil {
		t.Fatalf("failed to reload metaserver state: %v", err)
	}
	for id, info := range state.FileServers {
		if info.Address == fsAddr {
			t.Errorf("fileserver %s (id=%s) still exists in metaserver state file", fsAddr, id)
		}
	}
	if _, exists := state.Users["olduser"]; exists {
		t.Errorf("olduser still exists in metaserver state file")
	}
}
