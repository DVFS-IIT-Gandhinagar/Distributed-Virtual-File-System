package client

import (
	"net"
	"testing"
	"time"

	fspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// restartableFileServer serves one FileServer (state survives restarts) on a
// fixed address so tests can drop and restore the connection.
type restartableFileServer struct {
	t       *testing.T
	addr    string
	handler fspb.FileServerServer
	srv     *grpc.Server
}

func startRestartableFileServer(t *testing.T, wrap func(*fileserver.GRPCHandler) fspb.FileServerServer) *restartableFileServer {
	t.Helper()
	fs, err := fileserver.NewFileServer("fs-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}
	var h fspb.FileServerServer = fileserver.NewGRPCHandler(fs)
	if wrap != nil {
		h = wrap(h.(*fileserver.GRPCHandler))
	}
	r := &restartableFileServer{t: t, handler: h}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	r.addr = lis.Addr().String()
	r.serve(lis)
	t.Cleanup(func() { r.srv.Stop() })
	return r
}

func (r *restartableFileServer) serve(lis net.Listener) {
	r.srv = grpc.NewServer()
	fspb.RegisterFileServerServer(r.srv, r.handler)
	go func() { _ = r.srv.Serve(lis) }()
}

func (r *restartableFileServer) restart() {
	r.t.Helper()
	r.srv.Stop()
	var lis net.Listener
	var err error
	for i := 0; i < 50; i++ {
		if lis, err = net.Listen("tcp", r.addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		r.t.Fatalf("relisten on %s failed: %v", r.addr, err)
	}
	r.serve(lis)
}

// A client on a current server gets notifications without opening any
// listening port: nothing for a firewall or NAT to block.
func TestEventStreamNeedsNoInboundListener(t *testing.T) {
	defer withTempWorkingDir(t)()
	alice, bob, bobCache, expect := setupSharedPair(t)

	for _, c := range []*Client{alice, bob} {
		if c.callbackAddr != "" || c.stopCallback != nil || c.legacyCallbacks {
			t.Fatalf("%s opened a callback listener (%q); expected stream-only", c.username, c.callbackAddr)
		}
		if c.watchCancel == nil {
			t.Fatalf("%s has no event stream running", c.username)
		}
	}

	expect("create", "added", func() error { _, err := alice.CreateFile("pushed.txt"); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "pushed.txt") {
		t.Fatalf("bob does not see pushed.txt: %+v", files)
	}
	_ = bob
}

// If the connection drops (server restart, Wi-Fi blip), the client reconnects
// its stream on its own and keeps receiving notifications.
func TestEventStreamReconnectsAfterServerRestart(t *testing.T) {
	defer withTempWorkingDir(t)()
	r := startRestartableFileServer(t, nil)
	alice, _, bobCache, expect := setupSharedPairAt(t, r.addr)

	expect("create before restart", "added", func() error { _, err := alice.CreateFile("before.txt"); return err })

	r.restart()
	// Wait past the first reconnect backoff so bob's stream is back up.
	time.Sleep(eventStreamMinBackoff + 500*time.Millisecond)

	expect("create after restart", "added", func() error { _, err := alice.CreateFile("after.txt"); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "after.txt") {
		t.Fatalf("bob does not see after.txt after reconnect: %+v", files)
	}
}

// legacyHandler is a fileserver that predates WatchEvents.
type legacyHandler struct{ *fileserver.GRPCHandler }

func (legacyHandler) WatchEvents(*fspb.WatchEventsRequest, fspb.FileServer_WatchEventsServer) error {
	return status.Error(codes.Unimplemented, "method WatchEvents not implemented")
}

// Against an old server the client falls back to the callback listener, so a
// new client keeps working with not-yet-upgraded fileservers.
func TestEventStreamFallsBackToCallbacksOnOldServer(t *testing.T) {
	defer withTempWorkingDir(t)()
	r := startRestartableFileServer(t, func(h *fileserver.GRPCHandler) fspb.FileServerServer {
		return legacyHandler{h}
	})
	alice, bob, bobCache, expect := setupSharedPairAt(t, r.addr)

	if !bob.legacyCallbacks || bob.callbackAddr == "" || bob.watchCancel != nil {
		t.Fatalf("expected legacy callback fallback: legacy=%v addr=%q stream=%v", bob.legacyCallbacks, bob.callbackAddr, bob.watchCancel != nil)
	}

	expect("create on old server", "added", func() error { _, err := alice.CreateFile("legacy.txt"); return err })
	if files, _ := bobCache.ListFiles(); !hasFile(files, "legacy.txt") {
		t.Fatalf("bob does not see legacy.txt via callbacks: %+v", files)
	}

	bob.Disconnect()
	if bob.callbackAddr != "" || bob.stopCallback != nil {
		t.Fatalf("Disconnect left the callback listener running")
	}
}
