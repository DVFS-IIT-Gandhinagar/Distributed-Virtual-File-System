package fileserver

import (
	"context"
	"net"
	"testing"
	"time"

	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/metaserver"
	"google.golang.org/grpc"
)

func startTestMetaServerForFS(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	ms, err := metaserver.NewMetaServer(t.TempDir() + "/mds_state.json")
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	mspb.RegisterMetaServerServer(grpcServer, metaserver.NewGRPCHandler(ms))
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	cleanup = func() {
		grpcServer.Stop()
		_ = listener.Close()
	}

	return listener.Addr().String(), cleanup
}

func testMetaClientConn(t *testing.T, addr string) mspb.MetaServerClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithInsecure())
	if err != nil {
		t.Fatalf("failed to dial metaserver: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return mspb.NewMetaServerClient(conn)
}

func TestMSClientNoopsWhenMetaAddrEmpty(t *testing.T) {
	fs := newTestFileServer(t)

	if err := fs.RegisterWithMetaServer("127.0.0.1:50052"); err != nil {
		t.Fatalf("RegisterWithMetaServer no-op failed: %v", err)
	}
	if err := fs.RootShare("alice", "root", "/alice", "bob"); err != nil {
		t.Fatalf("RootShare no-op failed: %v", err)
	}
	if err := fs.RootUnshare("alice", "root", "/alice", "bob"); err != nil {
		t.Fatalf("RootUnshare no-op failed: %v", err)
	}
	if err := fs.HeartbeatWithMetaServer("127.0.0.1:50052"); err != nil {
		t.Fatalf("HeartbeatWithMetaServer no-op failed: %v", err)
	}

	stop := fs.StartMetaServerSync("", "127.0.0.1:50052", 10*time.Millisecond, 10*time.Millisecond)
	stop()
}

func TestRegisterWithMetaServerAndHeartbeat(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), false, msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot alice failed: %v", err)
	}

	selfAddr := "127.0.0.1:50077"
	if err := fs.RegisterWithMetaServer(selfAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	if err := fs.HeartbeatWithMetaServer(selfAddr); err != nil {
		t.Fatalf("HeartbeatWithMetaServer failed: %v", err)
	}

	mc := testMetaClientConn(t, msAddr)
	rootsResp, err := mc.GetRoots(context.Background(), &mspb.GetRootsRequest{Username: "alice"})
	if err != nil {
		t.Fatalf("GetRoots RPC failed: %v", err)
	}
	if !rootsResp.Success {
		t.Fatalf("GetRoots expected success, got error=%q", rootsResp.Error)
	}

	navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	}
	if !navResp.Success || navResp.Address != selfAddr {
		t.Fatalf("Navigate response mismatch: %+v", navResp)
	}
}

func TestRootShareAndUnshareNotifyMetaServer(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), false, msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot alice failed: %v", err)
	}
	if _, err := fs.GetUserRoot("bob", "bob"); err != nil {
		t.Fatalf("GetUserRoot bob failed: %v", err)
	}

	selfAddr := "127.0.0.1:50078"
	if err := fs.RegisterWithMetaServer(selfAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	if err := fs.RootShare("alice", "alice", "/alice", "bob"); err != nil {
		t.Fatalf("RootShare failed: %v", err)
	}

	mc := testMetaClientConn(t, msAddr)
	if navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "bob", RootUser: "alice"}); err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	} else if !navResp.Success {
		t.Fatalf("expected bob navigation to shared root to succeed: %+v", navResp)
	}

	if err := fs.RootUnshare("alice", "alice", "/alice", "bob"); err != nil {
		t.Fatalf("RootUnshare failed: %v", err)
	}

	if navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "bob", RootUser: "alice"}); err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	} else if navResp.Success {
		t.Fatalf("expected bob navigation to fail after unshare")
	}
}

func TestStartMetaServerSyncRegistersEventually(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), false, msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}
	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	selfAddr := "127.0.0.1:50079"
	stop := fs.StartMetaServerSync(msAddr, selfAddr, 20*time.Millisecond, 40*time.Millisecond)
	defer stop()

	mc := testMetaClientConn(t, msAddr)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "alice", RootUser: "alice"})
		if err == nil && navResp.Success && navResp.Address == selfAddr {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("metaserver sync did not register fileserver before deadline")
}
