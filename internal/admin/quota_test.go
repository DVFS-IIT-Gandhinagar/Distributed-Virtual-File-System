package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type mockFileServer struct {
	pb.UnimplementedFileServerServer
	receivedUser  string
	receivedQuota uint64
}

func (m *mockFileServer) SetQuota(ctx context.Context, req *pb.SetQuotaRequest) (*pb.SetQuotaResponse, error) {
	m.receivedUser = req.Username
	m.receivedQuota = req.QuotaBytes
	return &pb.SetQuotaResponse{Success: true}, nil
}

func TestHandleUsers(t *testing.T) {
	admin := NewAdminServer("", "")
	admin.users["alice"] = "0"
	admin.users["bob"] = "1"

	admin.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: "127.0.0.1:50052",
		Status:  StatusOnline,
		Metrics: &FileserverMetrics{
			PerUserStorage: map[string]uint64{
				"alice": 200 * 1024 * 1024,
			},
			PerUserQuota: map[string]uint64{
				"alice": 1024 * 1024 * 1024,
			},
			ActiveUsers: []string{"alice"},
		},
	}
	admin.nodes["1"] = &NodeState{
		FsID:    "1",
		Address: "127.0.0.1:50053",
		Status:  StatusOnline,
		Metrics: &FileserverMetrics{
			PerUserStorage: map[string]uint64{
				"bob":   980 * 1024 * 1024,
				"alice": 50 * 1024 * 1024, // Alice on node 1 as well
			},
			PerUserQuota: map[string]uint64{
				"bob": 1024 * 1024 * 1024,
			},
			ActiveUsers: []string{},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	admin.handleUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var userList []UserSummary
	if err := json.NewDecoder(rec.Body).Decode(&userList); err != nil {
		t.Fatalf("failed to decode user summary list: %v", err)
	}

	if len(userList) != 2 {
		t.Fatalf("expected 2 users, got %d", len(userList))
	}

	userMap := make(map[string]UserSummary)
	for _, u := range userList {
		userMap[u.Username] = u
	}

	alice, exists := userMap["alice"]
	if !exists {
		t.Fatalf("alice not found in response")
	}
	if alice.HomeFsID != "0" {
		t.Errorf("expected alice home fs 0, got %s", alice.HomeFsID)
	}
	expectedAliceUsed := uint64((200 + 50) * 1024 * 1024)
	if alice.QuotaUsed != expectedAliceUsed {
		t.Errorf("expected alice used %d, got %d", expectedAliceUsed, alice.QuotaUsed)
	}
	if len(alice.Nodes) != 2 {
		t.Errorf("expected alice to have entries on 2 nodes, got %d", len(alice.Nodes))
	}
	if alice.ActiveSessions != 1 || !alice.IsOnline {
		t.Errorf("expected alice active_sessions=1 and is_online=true, got sessions=%d online=%v", alice.ActiveSessions, alice.IsOnline)
	}

	bob, exists := userMap["bob"]
	if !exists {
		t.Fatalf("bob not found in response")
	}
	expectedBobUsed := uint64(980 * 1024 * 1024)
	if bob.QuotaUsed != expectedBobUsed {
		t.Errorf("expected bob used %d, got %d", expectedBobUsed, bob.QuotaUsed)
	}
	if bob.UsagePercent < 95.0 {
		t.Errorf("expected bob usage percent > 95%%, got %f", bob.UsagePercent)
	}
	if bob.ActiveSessions != 0 || bob.IsOnline {
		t.Errorf("expected bob active_sessions=0 and is_online=false, got sessions=%d online=%v", bob.ActiveSessions, bob.IsOnline)
	}
}

func TestHandleUserQuotaValidation(t *testing.T) {
	admin := NewAdminServer("", "")
	admin.users["alice"] = "0"

	// 1. Invalid path
	req := httptest.NewRequest(http.MethodPut, "/api/users/badpath", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	admin.handleUserQuota(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on bad path, got %d", rec.Code)
	}

	// 2. User not found
	req = httptest.NewRequest(http.MethodPut, "/api/users/ghost/quota", bytes.NewBufferString(`{"quota_bytes": 1000}`))
	rec = httptest.NewRecorder()
	admin.handleUserQuota(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing user, got %d", rec.Code)
	}

	// 3. Zero quota
	req = httptest.NewRequest(http.MethodPut, "/api/users/alice/quota", bytes.NewBufferString(`{"quota_bytes": 0}`))
	rec = httptest.NewRecorder()
	admin.handleUserQuota(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for zero quota, got %d", rec.Code)
	}
}

func TestHandleUserQuotaSuccess(t *testing.T) {
	// Start mock gRPC fileserver
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	mockSrv := &mockFileServer{}
	grpcServer := grpc.NewServer()
	pb.RegisterFileServerServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	serverAddr := lis.Addr().String()

	admin := NewAdminServer("", "")
	admin.users["alice"] = "0"
	admin.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: serverAddr,
		Metrics: &FileserverMetrics{
			PerUserQuota: map[string]uint64{
				"alice": 1024 * 1024 * 1024,
			},
		},
	}

	newQuota := uint64(2 * 1024 * 1024 * 1024)
	payload := SetQuotaPayload{QuotaBytes: newQuota}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPut, "/api/users/alice/quota", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	admin.handleUserQuota(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if mockSrv.receivedUser != "alice" {
		t.Errorf("expected mock to receive user 'alice', got %s", mockSrv.receivedUser)
	}
	if mockSrv.receivedQuota != newQuota {
		t.Errorf("expected mock to receive quota %d, got %d", newQuota, mockSrv.receivedQuota)
	}

	// Verify cached metrics updated immediately
	if q := admin.nodes["0"].Metrics.PerUserQuota["alice"]; q != newQuota {
		t.Errorf("expected cached quota %d, got %d", newQuota, q)
	}
}

func TestHandleUserQuotaSuccessTLS(t *testing.T) {
	certFile := "../../deploy_certs/localhost/server.crt"
	keyFile := "../../deploy_certs/localhost/server.key"
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		certFile = "deploy_certs/localhost/server.crt"
		keyFile = "deploy_certs/localhost/server.key"
	}
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Skipf("skipping TLS test, deploy_certs not found: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	mockSrv := &mockFileServer{}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewServerTLSFromCert(&tlsCert)))
	pb.RegisterFileServerServer(grpcServer, mockSrv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	serverAddr := lis.Addr().String()

	admin := NewAdminServer("", "")
	admin.users["alice"] = "0"
	admin.nodes["0"] = &NodeState{
		FsID:    "0",
		Address: serverAddr,
		Metrics: &FileserverMetrics{
			PerUserQuota: map[string]uint64{
				"alice": 1024 * 1024 * 1024,
			},
		},
	}

	newQuota := uint64(5 * 1024 * 1024 * 1024)
	payload := SetQuotaPayload{QuotaBytes: newQuota}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPut, "/api/users/alice/quota", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	admin.handleUserQuota(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	if mockSrv.receivedUser != "alice" {
		t.Errorf("expected mock to receive user 'alice', got %s", mockSrv.receivedUser)
	}
	if mockSrv.receivedQuota != newQuota {
		t.Errorf("expected mock to receive quota %d, got %d", newQuota, mockSrv.receivedQuota)
	}

	if q := admin.nodes["0"].Metrics.PerUserQuota["alice"]; q != newQuota {
		t.Errorf("expected cached quota %d, got %d", newQuota, q)
	}
}

func TestHandleUsers_DiscoveredMachineMappingAndShiftFix(t *testing.T) {
	admin := NewAdminServer("", "")
	// Bob is assigned to fsID "1", which in the cluster corresponds to dvfs3 (FS-3) because dvfs2 was skipped
	admin.users["bob"] = "1"
	admin.users["alice"] = "0"

	admin.nodes["0"] = &NodeState{
		FsID:        "0",
		DisplayID:   1,
		DisplayName: "FS-1",
		MachineName: "dvfs1",
		Address:     "10.0.171.38:50052",
		Status:      StatusOnline,
		Metrics: &FileserverMetrics{
			PerUserStorage: map[string]uint64{
				"alice": 100 * 1024 * 1024,
			},
			PerUserQuota: map[string]uint64{
				"alice": 1024 * 1024 * 1024,
			},
		},
	}
	admin.nodes["1"] = &NodeState{
		FsID:        "1",
		DisplayID:   3,
		DisplayName: "FS-3",
		MachineName: "dvfs3",
		Address:     "10.0.171.40:50052",
		Status:      StatusOnline,
		Metrics: &FileserverMetrics{
			PerUserStorage: map[string]uint64{
				"bob":   500 * 1024 * 1024,
				"alice": 50 * 1024 * 1024,
			},
			PerUserQuota: map[string]uint64{
				"bob": 2 * 1024 * 1024 * 1024,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()

	admin.handleUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var userList []UserSummary
	if err := json.NewDecoder(rec.Body).Decode(&userList); err != nil {
		t.Fatalf("failed to decode user list: %v", err)
	}

	userMap := make(map[string]UserSummary)
	for _, u := range userList {
		userMap[u.Username] = u
	}

	bob, exists := userMap["bob"]
	if !exists {
		t.Fatalf("bob not found in user list")
	}

	// Bob's home fs is "1", but must be mapped to FS-3 and dvfs3, NOT FS-2 or dvfs2
	if bob.HomeFsDisplay != "FS-3" {
		t.Errorf("expected bob HomeFsDisplay to be 'FS-3', got %q", bob.HomeFsDisplay)
	}
	if bob.HomeFsMachine != "dvfs3" {
		t.Errorf("expected bob HomeFsMachine to be 'dvfs3', got %q", bob.HomeFsMachine)
	}
	if bob.HomeFsAddress != "10.0.171.40:50052" {
		t.Errorf("expected bob HomeFsAddress to be '10.0.171.40:50052', got %q", bob.HomeFsAddress)
	}

	// Also check Alice's storage node breakdown: node "1" must have DisplayName "FS-3" and MachineName "dvfs3"
	alice, exists := userMap["alice"]
	if !exists {
		t.Fatalf("alice not found in user list")
	}
	var node1Storage *NodeUserStorage
	for i := range alice.Nodes {
		if alice.Nodes[i].FsID == "1" {
			node1Storage = &alice.Nodes[i]
			break
		}
	}
	if node1Storage == nil {
		t.Fatalf("alice node 1 breakdown not found")
	}
	if node1Storage.DisplayID != 3 {
		t.Errorf("expected node 1 DisplayID 3, got %d", node1Storage.DisplayID)
	}
	if node1Storage.DisplayName != "FS-3" {
		t.Errorf("expected node 1 DisplayName 'FS-3', got %q", node1Storage.DisplayName)
	}
	if node1Storage.MachineName != "dvfs3" {
		t.Errorf("expected node 1 MachineName 'dvfs3', got %q", node1Storage.MachineName)
	}
}

