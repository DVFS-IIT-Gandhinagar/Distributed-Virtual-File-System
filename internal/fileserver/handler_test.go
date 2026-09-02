package fileserver

import (
	"context"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func setupHandlerTest(t *testing.T) (*GRPCHandler, *FileServer) {
	t.Helper()

	fs := newTestFileServer(t)
	h := NewGRPCHandler(fs)
	return h, fs
}

func TestHandlerRegisterClientAndShareAccess(t *testing.T) {
	h, _ := setupHandlerTest(t)

	badResp, err := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "", RootUser: "", RootPath: ""})
	if err != nil {
		t.Fatalf("RegisterClient returned RPC error: %v", err)
	}
	if badResp.Success {
		t.Fatalf("expected RegisterClient with missing fields to fail")
	}

	aliceResp, err := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if err != nil || !aliceResp.Success {
		t.Fatalf("alice RegisterClient failed: err=%v resp=%+v", err, aliceResp)
	}

	if resp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "bob", RootUser: "alice", RootPath: "alice"}); resp.Success {
		t.Fatalf("expected bob registration on alice root to fail before share")
	}

	shareResp, err := h.Share(context.Background(), &pb.ShareRequest{
		Username:  "alice",
		Fid:       aliceResp.UserRootFid,
		ShareWith: "bob",
	})
	if err != nil || !shareResp.Success {
		t.Fatalf("Share failed: err=%v resp=%+v", err, shareResp)
	}

	bobResp, err := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "bob", RootUser: "alice", RootPath: "alice"})
	if err != nil || !bobResp.Success {
		t.Fatalf("expected bob registration on shared root to succeed: err=%v resp=%+v", err, bobResp)
	}
}

func TestHandlerCreateListReadWriteGetAttrPathChangeDir(t *testing.T) {
	h, _ := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("root registration failed: %+v", rootResp)
	}

	rootFID := rootResp.UserRootFid

	dirResp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "docs",
		RootUser: "alice",
		Fid:      rootFID,
		Type:     pb.InodeType_DIRECTORY,
	})
	if err != nil || !dirResp.Success {
		t.Fatalf("CreateFile directory failed: err=%v resp=%+v", err, dirResp)
	}

	changeResp, err := h.ChangeDir(context.Background(), &pb.ChangeDirRequest{
		Fid:     rootFID,
		RootFid: rootFID,
		Path:    "docs",
	})
	if err != nil || !changeResp.Success {
		t.Fatalf("ChangeDir docs failed: err=%v resp=%+v", err, changeResp)
	}

	fileResp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "note.txt",
		RootUser: "alice",
		Fid:      changeResp.NewFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil || !fileResp.Success {
		t.Fatalf("CreateFile file failed: err=%v resp=%+v", err, fileResp)
	}

	wrResp, err := h.WriteFile(context.Background(), &pb.WriteFileRequest{
		ParentFid: changeResp.NewFid,
		Name:      "note.txt",
		Offset:    0,
		Data:      []byte("handler data"),
	})
	if err != nil || !wrResp.Success {
		t.Fatalf("WriteFile failed: err=%v resp=%+v", err, wrResp)
	}

	rdResp, err := h.ReadFile(context.Background(), &pb.ReadFileRequest{
		ParentFid: changeResp.NewFid,
		Name:      "note.txt",
	})
	if err != nil || !rdResp.Success {
		t.Fatalf("ReadFile failed: err=%v resp=%+v", err, rdResp)
	}
	if got, want := string(rdResp.Data), "handler data"; got != want {
		t.Fatalf("ReadFile content mismatch: got=%q want=%q", got, want)
	}

	attrResp, err := h.GetAttr(context.Background(), &pb.GetAttrRequest{Fid: fileResp.Fid})
	if err != nil || !attrResp.Success {
		t.Fatalf("GetAttr failed: err=%v resp=%+v", err, attrResp)
	}
	if attrResp.Name != "note.txt" {
		t.Fatalf("GetAttr name mismatch: got=%q", attrResp.Name)
	}

	listResp, err := h.ListDir(context.Background(), &pb.ListDirRequest{Fid: changeResp.NewFid})
	if err != nil || !listResp.Success {
		t.Fatalf("ListDir failed: err=%v resp=%+v", err, listResp)
	}
	if len(listResp.Entries) != 1 || listResp.Entries[0].Name != "note.txt" {
		t.Fatalf("ListDir entries mismatch: %+v", listResp.Entries)
	}

	pathResp, err := h.Path(context.Background(), &pb.PathRequest{Fid: changeResp.NewFid, RootUser: "alice"})
	if err != nil || !pathResp.Success {
		t.Fatalf("Path failed: err=%v resp=%+v", err, pathResp)
	}
}

func TestHandlerDeleteTrashRestoreLifecycle(t *testing.T) {
	h, _ := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("registration failed: %+v", rootResp)
	}

	createResp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "temp.txt",
		RootUser: "alice",
		Fid:      rootResp.UserRootFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil || !createResp.Success {
		t.Fatalf("CreateFile failed: err=%v resp=%+v", err, createResp)
	}

	trashResp, err := h.TrashFile(context.Background(), &pb.TrashFileRequest{
		Fid:      createResp.Fid,
		RootUser: "alice",
	})
	if err != nil || !trashResp.Success {
		t.Fatalf("TrashFile failed: err=%v resp=%+v", err, trashResp)
	}

	restoreResp, err := h.RestoreFile(context.Background(), &pb.RestoreFileRequest{
		Fid:      createResp.Fid,
		RootUser: "alice",
		Username: "alice",
	})
	if err != nil || !restoreResp.Success {
		t.Fatalf("RestoreFile failed: err=%v resp=%+v", err, restoreResp)
	}

	delResp, err := h.DeleteFile(context.Background(), &pb.DeleteFileRequest{
		Fid:      createResp.Fid,
		RootUser: "alice",
	})
	if err != nil || !delResp.Success {
		t.Fatalf("DeleteFile failed: err=%v resp=%+v", err, delResp)
	}

	if badDel, _ := h.DeleteFile(context.Background(), &pb.DeleteFileRequest{Fid: nil, RootUser: "alice"}); badDel.Success {
		t.Fatalf("expected DeleteFile with nil fid to fail")
	}
}

func TestHandlerRejectsNestedCreatePath(t *testing.T) {
	h, _ := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("registration failed: %+v", rootResp)
	}

	resp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "nested/path.txt",
		RootUser: "alice",
		Fid:      rootResp.UserRootFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil {
		t.Fatalf("CreateFile returned RPC error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected nested path create to fail")
	}
}

func TestHandlerCreateFileQuotaExceeded(t *testing.T) {
	h, fs := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("registration failed: %+v", rootResp)
	}

	rootFID := domain.FIDFromProto(rootResp.UserRootFid)
	rootInode, err := fs.GetInode(rootFID)
	if err != nil {
		t.Fatalf("GetInode(root) failed: %v", err)
	}
	rootInode.Size = storageQuota + 1

	resp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "will-fail.txt",
		RootUser: "alice",
		Fid:      rootResp.UserRootFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil {
		t.Fatalf("CreateFile returned RPC error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected CreateFile to fail when quota is exceeded")
	}
}

func TestHandlerRejectsChangeDirToTrash(t *testing.T) {
	h, _ := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("registration failed: %+v", rootResp)
	}

	resp, err := h.ChangeDir(context.Background(), &pb.ChangeDirRequest{
		Fid:     rootResp.UserRootFid,
		RootFid: rootResp.UserRootFid,
		Path:    ".trash",
	})
	if err != nil {
		t.Fatalf("ChangeDir returned rpc error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected cd .trash to be rejected")
	}
}

func TestHandlerShowTrashListsEntries(t *testing.T) {
	h, _ := setupHandlerTest(t)

	rootResp, _ := h.RegisterClient(context.Background(), &pb.RegisterClientRequest{Username: "alice", RootUser: "alice", RootPath: "alice"})
	if !rootResp.Success {
		t.Fatalf("registration failed: %+v", rootResp)
	}

	createResp, err := h.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     "temp.txt",
		RootUser: "alice",
		Fid:      rootResp.UserRootFid,
		Type:     pb.InodeType_FILE,
	})
	if err != nil || !createResp.Success {
		t.Fatalf("CreateFile failed: err=%v resp=%+v", err, createResp)
	}

	trashResp, err := h.TrashFile(context.Background(), &pb.TrashFileRequest{
		Fid:      createResp.Fid,
		RootUser: "alice",
	})
	if err != nil || !trashResp.Success {
		t.Fatalf("TrashFile failed: err=%v resp=%+v", err, trashResp)
	}

	showResp, err := h.ShowTrash(context.Background(), &pb.ShowTrashRequest{RootUser: "alice", Username: "alice"})
	if err != nil {
		t.Fatalf("ShowTrash returned rpc error: %v", err)
	}
	if !showResp.Success {
		t.Fatalf("ShowTrash failed: %+v", showResp)
	}

	if len(showResp.Entries) == 0 {
		t.Fatalf("expected trash entries to be listed")
	}
	if showResp.Entries[0].Name == "" {
		t.Fatalf("expected listed entry name to be set")
	}
}
