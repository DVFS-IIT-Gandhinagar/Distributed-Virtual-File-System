package integration_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	fspb "github.com/umangshikarvar/dvfs/api/fileserver"
	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/client"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"github.com/umangshikarvar/dvfs/internal/metaserver"
	"google.golang.org/grpc"
)

func startMetaServer(t *testing.T) (addr string, ms *metaserver.MetaServer, cleanup func()) {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "mds", "state.json")
	ms, err := metaserver.NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for mds: %v", err)
	}

	server := grpc.NewServer()
	mspb.RegisterMetaServerServer(server, metaserver.NewGRPCHandler(ms))
	go func() { _ = server.Serve(lis) }()

	cleanup = func() {
		server.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), ms, cleanup
}

func startFileServer(t *testing.T, msAddr string) (addr string, fs *fileserver.FileServer, cleanup func()) {
	t.Helper()

	rootDir := t.TempDir()
	fs, err := fileserver.NewFileServer("fs-e2e", rootDir, false, msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for fs: %v", err)
	}

	server := grpc.NewServer()
	fspb.RegisterFileServerServer(server, fileserver.NewGRPCHandler(fs))
	go func() { _ = server.Serve(lis) }()

	cleanup = func() {
		server.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), fs, cleanup
}

func TestE2E_MDSRoutingAndClientCRUD(t *testing.T) {
	mdsAddr, _, cleanupMDS := startMetaServer(t)
	defer cleanupMDS()

	fsAddr, fs, cleanupFS := startFileServer(t, mdsAddr)
	defer cleanupFS()

	if err := fs.RegisterWithMetaServer(fsAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	alice := client.NewClient("alice", "alice", false)
	if _, err := alice.GetRoots(mdsAddr); err != nil {
		t.Fatalf("GetRoots failed: %v", err)
	}

	navAddr, err := alice.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("NavigateToFileServer failed: %v", err)
	}
	if navAddr != fsAddr {
		t.Fatalf("navigation address mismatch: got=%s want=%s", navAddr, fsAddr)
	}

	if _, err := alice.Connect(navAddr); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if _, err := alice.CreateDirectory("docs"); err != nil {
		t.Fatalf("CreateDirectory failed: %v", err)
	}
	if _, err := alice.ChangeDirectory("docs"); err != nil {
		t.Fatalf("ChangeDirectory failed: %v", err)
	}
	if _, err := alice.CreateFile("hello.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if err := alice.WriteFile("hello.txt", []byte("hello from e2e")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, err := alice.ReadFile("hello.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(data), "hello from e2e"; got != want {
		t.Fatalf("read content mismatch: got=%q want=%q", got, want)
	}
}

func TestE2E_ShareUnshareFlowAcrossUsers(t *testing.T) {
	mdsAddr, _, cleanupMDS := startMetaServer(t)
	defer cleanupMDS()

	fsAddr, fs, cleanupFS := startFileServer(t, mdsAddr)
	defer cleanupFS()

	if err := fs.RegisterWithMetaServer(fsAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	alice := client.NewClient("alice", "alice", false)
	bob := client.NewClient("bob", "bob", false)

	if _, err := alice.GetRoots(mdsAddr); err != nil {
		t.Fatalf("alice GetRoots failed: %v", err)
	}
	if _, err := bob.GetRoots(mdsAddr); err != nil {
		t.Fatalf("bob GetRoots failed: %v", err)
	}

	aliceFSAddr, err := alice.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("alice NavigateToFileServer failed: %v", err)
	}
	if _, err := alice.Connect(aliceFSAddr); err != nil {
		t.Fatalf("alice Connect failed: %v", err)
	}

	if _, err := alice.CreateFile("shared.txt"); err != nil {
		t.Fatalf("alice CreateFile failed: %v", err)
	}
	if err := alice.WriteFile("shared.txt", []byte("shared payload")); err != nil {
		t.Fatalf("alice WriteFile failed: %v", err)
	}

	if err := alice.Share("bob"); err != nil {
		t.Fatalf("alice Share failed: %v", err)
	}

	bob.SetRootUser("alice")
	bobFSAddr, err := bob.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("bob NavigateToFileServer to alice root failed: %v", err)
	}
	if _, err := bob.Connect(bobFSAddr); err != nil {
		t.Fatalf("bob Connect failed: %v", err)
	}

	data, err := bob.ReadFile("shared.txt")
	if err != nil {
		t.Fatalf("bob ReadFile on shared file failed: %v", err)
	}
	if got, want := string(data), "shared payload"; got != want {
		t.Fatalf("bob read content mismatch: got=%q want=%q", got, want)
	}

	if err := alice.Unshare("bob"); err != nil {
		t.Fatalf("alice Unshare failed: %v", err)
	}

	if _, err := bob.NavigateToFileServer(mdsAddr); err == nil {
		t.Fatalf("expected bob navigation to alice root to fail after unshare")
	}
}

func TestIntegration_HeartbeatStaleTransition(t *testing.T) {
	mdsAddr, ms, cleanupMDS := startMetaServer(t)
	defer cleanupMDS()

	fsAddr, fs, cleanupFS := startFileServer(t, mdsAddr)
	defer cleanupFS()

	ms.SetHeartbeatConfig(1*time.Second, 50*time.Millisecond)
	stop := ms.StartHeartbeatMonitor()
	defer stop()

	if err := fs.RegisterWithMetaServer(fsAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	alice := client.NewClient("alice", "alice", false)
	if _, err := alice.GetRoots(mdsAddr); err != nil {
		t.Fatalf("GetRoots before stale transition failed: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	if _, err := alice.NavigateToFileServer(mdsAddr); err == nil {
		t.Fatalf("expected NavigateToFileServer to fail after fileserver becomes stale")
	}
}

func TestIntegration_MetaServerGRPCHealthy(t *testing.T) {
	mdsAddr, _, cleanupMDS := startMetaServer(t)
	defer cleanupMDS()

	conn, err := grpc.NewClient(mdsAddr, grpc.WithInsecure())
	if err != nil {
		t.Fatalf("failed to dial metaserver directly: %v", err)
	}
	defer conn.Close()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.Heartbeat(context.Background(), &mspb.HeartbeatRequest{Address: "unknown"})
	if err != nil {
		t.Fatalf("Heartbeat RPC failed: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected unknown heartbeat to fail")
	}
}
