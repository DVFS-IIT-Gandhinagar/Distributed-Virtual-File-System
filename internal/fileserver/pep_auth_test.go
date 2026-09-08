package fileserver

import (
	"context"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contextWithUser(user string) context.Context {
	sess := &session.Session{
		HashID:      "test-session-hash",
		Username:    user,
		ClientID:    "test-client-id",
		ClientIP:    "127.0.0.1",
		CreatedAt:   time.Now(),
		LastSeenAt:  time.Now(),
		AbsoluteExp: time.Now().Add(1 * time.Hour),
	}
	return session.WithSession(context.Background(), sess)
}

// TestPEP_Authorize_Direct verifies permission enforcement across different permission levels.
func TestPEP_Authorize_Direct(t *testing.T) {
	fs := newTestFileServer(t)

	aliceRoot, err := fs.GetUserRoot("alice@example.com", "alice@example.com")
	require.NoError(t, err)

	fileFID, err := fs.CreateFile(aliceRoot, "document.txt", "alice@example.com", domain.InodeTypeFile)
	require.NoError(t, err)

	ctxAlice := contextWithUser("alice@example.com")
	ctxBob := contextWithUser("bob@example.com")

	// 1. Alice (owner) has full access
	_, caller, err := fs.Authorize(ctxAlice, fileFID, PermRead)
	assert.NoError(t, err)
	assert.Equal(t, "alice@example.com", caller)

	_, _, err = fs.Authorize(ctxAlice, fileFID, PermWrite)
	assert.NoError(t, err)

	_, _, err = fs.Authorize(ctxAlice, fileFID, PermDelete)
	assert.NoError(t, err)

	// 2. Bob (unauthorized) is denied all access
	_, _, err = fs.Authorize(ctxBob, fileFID, PermRead)
	assert.Error(t, err)

	_, _, err = fs.Authorize(ctxBob, fileFID, PermWrite)
	assert.Error(t, err)

	_, _, err = fs.Authorize(ctxBob, fileFID, PermDelete)
	assert.Error(t, err)

	// 3. Share with Bob
	fileInode, err := fs.GetInode(fileFID)
	require.NoError(t, err)
	fileInode.ACL.Shared = append(fileInode.ACL.Shared, "bob@example.com")

	// Bob can read and write now
	_, _, err = fs.Authorize(ctxBob, fileFID, PermRead)
	assert.NoError(t, err)

	_, _, err = fs.Authorize(ctxBob, fileFID, PermWrite)
	assert.NoError(t, err)

	// Bob CANNOT delete (delete is strictly owner-only)
	_, _, err = fs.Authorize(ctxBob, fileFID, PermDelete)
	assert.Error(t, err, "Delete must be restricted to file owner")

	// 4. Test PermAdmin
	t.Setenv("DVFS_ADMIN_EMAIL", "admin@example.com")
	ctxAdmin := contextWithUser("admin@example.com")

	_, _, err = fs.Authorize(ctxAdmin, fileFID, PermAdmin)
	assert.NoError(t, err)

	_, _, err = fs.Authorize(ctxAlice, fileFID, PermAdmin)
	assert.Error(t, err, "Non-admin must be denied PermAdmin")
}

// TestPEP_Handler_IDORPrevention verifies that an attacker cannot delete or trash
// another user's file even if they specify the victim's name in req.RootUser.
func TestPEP_Handler_IDORPrevention(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)

	aliceRoot, err := fs.GetUserRoot("alice@example.com", "alice@example.com")
	require.NoError(t, err)

	fileFID, err := fs.CreateFile(aliceRoot, "secret_notes.txt", "alice@example.com", domain.InodeTypeFile)
	require.NoError(t, err)

	ctxAttacker := contextWithUser("attacker@example.com")

	// Attacker attempts DeleteFile pretending to be alice
	delResp, err := handler.DeleteFile(ctxAttacker, &pb.DeleteFileRequest{
		Fid:      fileFID.ToProto(),
		RootUser: "alice@example.com", // Spoofed victim parameter
	})
	require.NoError(t, err)
	assert.False(t, delResp.Success, "Spoofed DeleteFile must fail")
	assert.Contains(t, delResp.Error, "permission denied")

	// Verify file still exists
	inode, err := fs.GetInode(fileFID)
	assert.NoError(t, err)
	assert.NotNil(t, inode)

	// Attacker attempts TrashFile pretending to be alice
	trashResp, err := handler.TrashFile(ctxAttacker, &pb.TrashFileRequest{
		Fid:      fileFID.ToProto(),
		RootUser: "alice@example.com", // Spoofed victim parameter
	})
	require.NoError(t, err)
	assert.False(t, trashResp.Success, "Spoofed TrashFile must fail")
	assert.Contains(t, trashResp.Error, "permission denied")

	// Legitimate owner Alice can trash her own file
	ctxAlice := contextWithUser("alice@example.com")
	trashRespAlice, err := handler.TrashFile(ctxAlice, &pb.TrashFileRequest{
		Fid:      fileFID.ToProto(),
		RootUser: "alice@example.com",
	})
	require.NoError(t, err)
	assert.True(t, trashRespAlice.Success, "Legitimate owner TrashFile should succeed")
}

// TestPEP_Handler_GetAttr_ListDir_IDORPrevention verifies that unshared files/dirs
// cannot be read or listed by an unauthorized user.
func TestPEP_Handler_GetAttr_ListDir_IDORPrevention(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)

	aliceRoot, err := fs.GetUserRoot("alice@example.com", "alice@example.com")
	require.NoError(t, err)

	fileFID, err := fs.CreateFile(aliceRoot, "classified.txt", "alice@example.com", domain.InodeTypeFile)
	require.NoError(t, err)

	dirFID, err := fs.CreateFile(aliceRoot, "private_folder", "alice@example.com", domain.InodeTypeDirectory)
	require.NoError(t, err)

	ctxAttacker := contextWithUser("attacker@example.com")
	ctxAlice := contextWithUser("alice@example.com")

	// 1. Attacker GetAttr on Alice's file
	attrResp, err := handler.GetAttr(ctxAttacker, &pb.GetAttrRequest{
		Fid: fileFID.ToProto(),
	})
	require.NoError(t, err)
	assert.False(t, attrResp.Success)
	assert.Contains(t, attrResp.Error, "permission denied")

	// Alice GetAttr succeeds
	attrRespAlice, err := handler.GetAttr(ctxAlice, &pb.GetAttrRequest{
		Fid: fileFID.ToProto(),
	})
	require.NoError(t, err)
	assert.True(t, attrRespAlice.Success)
	assert.Equal(t, "classified.txt", attrRespAlice.Name)

	// 2. Attacker ListDir on Alice's directory
	listResp, err := handler.ListDir(ctxAttacker, &pb.ListDirRequest{
		Fid: dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.False(t, listResp.Success)
	assert.Contains(t, listResp.Error, "permission denied")

	// Alice ListDir succeeds
	listRespAlice, err := handler.ListDir(ctxAlice, &pb.ListDirRequest{
		Fid: dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.True(t, listRespAlice.Success)
}

// TestPEP_Handler_Share_Unshare_IDORPrevention verifies that only the actual resource
// owner can share or unshare a directory.
func TestPEP_Handler_Share_Unshare_IDORPrevention(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)

	aliceRoot, err := fs.GetUserRoot("alice@example.com", "alice@example.com")
	require.NoError(t, err)

	dirFID, err := fs.CreateFile(aliceRoot, "team_collab", "alice@example.com", domain.InodeTypeDirectory)
	require.NoError(t, err)

	ctxAttacker := contextWithUser("attacker@example.com")
	ctxAlice := contextWithUser("alice@example.com")

	// Attacker attempts to share Alice's directory with Eve pretending to be Alice
	shareResp, err := handler.Share(ctxAttacker, &pb.ShareRequest{
		Username:  "alice@example.com", // Spoofed username
		ShareWith: "eve@example.com",
		Fid:       dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.False(t, shareResp.Success)
	assert.Contains(t, shareResp.Error, "permission denied")

	// Alice legitimately shares with Eve
	shareRespAlice, err := handler.Share(ctxAlice, &pb.ShareRequest{
		Username:  "alice@example.com",
		ShareWith: "eve@example.com",
		Fid:       dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.True(t, shareRespAlice.Success)

	// Attacker attempts to unshare Alice's directory
	unshareResp, err := handler.Unshare(ctxAttacker, &pb.UnshareRequest{
		Username:    "alice@example.com",
		UnshareWith: "eve@example.com",
		Fid:         dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.False(t, unshareResp.Success)
	assert.Contains(t, unshareResp.Error, "permission denied")

	// Alice legitimately unshares
	unshareRespAlice, err := handler.Unshare(ctxAlice, &pb.UnshareRequest{
		Username:    "alice@example.com",
		UnshareWith: "eve@example.com",
		Fid:         dirFID.ToProto(),
	})
	require.NoError(t, err)
	assert.True(t, unshareRespAlice.Success)
}

// TestPEP_Handler_ShowTrash_RestoreFile_IDORPrevention verifies that trash contents
// and restore operations are isolated to the authenticated owner.
func TestPEP_Handler_ShowTrash_RestoreFile_IDORPrevention(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)

	aliceRoot, err := fs.GetUserRoot("alice@example.com", "alice@example.com")
	require.NoError(t, err)

	fileFID, err := fs.CreateFile(aliceRoot, "trashed_doc.txt", "alice@example.com", domain.InodeTypeFile)
	require.NoError(t, err)

	ctxAlice := contextWithUser("alice@example.com")
	ctxAttacker := contextWithUser("attacker@example.com")

	// Alice trashes her file
	trashResp, err := handler.TrashFile(ctxAlice, &pb.TrashFileRequest{
		Fid:       fileFID.ToProto(),
		RootUser:  "alice@example.com",
		Recursive: false,
	})
	require.NoError(t, err)
	require.True(t, trashResp.Success)

	// Attacker tries to view Alice's trash
	showResp, err := handler.ShowTrash(ctxAttacker, &pb.ShowTrashRequest{
		RootUser: "alice@example.com",
		Username: "alice@example.com",
	})
	require.NoError(t, err)
	assert.False(t, showResp.Success)
	assert.Contains(t, showResp.Error, "permission denied")

	// Alice can view her own trash
	showRespAlice, err := handler.ShowTrash(ctxAlice, &pb.ShowTrashRequest{
		RootUser: "alice@example.com",
		Username: "alice@example.com",
	})
	require.NoError(t, err)
	assert.True(t, showRespAlice.Success)
	assert.NotEmpty(t, showRespAlice.Entries)

	// Attacker tries to restore Alice's file
	restoreResp, err := handler.RestoreFile(ctxAttacker, &pb.RestoreFileRequest{
		Fid:      fileFID.ToProto(),
		RootUser: "alice@example.com",
		Username: "alice@example.com",
	})
	require.NoError(t, err)
	assert.False(t, restoreResp.Success)
	assert.Contains(t, restoreResp.Error, "permission denied")

	// Alice restores her own file
	restoreRespAlice, err := handler.RestoreFile(ctxAlice, &pb.RestoreFileRequest{
		Fid:      fileFID.ToProto(),
		RootUser: "alice@example.com",
		Username: "alice@example.com",
	})
	require.NoError(t, err)
	assert.True(t, restoreRespAlice.Success)
}

