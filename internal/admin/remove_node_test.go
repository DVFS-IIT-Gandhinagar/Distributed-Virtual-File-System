package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"google.golang.org/grpc"
)

// newStoreBackedAdmin returns an AdminServer that reads the given store but
// writes no files. Constructing with a nil store disables the snapshot,
// history, alert and tombstone paths; the store is attached afterwards.
func newStoreBackedAdmin(store storage.MetaStore) *AdminServer {
	srv := NewAdminServer(nil, "")
	srv.store = store
	return srv
}

func TestHandleRemoveNode_MethodsAndValidation(t *testing.T) {
	srv := NewAdminServer(nil, "")
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
	srv := NewAdminServer(nil, "")
	srv.authManager.SetHash("8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918") // "admin"

	srv.nodes["3"] = &NodeState{
		FsID:        "3",
		NodeID:      "fs4",
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

// The full path: admin -> gRPC -> metaserver -> store, with the admin reading
// the same store the metaserver writes, as in production.
func TestHandleRemoveNode_WithMetaServerDeregister(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	ms, err := metaserver.NewMetaServer(ctx, store)
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

	fsAddr := "10.0.171.41:50052"
	if _, err := grpcHandler.RegisterFileServer(ctx, &mspb.RegisterFileServerRequest{
		FsId: "fs-old", Address: fsAddr, Users: []string{"olduser"},
	}); err != nil {
		t.Fatalf("failed to register fs-old: %v", err)
	}
	if _, err := grpcHandler.RegisterFileServer(ctx, &mspb.RegisterFileServerRequest{
		FsId: "fs-keep", Address: "10.0.172.42:50052", Users: []string{"otheruser"},
	}); err != nil {
		t.Fatalf("failed to register fs-keep: %v", err)
	}

	srv := newStoreBackedAdmin(store)
	srv.authManager = nil // bypass auth for this integration test
	srv.SetMetaServerAddr(msAddr)

	// Discover from the store, exactly as the poller does.
	srv.refreshNodes()
	var oldFsID string
	for id, n := range srv.nodes {
		if n.NodeID == "fs-old" {
			oldFsID = id
		}
	}
	if oldFsID == "" {
		t.Fatalf("fs-old was not discovered from the store: %+v", srv.nodes)
	}
	if srv.users["olduser"] != oldFsID {
		t.Fatalf("olduser should map to %s, got %q", oldFsID, srv.users["olduser"])
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/"+oldFsID, nil)
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
	if resp["removed_node_id"] != "fs-old" {
		t.Errorf("expected removed_node_id fs-old, got %v", resp["removed_node_id"])
	}
	if w, warned := resp["metaserver_warning"]; warned {
		t.Fatalf("deregister should have reached the metaserver cleanly, got warning: %v", w)
	}

	// The console's own view.
	if _, exists := srv.nodes[oldFsID]; exists {
		t.Errorf("removed node still in admin nodes")
	}
	if _, exists := srv.users["olduser"]; exists {
		t.Errorf("olduser still in admin users")
	}
	if _, exists := srv.users["otheruser"]; !exists {
		t.Errorf("otheruser should be untouched")
	}

	// The store, which is what every other component reads.
	snap, err := store.LoadSnapshot(ctx)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if len(snap.FileServers) != 1 || snap.FileServers[0].NodeID != "fs-keep" {
		t.Errorf("expected only fs-keep in the store, got %+v", snap.FileServers)
	}
	for _, u := range snap.Users {
		if u.Username == "olduser" {
			t.Errorf("olduser still in the store, pointing at %s", u.HomeNodeID)
		}
	}

	// And the next poll must not bring it back.
	srv.refreshNodes()
	if _, exists := srv.nodes[oldFsID]; exists {
		t.Errorf("removed node was resurrected by the next refresh")
	}
	if _, exists := srv.users["otheruser"]; !exists {
		t.Errorf("otheruser disappeared after refresh")
	}
}

// With no metaserver address the console cannot change the cluster; the node
// stays in the store. The tombstone must still keep it hidden from the console
// across refreshes, until the node proves it was redeployed by heartbeating
// again. The response must also admit that the cluster was not changed.
func TestHandleRemoveNode_NoResurrectionOnRefresh(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	now := time.Now().Unix()

	if _, err := store.UpsertFileServer(ctx, storage.FileServerRecord{
		NodeID: "fs2", Address: "10.0.172.42:50052", Status: domain.FileServerStatusHealthy, LastHeartbeatUnix: now,
	}); err != nil {
		t.Fatalf("upsert fs2: %v", err)
	}
	if _, err := store.UpsertFileServer(ctx, storage.FileServerRecord{
		NodeID: "fs3", Address: "10.0.171.41:50052", Status: domain.FileServerStatusStale, LastHeartbeatUnix: now - 3600,
	}); err != nil {
		t.Fatalf("upsert fs3: %v", err)
	}

	srv := newStoreBackedAdmin(store)
	srv.authManager = nil
	// Deliberately no metaserver address.

	// 1. Initial refresh discovers both.
	srv.refreshNodes()
	if len(srv.nodes) != 2 {
		t.Fatalf("expected 2 nodes discovered, got %d", len(srv.nodes))
	}
	var staleID string
	for id, n := range srv.nodes {
		if n.NodeID == "fs3" {
			staleID = id
		}
	}
	if staleID == "" {
		t.Fatalf("fs3 not discovered")
	}

	// 2. Admin removes fs3.
	req := httptest.NewRequest(http.MethodDelete, "/api/nodes/"+staleID, nil)
	rec := httptest.NewRecorder()
	srv.handleRemoveNode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["metaserver_warning"] == nil {
		t.Fatalf("without a metaserver address the response must say the cluster was not changed, got %v", resp)
	}
	if _, ok := srv.nodes[staleID]; ok {
		t.Fatalf("expected fs3 to be removed from the console")
	}

	// 3. The store still has fs3; the next refresh MUST NOT resurrect it.
	srv.refreshNodes()
	if _, ok := srv.nodes[staleID]; ok {
		t.Fatalf("fs3 was resurrected by refreshNodes despite admin removal")
	}

	// 4. A heartbeat newer than the removal means fs3 was redeployed; it comes back.
	if err := store.RecordHeartbeat(ctx, "fs3", time.Now().Add(time.Hour), domain.FileServerStatusHealthy); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	srv.refreshNodes()
	if _, ok := srv.nodes[staleID]; !ok {
		t.Fatalf("expected fs3 to be accepted after a heartbeat newer than its removal")
	}
	if _, tombstoned := srv.removedNodes[staleID]; tombstoned {
		t.Errorf("tombstone should have been cleared by the fresh heartbeat")
	}
}
