package metaserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

func newTestMetaServer(t *testing.T) *MetaServer {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "state", "metaserver_state.json")
	ms, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}
	return ms
}

func TestMetaServerSaveAndLoadStateRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nested", "ms_state.json")

	ms, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	now := time.Now().Unix()
	ms.fileservers[1] = &domain.FileServerInfo{
		Address:           "10.0.0.1:5001",
		UserCount:         2,
		LastHeartbeatUnix: now,
		Status:            domain.FileServerStatusHealthy,
	}
	ms.users["alice"] = 1
	ms.users["bob"] = 1
	ms.shared["bob"] = []SharedDirEntry{{Owner: "alice", Path: "/alice/docs", DisplayName: "docs"}}
	ms.nextFsID = 2

	if err := ms.SaveState(); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	reloaded, err := NewMetaServer(statePath)
	if err != nil {
		t.Fatalf("NewMetaServer (reload) failed: %v", err)
	}

	if reloaded.nextFsID != 2 {
		t.Fatalf("nextFsID mismatch: got=%d want=2", reloaded.nextFsID)
	}

	if got := reloaded.fileservers[1]; got == nil || got.Address != "10.0.0.1:5001" || got.UserCount != 2 {
		t.Fatalf("reloaded fileserver mismatch: got=%+v", got)
	}

	if !reflect.DeepEqual(reloaded.users, ms.users) {
		t.Fatalf("users mismatch: got=%v want=%v", reloaded.users, ms.users)
	}

	if !reflect.DeepEqual(reloaded.shared, ms.shared) {
		t.Fatalf("shared mismatch: got=%v want=%v", reloaded.shared, ms.shared)
	}
}

func TestMarkStaleFileServersLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	ms.heartbeatTimeout = 10 * time.Second

	now := time.Now().Unix()
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[2] = &domain.FileServerInfo{Address: "fs2", UserCount: 1, LastHeartbeatUnix: now - 100, Status: domain.FileServerStatusHealthy}

	changed := ms.markStaleFileServersLocked(now)
	if !changed {
		t.Fatalf("expected stale transition to report changed=true")
	}

	if got := ms.fileservers[1].Status; got != domain.FileServerStatusHealthy {
		t.Fatalf("fs1 status mismatch: got=%s want=%s", got, domain.FileServerStatusHealthy)
	}

	if got := ms.fileservers[2].Status; got != domain.FileServerStatusStale {
		t.Fatalf("fs2 status mismatch: got=%s want=%s", got, domain.FileServerStatusStale)
	}

	if changedAgain := ms.markStaleFileServersLocked(now); changedAgain {
		t.Fatalf("expected second stale pass to be no-op")
	}
}

func TestGetLeastLoadedHealthyFileServerLocked(t *testing.T) {
	ms := newTestMetaServer(t)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{Address: "fs0", UserCount: 10, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusStale}
	ms.fileservers[2] = &domain.FileServerInfo{Address: "fs2", UserCount: 3, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}

	gotID, ok := ms.getLeastLoadedHealthyFileServerLocked(now)
	if !ok {
		t.Fatalf("expected to find at least one healthy file server")
	}

	if gotID != 2 {
		t.Fatalf("least-loaded healthy server mismatch: got=%d want=2", gotID)
	}
}

func TestSetHeartbeatConfig(t *testing.T) {
	ms := newTestMetaServer(t)

	ms.SetHeartbeatConfig(60*time.Second, 8*time.Second)

	if ms.heartbeatTimeout != 60*time.Second {
		t.Fatalf("heartbeatTimeout mismatch: got=%v want=60s", ms.heartbeatTimeout)
	}
	if ms.heartbeatCheckInterval != 8*time.Second {
		t.Fatalf("heartbeatCheckInterval mismatch: got=%v want=8s", ms.heartbeatCheckInterval)
	}
}

func TestHandlerRegisterFileServerSuccessAndSharedMapping(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice", "bob"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "docs", Path: "alice/docs", Users: []string{"bob", "alice"}},
		},
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("RegisterFileServer failed: %s", resp.Error)
	}

	if ms.nextFsID != 1 {
		t.Fatalf("nextFsID mismatch: got=%d want=1", ms.nextFsID)
	}

	if got := ms.users["alice"]; got != 0 {
		t.Fatalf("alice mapping mismatch: got=%d want=0", got)
	}
	if got := ms.users["bob"]; got != 0 {
		t.Fatalf("bob mapping mismatch: got=%d want=0", got)
	}

	sharedForBob := ms.shared["bob"]
	if len(sharedForBob) != 1 {
		t.Fatalf("shared entries for bob mismatch: got=%d want=1", len(sharedForBob))
	}
	if sharedForBob[0].Owner != "alice" || sharedForBob[0].Path != "/alice/docs" || sharedForBob[0].DisplayName != "docs" {
		t.Fatalf("unexpected shared entry: %+v", sharedForBob[0])
	}

	if ms.fileservers[0].UserCount != 2 {
		t.Fatalf("user count mismatch: got=%d want=2", ms.fileservers[0].UserCount)
	}
}

func TestHandlerRegisterFileServerRejectsUserConflict(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	first, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice"},
	})
	if err != nil || !first.Success {
		t.Fatalf("first registration failed: err=%v resp=%+v", err, first)
	}

	second, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5002",
		Users:   []string{"alice"},
	})
	if err != nil {
		t.Fatalf("second registration returned RPC error: %v", err)
	}
	if second.Success {
		t.Fatalf("expected conflict registration to fail")
	}
	if !strings.Contains(second.Error, "already exists") {
		t.Fatalf("unexpected conflict error: %q", second.Error)
	}
}

func TestHandlerHeartbeat(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	_, _ = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice"},
	})

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: ""}); resp.Success {
		t.Fatalf("expected empty-address heartbeat to fail")
	}

	if resp, _ := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "unknown:5001"}); resp.Success {
		t.Fatalf("expected unknown-address heartbeat to fail")
	}

	resp, err := h.Heartbeat(context.Background(), &pb.HeartbeatRequest{Address: "127.0.0.1:5001"})
	if err != nil {
		t.Fatalf("heartbeat returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected heartbeat success, got error=%q", resp.Error)
	}
}

func TestHandlerNavigateAuthorizationMatrix(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	_, _ = h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address: "127.0.0.1:5001",
		Users:   []string{"alice", "bob", "charlie"},
		Shared: []*pb.SharedDir{
			{Owner: "alice", Name: "docs", Path: "alice/docs", Users: []string{"bob"}},
		},
	})

	ownerResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if !ownerResp.Success || ownerResp.Address != "127.0.0.1:5001" {
		t.Fatalf("owner navigate mismatch: %+v", ownerResp)
	}

	sharedResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "bob", RootUser: "alice"})
	if !sharedResp.Success || sharedResp.Address != "127.0.0.1:5001" {
		t.Fatalf("shared navigate mismatch: %+v", sharedResp)
	}

	deniedResp, _ := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "charlie", RootUser: "alice"})
	if deniedResp.Success {
		t.Fatalf("expected unauthorized navigate to fail")
	}
	if !strings.Contains(deniedResp.Error, "does not have access") {
		t.Fatalf("unexpected unauthorized error: %q", deniedResp.Error)
	}
}

func TestHandlerGetRootsAssignsLeastLoadedHealthyServer(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)
	now := time.Now().Unix()

	ms.fileservers[0] = &domain.FileServerInfo{Address: "fs0:5001", UserCount: 3, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.fileservers[1] = &domain.FileServerInfo{Address: "fs1:5001", UserCount: 1, LastHeartbeatUnix: now, Status: domain.FileServerStatusHealthy}
	ms.nextFsID = 2

	resp, err := h.GetRoots(context.Background(), &pb.GetRootsRequest{Username: "dave"})
	if err != nil {
		t.Fatalf("GetRoots returned error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("GetRoots failed: %s", resp.Error)
	}

	if got := ms.users["dave"]; got != 1 {
		t.Fatalf("user assignment mismatch: got=%d want=1", got)
	}

	if got := ms.fileservers[1].UserCount; got != 2 {
		t.Fatalf("updated user count mismatch: got=%d want=2", got)
	}

	if len(resp.Roots) != 1 {
		t.Fatalf("roots size mismatch: got=%d want=1", len(resp.Roots))
	}
	if resp.Roots[0].DisplayName != "mydrive" || resp.Roots[0].Owner != "dave" || resp.Roots[0].Path != "dave" {
		t.Fatalf("roots mismatch: got=%+v want={DisplayName:mydrive Owner:dave Path:dave}", resp.Roots[0])
	}
}

func TestHandlerRootShareAndUnshareLifecycle(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	ms.users["alice"] = 0
	ms.users["bob"] = 0
	ms.shared["bob"] = []SharedDirEntry{}

	shareResp, err := h.RootShare(context.Background(), &pb.RootShareRequest{
		Owner:     "alice",
		RootPath:  "/alice/project",
		ShareWith: "bob",
		Name:      "project",
	})
	if err != nil {
		t.Fatalf("RootShare returned error: %v", err)
	}
	if !shareResp.Success {
		t.Fatalf("RootShare failed: %s", shareResp.Error)
	}

	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("shared list length mismatch: got=%d want=1", got)
	}

	dupResp, _ := h.RootShare(context.Background(), &pb.RootShareRequest{
		Owner:     "alice",
		RootPath:  "/alice/project",
		ShareWith: "bob",
		Name:      "project",
	})
	if !dupResp.Success {
		t.Fatalf("duplicate RootShare should be idempotent success: %s", dupResp.Error)
	}
	if got := len(ms.shared["bob"]); got != 1 {
		t.Fatalf("duplicate share should not create extra entries: got=%d", got)
	}

	unshareResp, err := h.RootUnshare(context.Background(), &pb.RootUnshareRequest{
		Owner:       "alice",
		RootPath:    "/alice/project",
		UnshareWith: "bob",
		Name:        "project",
	})
	if err != nil {
		t.Fatalf("RootUnshare returned error: %v", err)
	}
	if !unshareResp.Success {
		t.Fatalf("RootUnshare failed: %s", unshareResp.Error)
	}

	if got := len(ms.shared["bob"]); got != 0 {
		t.Fatalf("shared list length after unshare mismatch: got=%d want=0", got)
	}
}

func TestLoadRevokedFileServerFingerprintsFromFile(t *testing.T) {
	ms := newTestMetaServer(t)

	fp1 := strings.Repeat("ab", 32)
	fp2 := strings.Repeat("cd", 32)
	var fp1WithColonsBuilder strings.Builder
	for i := 0; i < len(fp1); i += 2 {
		if i > 0 {
			fp1WithColonsBuilder.WriteString(":")
		}
		fp1WithColonsBuilder.WriteString(fp1[i : i+2])
	}

	revokedPath := filepath.Join(t.TempDir(), "revoked.txt")
	content := "# one-per-line or comma-separated\n" + fp1WithColonsBuilder.String() + "\n" + strings.ToUpper(fp2) + "\n"
	if err := os.WriteFile(revokedPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write revoked fingerprint file: %v", err)
	}

	added, err := ms.LoadRevokedFileServerFingerprintsFromFile(revokedPath)
	if err != nil {
		t.Fatalf("LoadRevokedFileServerFingerprintsFromFile failed: %v", err)
	}
	if added != 2 {
		t.Fatalf("added revoked fingerprint count mismatch: got=%d want=2", added)
	}

	if _, ok := ms.revokedFileServerCertFingerprints[fp1]; !ok {
		t.Fatalf("expected normalized fingerprint %s to be loaded", fp1)
	}
	if _, ok := ms.revokedFileServerCertFingerprints[fp2]; !ok {
		t.Fatalf("expected normalized fingerprint %s to be loaded", fp2)
	}

	reloaded, err := NewMetaServer(ms.stateFile)
	if err != nil {
		t.Fatalf("reloading metaserver failed: %v", err)
	}
	if _, ok := reloaded.revokedFileServerCertFingerprints[fp1]; !ok {
		t.Fatalf("expected fingerprint %s to persist in state", fp1)
	}
	if _, ok := reloaded.revokedFileServerCertFingerprints[fp2]; !ok {
		t.Fatalf("expected fingerprint %s to persist in state", fp2)
	}
}

func TestHandlerRegisterFileServerRejectsRevokedFingerprint(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	revoked := strings.Repeat("ef", 32)
	ms.revokedFileServerCertFingerprints[revoked] = struct{}{}

	resp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address:                     "127.0.0.1:5001",
		Users:                       []string{"alice"},
		ServerCertFingerprintSha256: revoked,
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected registration with revoked fingerprint to fail")
	}
	if !strings.Contains(resp.Error, "revoked") {
		t.Fatalf("expected revoked error, got=%q", resp.Error)
	}
}

func TestHandlerNavigateRejectsRevokedFileServerFingerprint(t *testing.T) {
	ms := newTestMetaServer(t)
	h := NewGRPCHandler(ms)

	fp := strings.Repeat("aa", 32)
	regResp, err := h.RegisterFileServer(context.Background(), &pb.RegisterFileServerRequest{
		Address:                     "127.0.0.1:5001",
		Users:                       []string{"alice"},
		ServerCertFingerprintSha256: fp,
	})
	if err != nil {
		t.Fatalf("RegisterFileServer returned error: %v", err)
	}
	if !regResp.Success {
		t.Fatalf("registration failed unexpectedly: %s", regResp.Error)
	}

	ms.revokedFileServerCertFingerprints[fp] = struct{}{}

	navResp, err := h.Navigate(context.Background(), &pb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if err != nil {
		t.Fatalf("Navigate returned error: %v", err)
	}
	if navResp.Success {
		t.Fatalf("expected navigate to fail for revoked fileserver fingerprint")
	}
	if !strings.Contains(strings.ToLower(navResp.Error), "revoked") {
		t.Fatalf("expected revoked navigate error, got=%q", navResp.Error)
	}
}
