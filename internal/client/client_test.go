package client

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fspb "github.com/umangshikarvar/dvfs/api/fileserver"
	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/fileserver"
	"github.com/umangshikarvar/dvfs/internal/metaserver"
	"google.golang.org/grpc"
)

func startTestFileServerGRPC(t *testing.T, msAddr string) (addr string, fs *fileserver.FileServer, cleanup func()) {
	t.Helper()

	rootDir := t.TempDir()
	fs, err := fileserver.NewFileServer("fs-test", rootDir, false, msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for file server: %v", err)
	}

	grpcServer := grpc.NewServer()
	fspb.RegisterFileServerServer(grpcServer, fileserver.NewGRPCHandler(fs))

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	cleanup = func() {
		grpcServer.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), fs, cleanup
}

func startTestMetaServerGRPC(t *testing.T) (addr string, ms *metaserver.MetaServer, cleanup func()) {
	t.Helper()

	msState := filepath.Join(t.TempDir(), "mds", "state.json")
	ms, err := metaserver.NewMetaServer(msState)
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for metaserver: %v", err)
	}

	grpcServer := grpc.NewServer()
	mspb.RegisterMetaServerServer(grpcServer, metaserver.NewGRPCHandler(ms))

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	cleanup = func() {
		grpcServer.Stop()
		_ = lis.Close()
	}

	return lis.Addr().String(), ms, cleanup
}

func connectTestClient(t *testing.T, username, rootUser, fsAddr string) *Client {
	t.Helper()

	c := NewClient(username, false)
	c.SetRootUser(rootUser)
	c.SetRootPath("mydrive", rootUser)
	if _, err := c.Connect(fsAddr); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	return c
}

func TestClientConnectAndCRUDFlow(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateDirectory("docs"); err != nil {
		t.Fatalf("CreateDirectory failed: %v", err)
	}

	if _, err := c.ChangeDirectory("docs"); err != nil {
		t.Fatalf("ChangeDirectory failed: %v", err)
	}

	if _, err := c.CreateFile("hello.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	if err := c.WriteFile("hello.txt", []byte("hello world")); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, err := c.ReadFile("hello.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(data), "hello world"; got != want {
		t.Fatalf("ReadFile content mismatch: got=%q want=%q", got, want)
	}

	path, err := c.Path()
	if err != nil {
		t.Fatalf("Path failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(path), "mydrive") {
		t.Fatalf("unexpected client path: %q", path)
	}
}

func TestClientUploadAndDownloadFile(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tempWD := t.TempDir()
	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	localFile := filepath.Join(tempWD, "upload-me.txt")
	if err := os.WriteFile(localFile, []byte("payload from local"), 0644); err != nil {
		t.Fatalf("failed to create local upload file: %v", err)
	}

	if _, err := c.Upload(localFile); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if err := c.Download("upload-me.txt"); err != nil {
		t.Fatalf("Download failed: %v", err)
	}

	downloadedPath := filepath.Join(tempWD, "Download", "upload-me.txt")
	data, err := os.ReadFile(downloadedPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if got, want := string(data), "payload from local"; got != want {
		t.Fatalf("downloaded content mismatch: got=%q want=%q", got, want)
	}
}

func TestClientDeleteTrashRestoreFlow(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateFile("delete-me.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}

	if _, err := c.TrashFile("delete-me.txt", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	if _, err := c.RestoreFile("delete-me.txt"); err != nil {
		t.Fatalf("RestoreFile failed: %v", err)
	}

	if err := c.DeleteFile("delete-me.txt", false); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
}

func TestClientShowTrash(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateFile("to-trash.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if _, err := c.TrashFile("to-trash.txt", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	entries, err := c.ShowTrash()
	if err != nil {
		t.Fatalf("ShowTrash failed: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "to-trash.txt") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected trashed file to appear in show_trash output")
	}
}

func TestClientRejectsTrashPathNavigation(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.GetFIDForPath(".trash"); err == nil {
		t.Fatalf("expected GetFIDForPath to reject .trash path")
	}

	if err := c.Download(".trash/any.txt"); err == nil {
		t.Fatalf("expected Download to reject .trash path")
	}
}

func TestClientClearTrash(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateFile("wipe-me.txt"); err != nil {
		t.Fatalf("CreateFile failed: %v", err)
	}
	if _, err := c.TrashFile("wipe-me.txt", false); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	deleted, err := c.ClearTrash()
	if err != nil {
		t.Fatalf("ClearTrash failed: %v", err)
	}
	if deleted == 0 {
		t.Fatalf("expected ClearTrash to delete at least one entry")
	}

	entries, err := c.ShowTrash()
	if err != nil {
		t.Fatalf("ShowTrash failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected trash to be empty after clear_trash, got %d entries", len(entries))
	}
}

func TestClientDeleteFromTrash(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateDirectory("trash-dir"); err != nil {
		t.Fatalf("CreateDirectory failed: %v", err)
	}
	if _, err := c.TrashFile("trash-dir", true); err != nil {
		t.Fatalf("TrashFile failed: %v", err)
	}

	if err := c.DeleteFromTrash("trash-dir"); err != nil {
		t.Fatalf("DeleteFromTrash failed: %v", err)
	}

	entries, err := c.ShowTrash()
	if err != nil {
		t.Fatalf("ShowTrash failed: %v", err)
	}
	for _, e := range entries {
		if e.Name == "trash-dir" {
			t.Fatalf("expected trash-dir to be permanently removed from trash")
		}
	}
}

func TestClientMetaServerRootsAndNavigation(t *testing.T) {
	mdsAddr, _, cleanupMDS := startTestMetaServerGRPC(t)
	defer cleanupMDS()

	fsAddr, fs, cleanupFS := startTestFileServerGRPC(t, mdsAddr)
	defer cleanupFS()

	if err := fs.RegisterWithMetaServer(fsAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	c := NewClient("alice", false)

	roots, err := c.GetRoots(mdsAddr)
	if err != nil {
		t.Fatalf("GetRoots failed: %v", err)
	}
	if len(roots) == 0 || roots[0].DisplayName != "mydrive" || roots[0].Owner != "alice" || roots[0].Path != "alice" {
		t.Fatalf("unexpected roots response: %v", roots)
	}
	c.SetRootPath(roots[0].DisplayName, roots[0].Path)
	c.SetRootUser(roots[0].Owner)

	navAddr, err := c.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("NavigateToFileServer failed: %v", err)
	}
	if navAddr != fsAddr {
		t.Fatalf("navigated address mismatch: got=%s want=%s", navAddr, fsAddr)
	}

	if _, err := c.Connect(navAddr); err != nil {
		t.Fatalf("Connect after navigation failed: %v", err)
	}
}

func TestClientEdgeCases(t *testing.T) {
	t.Run("connect invalid address", func(t *testing.T) {
		c := NewClient("alice", false)
		if _, err := c.Connect("127.0.0.1:1"); err == nil {
			t.Fatalf("expected Connect to invalid address to fail")
		}
	})

	t.Run("delete missing file", func(t *testing.T) {
		fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
		defer cleanupFS()

		c := connectTestClient(t, "alice", "alice", fsAddr)
		if err := c.DeleteFile("missing.txt", false); err == nil {
			t.Fatalf("expected deleting missing file to fail")
		}
	})

	t.Run("trash empty name", func(t *testing.T) {
		fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
		defer cleanupFS()

		c := connectTestClient(t, "alice", "alice", fsAddr)
		if _, err := c.TrashFile("", false); err == nil {
			t.Fatalf("expected empty-name trash to fail")
		}
	})

	t.Run("restore missing file in trash", func(t *testing.T) {
		fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
		defer cleanupFS()

		c := connectTestClient(t, "alice", "alice", fsAddr)
		if _, err := c.RestoreFile("missing.txt"); err == nil {
			t.Fatalf("expected restoring missing trash entry to fail")
		}
	})

	t.Run("GetRoots and Navigate no-op for empty msAddr", func(t *testing.T) {
		c := NewClient("alice", false)
		roots, err := c.GetRoots("")
		if err != nil {
			t.Fatalf("GetRoots with empty msAddr should not fail: %v", err)
		}
		if len(roots) != 0 {
			t.Fatalf("expected empty roots for empty msAddr, got=%v", roots)
		}

		addr, err := c.NavigateToFileServer("")
		if err != nil {
			t.Fatalf("NavigateToFileServer with empty msAddr should not fail: %v", err)
		}
		if addr != "" {
			t.Fatalf("expected empty address for empty msAddr, got=%q", addr)
		}
	})
}

func TestClientSharedRootSelectionFlow(t *testing.T) {
	mdsAddr, _, cleanupMDS := startTestMetaServerGRPC(t)
	defer cleanupMDS()

	fsAddr, fs, cleanupFS := startTestFileServerGRPC(t, mdsAddr)
	defer cleanupFS()

	if err := fs.RegisterWithMetaServer(fsAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	alice := NewClient("alice", false)
	bob := NewClient("bob", false)

	// Ensure both users are assigned in metaserver.
	if _, err := alice.GetRoots(mdsAddr); err != nil {
		t.Fatalf("alice GetRoots failed: %v", err)
	}
	if _, err := bob.GetRoots(mdsAddr); err != nil {
		t.Fatalf("bob GetRoots failed: %v", err)
	}

	aliceMyDrive, err := alice.GetRoots(mdsAddr)
	if err != nil {
		t.Fatalf("alice GetRoots (second call) failed: %v", err)
	}
	alice.SetRootUser(aliceMyDrive[0].Owner)
	alice.SetRootPath(aliceMyDrive[0].DisplayName, aliceMyDrive[0].Path)

	aliceFS, err := alice.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("alice NavigateToFileServer failed: %v", err)
	}
	if _, err := alice.Connect(aliceFS); err != nil {
		t.Fatalf("alice Connect failed: %v", err)
	}

	if _, err := alice.CreateDirectory("team"); err != nil {
		t.Fatalf("alice CreateDirectory(team) failed: %v", err)
	}
	if _, err := alice.ChangeDirectory("team"); err != nil {
		t.Fatalf("alice ChangeDirectory(team) failed: %v", err)
	}
	if _, err := alice.CreateFile("shared.txt"); err != nil {
		t.Fatalf("alice CreateFile(shared.txt) failed: %v", err)
	}
	if err := alice.WriteFile("shared.txt", []byte("shared-from-team")); err != nil {
		t.Fatalf("alice WriteFile(shared.txt) failed: %v", err)
	}
	if err := alice.Share("bob"); err != nil {
		t.Fatalf("alice Share(bob) failed: %v", err)
	}

	bobRoots, err := bob.GetRoots(mdsAddr)
	if err != nil {
		t.Fatalf("bob GetRoots after share failed: %v", err)
	}

	var sharedRoot SharedRoot
	found := false
	for _, r := range bobRoots {
		if r.Owner == "alice" && r.DisplayName == "team" {
			sharedRoot = r
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected shared root from alice/team in bob roots: %+v", bobRoots)
	}

	bob.SetRootUser(sharedRoot.Owner)
	bob.SetRootPath(sharedRoot.DisplayName, sharedRoot.Path)
	bobFS, err := bob.NavigateToFileServer(mdsAddr)
	if err != nil {
		t.Fatalf("bob NavigateToFileServer(shared root) failed: %v", err)
	}
	if _, err := bob.Connect(bobFS); err != nil {
		t.Fatalf("bob Connect(shared root) failed: %v", err)
	}

	data, err := bob.ReadFile("shared.txt")
	if err != nil {
		t.Fatalf("bob ReadFile(shared.txt) failed: %v", err)
	}
	if got, want := string(data), "shared-from-team"; got != want {
		t.Fatalf("shared read mismatch: got=%q want=%q", got, want)
	}
}

func TestClientShareUnshareWithRealServer(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	alice := connectTestClient(t, "alice", "alice", fsAddr)
	bob := connectTestClient(t, "bob", "bob", fsAddr)

	_ = bob // ensure bob exists on server as a user root.

	if err := alice.Share("bob"); err != nil {
		t.Fatalf("Share failed: %v", err)
	}

	if err := alice.Unshare("bob"); err != nil {
		t.Fatalf("Unshare failed: %v", err)
	}
}

func TestClientGetFIDForPath(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if _, err := c.CreateDirectory("docs"); err != nil {
		t.Fatalf("CreateDirectory failed: %v", err)
	}

	fid, err := c.GetFIDForPath("docs")
	if err != nil {
		t.Fatalf("GetFIDForPath valid path failed: %v", err)
	}
	if fid == nil {
		t.Fatalf("GetFIDForPath returned nil FID")
	}

	if _, err := c.GetFIDForPath("docs/missing"); err == nil {
		t.Fatalf("expected invalid GetFIDForPath to fail")
	}
}

func TestClientDownloadMissingPath(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	if err := c.Download("nope.txt"); err == nil {
		t.Fatalf("expected Download on missing path to fail")
	}
}

func TestClientListFilesAtNilFID(t *testing.T) {
	c := NewClient("alice", false)
	if _, err := c.ListFilesAt(nil); err == nil {
		t.Fatalf("expected ListFilesAt(nil) to fail")
	}
}

func TestClientCanReconnectAndKeepWorking(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := NewClient("alice", false)
	c.SetRootUser("alice")
	c.SetRootPath("mydrive", "alice")
	if _, err := c.Connect(fsAddr); err != nil {
		t.Fatalf("first connect failed: %v", err)
	}
	if _, err := c.Connect(fsAddr); err != nil {
		t.Fatalf("second connect failed: %v", err)
	}

	if _, err := c.CreateFile("after-reconnect.txt"); err != nil {
		t.Fatalf("CreateFile after reconnect failed: %v", err)
	}
}

func TestClientContextDoesNotLeakOnSimpleRPCs(t *testing.T) {
	fsAddr, _, cleanupFS := startTestFileServerGRPC(t, "")
	defer cleanupFS()

	c := connectTestClient(t, "alice", "alice", fsAddr)

	// Smoke check repeated simple calls to ensure connection/client stays usable.
	for i := 0; i < 3; i++ {
		if _, err := c.GetFileInfo(); err != nil {
			t.Fatalf("GetFileInfo iteration %d failed: %v", i, err)
		}
		if _, err := c.ListFiles(); err != nil {
			t.Fatalf("ListFiles iteration %d failed: %v", i, err)
		}
	}

}
