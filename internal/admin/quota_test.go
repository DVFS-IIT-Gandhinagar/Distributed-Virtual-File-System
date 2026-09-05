package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"google.golang.org/grpc"
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
