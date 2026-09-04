package client

import (
	"net"
	"testing"

	fspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver"
	"google.golang.org/grpc"
)

func TestClientRefreshRecoversFromFileserverRestart(t *testing.T) {
	rootDir := t.TempDir()

	// 1. Start fileserver on a fixed port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	serverAddr := lis.Addr().String()

	fs1, err := fileserver.NewFileServer("fs-1", rootDir, false, "", "")
	if err != nil {
		t.Fatalf("NewFileServer 1 failed: %v", err)
	}

	grpcServer1 := grpc.NewServer()
	fspb.RegisterFileServerServer(grpcServer1, fileserver.NewGRPCHandler(fs1))

	go func() {
		_ = grpcServer1.Serve(lis)
	}()

	// 2. Connect client and initialize cache
	c := NewClient("jassi", false, "")
	c.SetRootUser("jassi")
	c.SetRootPath("mydrive", "jassi")

	rootFID, err := c.Connect(serverAddr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	cacheHandler := NewCacheHandler(c, rootFID)
	if cacheHandler == nil {
		t.Fatalf("NewCacheHandler returned nil")
	}
	c.AttachCacheHandler(cacheHandler)

	// Create a test file: proj.c3p
	_, err = cacheHandler.CreateFile("proj.c3p")
	if err != nil {
		t.Fatalf("CreateFile proj.c3p failed: %v", err)
	}

	files, err := cacheHandler.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("expected at least 2 files (.trash and proj.c3p), got %d", len(files))
	}

	// 3. Stop fileserver instance 1
	grpcServer1.Stop()
	_ = lis.Close()

	// 4. Restart fileserver instance 2 on the SAME rootDir and SAME address
	lis2, err := net.Listen("tcp", serverAddr)
	if err != nil {
		t.Fatalf("failed to re-listen on %s: %v", serverAddr, err)
	}
	defer lis2.Close()

	fs2, err := fileserver.NewFileServer("fs-1", rootDir, false, "", "")
	if err != nil {
		t.Fatalf("NewFileServer 2 (restart) failed: %v", err)
	}

	grpcServer2 := grpc.NewServer()
	fspb.RegisterFileServerServer(grpcServer2, fileserver.NewGRPCHandler(fs2))
	defer grpcServer2.Stop()

	go func() {
		_ = grpcServer2.Serve(lis2)
	}()

	// 5. Client calls Refresh() - should recover session and keep files
	if err := cacheHandler.Refresh(); err != nil {
		t.Fatalf("Refresh() failed: %v", err)
	}

	// 6. List files - must NOT be empty!
	recoveredFiles, err := cacheHandler.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles after refresh failed: %v", err)
	}

	if len(recoveredFiles) == 0 {
		t.Fatalf("BUG REPRODUCED: directory is empty after refresh! Cache was wiped.")
	}

	foundProj := false
	foundTrash := false
	for _, f := range recoveredFiles {
		if f.Name == "proj.c3p" {
			foundProj = true
		}
		if f.Name == ".trash" {
			foundTrash = true
		}
	}

	if !foundProj {
		t.Errorf("expected to find 'proj.c3p' after fileserver restart and refresh")
	}
	if !foundTrash {
		t.Errorf("expected to find '.trash' after fileserver restart and refresh")
	}
}
