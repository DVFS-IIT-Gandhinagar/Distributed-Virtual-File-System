package fileserver

import (
	"context"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

// setupSharedRoot registers alice (owner) and bob (shared user) on alice's root.
func setupSharedRoot(t *testing.T) (*GRPCHandler, *FileServer, *pb.FID) {
	t.Helper()
	h, fs := setupHandlerTest(t)

	aliceResp, err := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{
		Username: "alice", RootUser: "alice", RootPath: "alice", CallbackAddress: "127.0.0.1:1",
	})
	if err != nil || !aliceResp.Success {
		t.Fatalf("alice RegisterClient failed: err=%v resp=%+v", err, aliceResp)
	}
	if resp, err := h.Share(context.Background(), &pb.ShareRequest{
		Username: "alice", Fid: aliceResp.UserRootFid, ShareWith: "bob",
	}); err != nil || !resp.Success {
		t.Fatalf("Share failed: err=%v resp=%+v", err, resp)
	}
	bobResp, err := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{
		Username: "bob", RootUser: "alice", RootPath: "alice", CallbackAddress: "127.0.0.1:2",
	})
	if err != nil || !bobResp.Success {
		t.Fatalf("bob RegisterClient failed: err=%v resp=%+v", err, bobResp)
	}
	if _, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: "docs", Fid: aliceResp.UserRootFid, RootUser: "alice", Type: pb.InodeType_DIRECTORY,
	}); err != nil {
		t.Fatalf("CreateFile docs failed: %v", err)
	}
	return h, fs, aliceResp.UserRootFid
}

func targetUsers(targets []clientSession) map[string]bool {
	out := map[string]bool{}
	for _, s := range targets {
		out[s.username] = true
	}
	return out
}

// The owner moving around a shared root must not move the shared user's
// tracked directory; otherwise the shared user stops getting invalidations.
func TestChangeDirOnlyMovesCallerSessionOnSharedRoot(t *testing.T) {
	h, fs, rootFID := setupSharedRoot(t)

	resp, err := h.ChangeDir(contextWithUser("alice"), &pb.ChangeDirRequest{Fid: rootFID, RootFid: rootFID, Path: "docs"})
	if err != nil || !resp.Success {
		t.Fatalf("alice ChangeDir failed: err=%v resp=%+v", err, resp)
	}

	root := domain.FIDFromProto(rootFID).String()
	fs.mu.RLock()
	bobDir := fs.sessions["bob"].currentDirFID
	aliceDir := fs.sessions["alice"].currentDirFID
	fs.mu.RUnlock()

	if bobDir != root {
		t.Fatalf("bob's current dir moved by alice's cd: got %s want %s", bobDir, root)
	}
	if aliceDir != domain.FIDFromProto(resp.NewFid).String() {
		t.Fatalf("alice's current dir not updated: got %s", aliceDir)
	}

	// A change alice makes in the root must still reach bob.
	fs.mu.RLock()
	targets := targetUsers(fs.snapshotNotifyTargetsForDirLocked(root, "alice"))
	fs.mu.RUnlock()
	if !targets["bob"] {
		t.Fatalf("expected bob to be notified about changes in the shared root, got %v", targets)
	}
}

// A heartbeat (ChangeDir ".") keeps an otherwise idle session eligible for callbacks.
func TestHeartbeatKeepsIdleSharedSessionActive(t *testing.T) {
	h, fs, rootFID := setupSharedRoot(t)
	root := domain.FIDFromProto(rootFID).String()

	fs.mu.Lock()
	fs.sessions["bob"].lastSeenAt = time.Now().Add(-2 * activeSessionTTL)
	fs.mu.Unlock()

	fs.mu.RLock()
	before := targetUsers(fs.snapshotNotifyTargetsForDirLocked(root, "alice"))
	fs.mu.RUnlock()
	if before["bob"] {
		t.Fatalf("precondition: stale bob session should not be a target")
	}

	resp, err := h.ChangeDir(contextWithUser("bob"), &pb.ChangeDirRequest{Fid: rootFID, RootFid: rootFID, Path: "."})
	if err != nil || !resp.Success {
		t.Fatalf("bob heartbeat ChangeDir failed: err=%v resp=%+v", err, resp)
	}

	fs.mu.RLock()
	after := targetUsers(fs.snapshotNotifyTargetsForDirLocked(root, "alice"))
	fs.mu.RUnlock()
	if !after["bob"] {
		t.Fatalf("expected heartbeat to make bob a notify target again, got %v", after)
	}
}
