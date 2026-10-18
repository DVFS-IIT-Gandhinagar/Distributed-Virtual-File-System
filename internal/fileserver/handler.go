package fileserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// GRPCHandler implements the gRPC file server interface
type GRPCHandler struct {
	pb.UnimplementedFileServerServer
	fileServer *FileServer
}

const chunkSize = 1024 * 1024 * 4 // 4MB

// NewGRPCHandler creates a new gRPC handler
func NewGRPCHandler(fileServer *FileServer) *GRPCHandler {
	return &GRPCHandler{
		fileServer: fileServer,
	}
}

func normalizeCallbackAddress(ctx context.Context, callbackAddress string) string {
	if callbackAddress == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(callbackAddress)
	if err != nil {
		return callbackAddress
	}

	peerInfo, ok := peer.FromContext(ctx)
	if !ok || peerInfo == nil || peerInfo.Addr == nil {
		return callbackAddress
	}

	remoteHost, _, err := net.SplitHostPort(peerInfo.Addr.String())
	if err != nil {
		remoteHost = peerInfo.Addr.String()
	}

	if remoteHost == "" {
		return callbackAddress
	}

	parsedHost := net.ParseIP(host)
	if host == "" || strings.EqualFold(host, "localhost") || host == "0.0.0.0" || host == "::" || host == "::1" || (parsedHost != nil && parsedHost.IsLoopback()) {
		return net.JoinHostPort(remoteHost, port)
	}

	return callbackAddress
}

// RegisterClient handles client registration and returns user root FID
func (h *GRPCHandler) RegisterClient(ctx context.Context, req *pb.RegisterClientRequest) (*pb.RegisterClientResponse, error) {
	log.Printf("RegisterClient: username=%s requesting for user root=%s", req.Username, req.RootUser)

	if req.Username == "" || req.RootUser == "" || req.RootPath == "" {
		log.Printf("RegisterClient: error - username ,request user root and path are required")
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   "username and request user root are required",
		}, nil
	}

	if req.Username != req.RootUser {
		// check if RootUser is present on this fs
		h.fileServer.mu.RLock()
		_, isPresent := h.fileServer.users[req.RootUser]
		h.fileServer.mu.RUnlock()
		if !isPresent {
			log.Printf("RegisterClient: error - user root %s is not present on this fs", req.RootUser)
			return &pb.RegisterClientResponse{
				Success: false,
				Error:   fmt.Sprintf("user root %s is not present on this fs", req.RootUser),
			}, nil
		}
	}

	rootFID, err := h.fileServer.GetUserRoot(req.RootPath, req.RootUser)
	if err != nil {
		log.Printf("RegisterClient: error getting requested root - %v", err)
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	h.fileServer.mu.RLock()
	rootInode, err := h.fileServer.GetInode(rootFID)
	if err != nil {
		h.fileServer.mu.RUnlock()
		log.Printf("RegisterClient: error getting root inode - %v", err)
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// registration should succeed only if user is owner or root is shared with them
	if rootInode.ACL.Owner != req.Username {
		// check if root is shared with this user
		isShared := false
		for _, u := range rootInode.ACL.Shared {
			if u == req.Username {
				isShared = true
				break
			}
		}
		if !isShared {
			h.fileServer.mu.RUnlock()
			log.Printf("RegisterClient: error - user %s is not allowed to access root %s", req.Username, req.RootPath)
			return &pb.RegisterClientResponse{
				Success: false,
				Error:   fmt.Sprintf("user %s is not allowed to access root %s", req.Username, req.RootUser),
			}, nil
		}
	}
	h.fileServer.mu.RUnlock()

	normalizedCallbackAddress := normalizeCallbackAddress(ctx, req.CallbackAddress)
	if normalizedCallbackAddress != req.CallbackAddress {
		log.Printf("RegisterClient: normalized callback address for user=%s from %s to %s", req.Username, req.CallbackAddress, normalizedCallbackAddress)
	}
	h.fileServer.UpsertClientSession(req.Username, normalizedCallbackAddress, rootFID)

	// Mint Server Session Token (SST) and record session in SessionStore
	var sessionToken string
	var expiresAt int64
	if store := h.fileServer.SessionStore(); store != nil {
		rawSST, err := session.GenerateSST()
		if err == nil {
			absExp := time.Now().Add(12 * time.Hour)
			peerAddr := ""
			if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
				peerAddr = p.Addr.String()
			}
			sess, err := store.CreateSession(
				rawSST,
				req.Username,
				req.ClientId,
				peerAddr,
				normalizedCallbackAddress,
				rootFID.String(),
				absExp,
			)
			if err == nil && sess != nil {
				sessionToken = rawSST
				expiresAt = sess.AbsoluteExp.Unix()
			}
		}
	}

	log.Printf("RegisterClient: success for user %s for the user root %s", req.Username, req.RootPath)
	return &pb.RegisterClientResponse{
		Success:      true,
		UserRootFid:  rootFID.ToProto(),
		SessionToken: sessionToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// UnregisterClient handles client disconnect and removes their active session
func (h *GRPCHandler) UnregisterClient(ctx context.Context, req *pb.UnregisterClientRequest) (*pb.UnregisterClientResponse, error) {
	if req == nil || req.Username == "" {
		return &pb.UnregisterClientResponse{
			Success: false,
			Error:   "username is required",
		}, nil
	}

	log.Printf("UnregisterClient: user '%s' disconnected (clientId: %s)", req.Username, req.ClientId)
	h.fileServer.RemoveClientSession(req.Username)
	if store := h.fileServer.SessionStore(); store != nil && req.ClientId != "" {
		_ = store.RevokeByClientID(req.ClientId)
	}
	return &pb.UnregisterClientResponse{
		Success: true,
	}, nil
}

// GetAttr gets file attributes
func (h *GRPCHandler) GetAttr(ctx context.Context, req *pb.GetAttrRequest) (*pb.GetAttrResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordRead(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("GetAttr: FID=%v", req.Fid)

	if req.Fid == nil {
		log.Printf("GetAttr: error - FID is required")
		opErr = errors.New("FID is required")
		return &pb.GetAttrResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	inode, _, authErr := h.fileServer.Authorize(ctx, fid, PermRead)
	if authErr != nil {
		log.Printf("GetAttr: auth error - %v", authErr)
		opErr = authErr
		return &pb.GetAttrResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	log.Printf("GetAttr: success for file %s", inode.Name)
	return &pb.GetAttrResponse{
		Success: true,
		Name:    inode.Name,
		Type:    inode.Type.ToProto(),
		Size:    inode.Size,
	}, nil
}

// Share another user the root dir only if current user is owner
func (h *GRPCHandler) Share(ctx context.Context, req *pb.ShareRequest) (*pb.ShareResponse, error) {
	if req.Username == "" || req.Fid == nil || req.ShareWith == "" {
		log.Printf("Share: error - username, user_root and share with username are required")
		return &pb.ShareResponse{
			Success: false,
			Error:   "username, user_root and share with username are required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	_, caller, authErr := h.fileServer.Authorize(ctx, fid, PermDelete)
	if authErr != nil {
		log.Printf("Share: auth error - %v", authErr)
		return &pb.ShareResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	effectiveUser := req.Username
	if caller != "" {
		if req.Username != "" && !strings.EqualFold(caller, req.Username) {
			return &pb.ShareResponse{
				Success: false,
				Error:   "permission denied: cannot share on behalf of another user",
			}, nil
		}
		effectiveUser = caller
	}

	err := h.fileServer.Share(effectiveUser, req.ShareWith, fid)
	if err != nil {
		log.Printf("Share: error sharing - %v", err)
		return &pb.ShareResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.ShareResponse{
		Success: true,
	}, nil
}

// Unshare another user the root dir only if current user is owner
func (h *GRPCHandler) Unshare(ctx context.Context, req *pb.UnshareRequest) (*pb.UnshareResponse, error) {
	if req.Username == "" || req.Fid == nil || req.UnshareWith == "" {
		log.Printf("Unshare: error - username, user_root and share with username are required")
		return &pb.UnshareResponse{
			Success: false,
			Error:   "username, user_root and share with username are required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	_, caller, authErr := h.fileServer.Authorize(ctx, fid, PermDelete)
	if authErr != nil {
		log.Printf("Unshare: auth error - %v", authErr)
		return &pb.UnshareResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	effectiveUser := req.Username
	if caller != "" {
		if req.Username != "" && !strings.EqualFold(caller, req.Username) {
			return &pb.UnshareResponse{
				Success: false,
				Error:   "permission denied: cannot unshare on behalf of another user",
			}, nil
		}
		effectiveUser = caller
	}

	err := h.fileServer.Unshare(effectiveUser, req.UnshareWith, fid)
	if err != nil {
		log.Printf("Unshare: error sharing - %v", err)
		return &pb.UnshareResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.UnshareResponse{
		Success: true,
	}, nil
}

// Returns current path
func (h *GRPCHandler) Path(ctx context.Context, req *pb.PathRequest) (*pb.PathResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordRead(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	if req.Fid == nil {
		log.Printf("Path: error - FID is required")
		opErr = errors.New("FID is required")
		return &pb.PathResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	path, err := h.fileServer.Path(fid)
	if err != nil {
		log.Printf("Path: error getting pwd - %v", err)
		opErr = err
		return &pb.PathResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	log.Printf("Path: success - found path: %v", path)
	path, err = filepath.Rel(req.RootUser, path)
	if err != nil {
		log.Printf("Path: error - error in path computation")
	}
	path = filepath.Join(req.DisplayName, path)
	return &pb.PathResponse{
		Path:    path,
		Success: true,
	}, nil
}

// Changes the current directory
func (h *GRPCHandler) ChangeDir(ctx context.Context, req *pb.ChangeDirRequest) (*pb.ChangeDirResponse, error) {
	if req.Fid == nil {
		log.Printf("ChangeDir: error - FID is required")
		return &pb.ChangeDirResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	log.Printf("Change Dir: from FID=%v to path=%v %v", req.Fid, req.Path, req.RootFid)

	fid := domain.FIDFromProto(req.Fid)
	root_fid := domain.FIDFromProto(req.RootFid)

	if _, _, authErr := h.fileServer.Authorize(ctx, fid, PermRead); authErr != nil {
		log.Printf("ChangeDir: auth error on current fid - %v", authErr)
		return &pb.ChangeDirResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	new_fid, err := h.fileServer.ChangeDir(fid, req.Path, root_fid)
	if err != nil {
		log.Printf("ChangeDir: error changing directory - %v", err)
		return &pb.ChangeDirResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	if _, _, authErr := h.fileServer.Authorize(ctx, new_fid, PermRead); authErr != nil {
		log.Printf("ChangeDir: auth error on new fid - %v", authErr)
		return &pb.ChangeDirResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	h.fileServer.TouchClientActivityByRootFID(root_fid)
	h.fileServer.UpdateClientCurrentDirByRootFID(root_fid, new_fid)

	log.Printf("ChangeDir: success - changed Dir to %v", fid)
	return &pb.ChangeDirResponse{
		Success: true,
		NewFid:  new_fid.ToProto(),
	}, nil
}

// ListDir lists directory contents
func (h *GRPCHandler) ListDir(ctx context.Context, req *pb.ListDirRequest) (*pb.ListDirResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordRead(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("ListDir: FID=%v", req.Fid)

	if req.Fid == nil {
		log.Printf("ListDir: error - FID is required")
		opErr = errors.New("FID is required")
		return &pb.ListDirResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	_, _, authErr := h.fileServer.Authorize(ctx, fid, PermRead)
	if authErr != nil {
		log.Printf("ListDir: auth error - %v", authErr)
		opErr = authErr
		return &pb.ListDirResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	children, err := h.fileServer.ListDirectory(fid)
	if err != nil {
		log.Printf("ListDir: error listing directory - %v", err)
		opErr = err
		return &pb.ListDirResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Convert to protobuf format
	pbChildren := make([]*pb.DirEntry, len(children))
	for i, child := range children {
		pbChildren[i] = &pb.DirEntry{
			Fid:  child.FID.ToProto(),
			Name: child.Name,
			Type: child.Type.ToProto(),
			Size: child.Size,
		}
	}

	log.Printf("ListDir: success - found %d entries", len(children))
	return &pb.ListDirResponse{
		Success: true,
		Entries: pbChildren,
	}, nil
}

// CreateFile creates a new file or directory
func (h *GRPCHandler) CreateFile(ctx context.Context, req *pb.CreateFileRequest) (*pb.CreateFileResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordWrite(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("CreateFile: name=%s, user=%s, type=%v", req.Name, req.RootUser, req.Type)

	if req.Name == "" || req.RootUser == "" {
		log.Printf("CreateFile: error - name and user are required")
		opErr = errors.New("name and user are required")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "name and user are required",
		}, nil
	}

	err := h.fileServer.checkStorageQuotaWithAdditional(req.RootUser, req.Size) // check if user has exceeded storage quota before allowing upload
	if err != nil {
		log.Println(err)
		opErr = err
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") || req.Name == ".." || req.Name == "." || filepath.Clean(req.Name) != req.Name || filepath.IsAbs(req.Name) {
		log.Printf("CreateFile: error - nested paths are not supported")
		opErr = errors.New("nested paths are not supported")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "nested paths are not supported",
		}, nil
	}

	// get parent FID
	parentFID := domain.FIDFromProto(req.Fid)

	// Authorize caller has write permission on parent directory
	_, caller, authErr := h.fileServer.Authorize(ctx, parentFID, PermWrite)
	if authErr != nil {
		opErr = authErr
		return &pb.CreateFileResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	effectiveUser := req.RootUser
	if caller != "" {
		effectiveUser = caller
	}

	// create file
	fileType := domain.InodeTypeFromProto(req.Type)
	fid, err := h.fileServer.CreateFile(parentFID, req.Name, effectiveUser, fileType)
	if err != nil {
		log.Printf("CreateFile: error - %v", err)
		opErr = err
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	log.Printf("CreateFile: success - created %s with FID %s", req.Name, fid.String())
	if fileType == domain.InodeTypeFile {
		h.fileServer.NotifyNewFileInDir(parentFID, req.Name, "")
	}

	return &pb.CreateFileResponse{
		Success: true,
		Fid:     fid.ToProto(),
	}, nil
}

// cleanupFailedUpload removes a partially uploaded file if an upload fails mid-stream or exceeds quota.
func (h *GRPCHandler) cleanupFailedUpload(parentFID *domain.FID, name string, user string) {
	if parentFID == nil || name == "" {
		return
	}
	h.fileServer.mu.RLock()
	parentInode, pErr := h.fileServer.GetInode(parentFID)
	if pErr != nil {
		h.fileServer.mu.RUnlock()
		return
	}
	childInode, cErr := h.fileServer.GetChildInodeByName(parentInode, name)
	h.fileServer.mu.RUnlock()
	if cErr != nil || childInode == nil {
		return
	}

	log.Printf("[FILESERVER] Scrapping partially uploaded file '%s' (FID: %s) for user '%s'", name, childInode.FID.String(), user)
	_ = h.fileServer.DeleteFile(childInode.FID, user, false)
}

// UploadFile uploads the file chunk by chunk
func (h *GRPCHandler) UploadFile(stream pb.FileServer_UploadFileServer) error {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordWriteOp(time.Since(start).Seconds()*1000.0, opErr)
	}()

	var name string
	first := true
	var parentFID *domain.FID
	var ogHash string
	var chunkCount int
	var uploadUser string

	// receive in chunks
	for {
		chunkCount++
		req, err := stream.Recv()

		if err == io.EOF {
			// check new hash to check if content has changed
			newHash, err := h.fileServer.GetFileHash(parentFID, name)
			if err != nil {
				opErr = err
				return stream.SendAndClose(&pb.UploadFileResponse{
					Success: false,
					Error:   err.Error(),
				})
			}
			log.Printf("UploadFile: completed upload for file %s with new hash %s and old hash %s with %d chunks", name, newHash, ogHash, chunkCount)

			// compare with original hash byte by byte
			if newHash != ogHash {
				log.Printf("UploadFile: file %s has been modified, new hash %s is different from original hash %s", name, newHash, ogHash)
			}

			h.fileServer.NotifyFileUpdated(parentFID, name, uploadUser)

			return stream.SendAndClose(&pb.UploadFileResponse{
				Success: true,
			})
		}
		if err != nil {
			opErr = err
			if !first && name != "" && parentFID != nil {
				log.Printf("UploadFile: stream error on file %s: %v. Scrapping partial file.", name, err)
				h.cleanupFailedUpload(parentFID, name, uploadUser)
			}
			return err
		}

		if req.ParentFid == nil {
			opErr = errors.New("missing parentFid")
			return stream.SendAndClose(&pb.UploadFileResponse{
				Success: false,
				Error:   "missing parentFid",
			})
		}

		if req.User != "" {
			uploadUser = req.User
			h.fileServer.TouchClientActivity(uploadUser)
		}

		parentFID = domain.FIDFromProto(req.ParentFid)

		if first {
			name = req.Name
			// Enforce Policy Enforcement Point on destination directory
			_, caller, authErr := h.fileServer.Authorize(stream.Context(), parentFID, PermWrite)
			if authErr != nil {
				opErr = authErr
				return stream.SendAndClose(&pb.UploadFileResponse{
					Success: false,
					Error:   authErr.Error(),
				})
			}
			if caller != "" {
				uploadUser = caller
			}

			ogHash, err = h.fileServer.GetFileHash(parentFID, name) // hash of the original file before upload
			if err != nil {
				opErr = err
				return stream.SendAndClose(&pb.UploadFileResponse{
					Success: false,
					Error:   err.Error(),
				})
			}
			first = false
		}

		// Touch active session to prevent idle timeout during active streaming upload
		if sess, ok := session.FromContext(stream.Context()); ok && sess != nil {
			sess.Touch(time.Now())
		}

		err = h.fileServer.WriteFile(parentFID, name, req.Offset, req.Chunk)
		if err != nil {
			opErr = err
			log.Printf("UploadFile: write error on file %s: %v. Scrapping partial file.", name, err)
			h.cleanupFailedUpload(parentFID, name, uploadUser)
			return stream.SendAndClose(&pb.UploadFileResponse{
				Success: false,
				Error:   err.Error(),
			})
		}
		h.fileServer.AddBytesWritten(uint64(len(req.Chunk)))
	}
}

// DownloadFile downloads the file by it's name in cwd
func (h *GRPCHandler) DownloadFile(req *pb.DownloadFileRequest, stream pb.FileServer_DownloadFileServer) error {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordReadOp(time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("DownloadFile: Name=%s", req.Name)
	parentFID := domain.FIDFromProto(req.ParentFid)

	// Enforce PEP on parent directory for download
	_, _, authErr := h.fileServer.Authorize(stream.Context(), parentFID, PermRead)
	if authErr != nil {
		opErr = authErr
		return stream.Send(&pb.DownloadFileResponse{
			Success: false,
			Error:   authErr.Error(),
		})
	}

	parentInode, err := h.fileServer.GetInode(parentFID)
	if err != nil {
		opErr = err
		return err
	}

	inode, err := h.fileServer.GetChildInodeByName(parentInode, req.Name)
	if err != nil {
		log.Printf("DownloadFile: error getting child inode - %v", err)
		opErr = err
		return err
	}

	if inode.Type != domain.InodeTypeFile {
		err := fmt.Errorf("Only files can be downloaded")
		opErr = err
		return stream.Send(&pb.DownloadFileResponse{
			Success: false,
			Error:   err.Error(),
		})
	}

	path := inode.OSPath
	file, err := os.Open(path)
	if err != nil {
		opErr = err
		return stream.Send(&pb.DownloadFileResponse{
			Success: false,
			Error:   err.Error(),
		})
	}
	defer file.Close()

	buf := make([]byte, chunkSize)

	offset := uint64(0)

	for {
		n, err := file.Read(buf)

		if n > 0 {
			res := &pb.DownloadFileResponse{
				Chunk:   buf[:n],
				Offset:  offset,
				Success: true,
			}

			if err := stream.Send(res); err != nil {
				opErr = err
				return err
			}

			offset += uint64(n)
			h.fileServer.AddBytesRead(uint64(n))
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			opErr = err
			return err
		}
	}

	return nil
}

// ReadFile reads data from a file
func (h *GRPCHandler) ReadFile(ctx context.Context, req *pb.ReadFileRequest) (*pb.ReadFileResponse, error) {
	start := time.Now()
	var bytesRead uint64
	var opErr error
	defer func() {
		h.fileServer.RecordRead(bytesRead, time.Since(start).Seconds()*1000.0, opErr)
	}()

	parentFID := domain.FIDFromProto(req.ParentFid)

	log.Printf("ReadFile: Name=%s, offset=%d, length=%d", req.Name, req.Offset, req.Length)
	if req.Name == "" {
		log.Printf("ReadFile: error - Name is required")
		opErr = errors.New("Name is required")
		return &pb.ReadFileResponse{
			Success: false,
			Error:   "Name is required",
		}, nil
	}

	// Enforce PEP on parent directory for reading
	_, _, authErr := h.fileServer.Authorize(ctx, parentFID, PermRead)
	if authErr != nil {
		opErr = authErr
		return &pb.ReadFileResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	data, err := h.fileServer.ReadFile(parentFID, req.Name, req.Offset, req.Length)
	if err != nil {
		log.Printf("ReadFile: error reading file - %v", err)
		opErr = err
		return &pb.ReadFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	bytesRead = uint64(len(data))
	return &pb.ReadFileResponse{
		Success: true,
		Data:    data,
	}, nil
}

// WriteFile writes data to a file
func (h *GRPCHandler) WriteFile(ctx context.Context, req *pb.WriteFileRequest) (*pb.WriteFileResponse, error) {
	start := time.Now()
	var bytesWritten uint64
	var opErr error
	defer func() {
		h.fileServer.RecordWrite(bytesWritten, time.Since(start).Seconds()*1000.0, opErr)
	}()

	parentFID := domain.FIDFromProto(req.ParentFid)
	log.Printf("WriteFile: Name=%s, data length=%d", req.Name, len(req.Data))
	if req.Name == "" {
		log.Printf("WriteFile: error - Name is required")
		opErr = errors.New("Name is required")
		return &pb.WriteFileResponse{
			Success: false,
			Error:   "Name is required",
		}, nil
	}

	// Enforce PEP on parent directory for writing
	_, _, authErr := h.fileServer.Authorize(ctx, parentFID, PermWrite)
	if authErr != nil {
		opErr = authErr
		return &pb.WriteFileResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	err := h.fileServer.WriteFile(parentFID, req.Name, req.Offset, req.Data)
	if err != nil {
		log.Printf("WriteFile: error writing file - %v", err)
		opErr = err
		return &pb.WriteFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	bytesWritten = uint64(len(req.Data))
	h.fileServer.NotifyFileUpdated(parentFID, req.Name, "")

	return &pb.WriteFileResponse{
		Success: true,
	}, nil
}

// DeleteFile deletes a file or directory
func (h *GRPCHandler) DeleteFile(ctx context.Context, req *pb.DeleteFileRequest) (*pb.DeleteFileResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordWrite(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("DeleteFile: FID=%v, user=%s", req.Fid, req.RootUser)

	// Validate request
	if req.Fid == nil {
		log.Printf("DeleteFile: error - FID is required")
		opErr = errors.New("FID is required")
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)

	// Enforce PEP: caller must be authorized owner to delete
	_, caller, authErr := h.fileServer.Authorize(ctx, fid, PermDelete)
	if authErr != nil {
		opErr = authErr
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   authErr.Error(),
		}, nil
	}

	effectiveUser := req.RootUser
	if caller != "" {
		effectiveUser = caller
	}

	if effectiveUser == "" {
		log.Printf("DeleteFile: error - user is required")
		opErr = errors.New("user is required")
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   "user is required",
		}, nil
	}

	// Capture parent directory and name before deletion for callbacks.
	var parentFIDForNotify *domain.FID
	var deletedName string
	h.fileServer.mu.RLock()
	if inode, inodeErr := h.fileServer.GetInode(fid); inodeErr == nil {
		deletedName = inode.Name
		if inode.Parent != nil && inode.Parent.FID != nil {
			parentFIDForNotify = &domain.FID{
				FileServerID:     inode.Parent.FID.FileServerID,
				InodeID:          inode.Parent.FID.InodeID,
				GenerationNumber: inode.Parent.FID.GenerationNumber,
			}
		}
	}
	h.fileServer.mu.RUnlock()

	// Get recursive flag from request (defaults to false for safety)
	recursive := req.Recursive

	// Attempt deletion
	err := h.fileServer.DeleteFile(fid, effectiveUser, recursive)
	if err != nil {
		log.Printf("DeleteFile: error deleting file - %v", err)
		opErr = err
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	h.fileServer.NotifyFileDeletedInDir(parentFIDForNotify, deletedName, effectiveUser)

	log.Printf("DeleteFile: success for FID %s", fid.String())
	return &pb.DeleteFileResponse{
		Success: true,
	}, nil
}

// TrashFile moves a file or directory into the user's trash (soft delete)
func (h *GRPCHandler) TrashFile(ctx context.Context, req *pb.TrashFileRequest) (*pb.TrashFileResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordWrite(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("TrashFile: FID=%v, user=%s", req.Fid, req.RootUser)

	if req.Fid == nil {
		opErr = errors.New("FID is required")
		return &pb.TrashFileResponse{Success: false, Error: "FID is required"}, nil
	}

	fid := domain.FIDFromProto(req.Fid)

	// Enforce PEP: caller must be authorized owner to move to trash
	_, caller, authErr := h.fileServer.Authorize(ctx, fid, PermDelete)
	if authErr != nil {
		opErr = authErr
		return &pb.TrashFileResponse{Success: false, Error: authErr.Error()}, nil
	}

	effectiveUser := req.RootUser
	if caller != "" {
		effectiveUser = caller
	}

	if effectiveUser == "" {
		opErr = errors.New("user is required")
		return &pb.TrashFileResponse{Success: false, Error: "user is required"}, nil
	}

	trashedName, err := h.fileServer.TrashFile(fid, effectiveUser, req.Recursive)
	if err != nil {
		log.Printf("TrashFile: error - %v", err)
		opErr = err
		return &pb.TrashFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.TrashFileResponse{Success: true, TrashedName: trashedName}, nil
}

// RestoreFile restores a file or directory from trash back to its original location
func (h *GRPCHandler) RestoreFile(ctx context.Context, req *pb.RestoreFileRequest) (*pb.RestoreFileResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordWrite(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	log.Printf("RestoreFile: FID=%v, user=%s", req.Fid, req.RootUser)

	if req.Fid == nil {
		opErr = errors.New("FID is required")
		return &pb.RestoreFileResponse{Success: false, Error: "FID is required"}, nil
	}
	if req.RootUser == "" {
		opErr = errors.New("user is required")
		return &pb.RestoreFileResponse{Success: false, Error: "user is required"}, nil
	}
	if req.Username == "" {
		opErr = errors.New("username is required")
		return &pb.RestoreFileResponse{Success: false, Error: "username is required"}, nil
	}

	fid := domain.FIDFromProto(req.Fid)

	caller, ok := session.UsernameFromContext(ctx)
	if ok && caller != "" {
		if req.RootUser != "" && !strings.EqualFold(caller, req.RootUser) {
			opErr = errors.New("permission denied: cannot restore files in another user's root")
			return &pb.RestoreFileResponse{Success: false, Error: "permission denied: cannot restore files in another user's root"}, nil
		}
		if req.Username != "" && !strings.EqualFold(caller, req.Username) {
			opErr = errors.New("permission denied: username mismatch")
			return &pb.RestoreFileResponse{Success: false, Error: "permission denied: username mismatch"}, nil
		}
	}

	restoredName, err := h.fileServer.RestoreFile(fid, req.RootUser, req.Username)
	if err != nil {
		log.Printf("RestoreFile: error - %v", err)
		opErr = err
		return &pb.RestoreFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.RestoreFileResponse{Success: true, RestoredName: restoredName}, nil
}

// ShowTrash lists the current user's trash directory contents.
func (h *GRPCHandler) ShowTrash(ctx context.Context, req *pb.ShowTrashRequest) (*pb.ShowTrashResponse, error) {
	start := time.Now()
	var opErr error
	defer func() {
		h.fileServer.RecordRead(0, time.Since(start).Seconds()*1000.0, opErr)
	}()

	if req.RootUser == "" {
		opErr = errors.New("user is required")
		return &pb.ShowTrashResponse{Success: false, Error: "user is required"}, nil
	}
	if req.Username == "" {
		opErr = errors.New("username is required")
		return &pb.ShowTrashResponse{Success: false, Error: "username is required"}, nil
	}

	caller, ok := session.UsernameFromContext(ctx)
	if ok && caller != "" {
		if req.RootUser != "" && !strings.EqualFold(caller, req.RootUser) {
			opErr = errors.New("permission denied: cannot view another user's trash")
			return &pb.ShowTrashResponse{Success: false, Error: "permission denied: cannot view another user's trash"}, nil
		}
		if req.Username != "" && !strings.EqualFold(caller, req.Username) {
			opErr = errors.New("permission denied: username mismatch")
			return &pb.ShowTrashResponse{Success: false, Error: "permission denied: username mismatch"}, nil
		}
	}

	entries, err := h.fileServer.ShowTrash(req.RootUser, req.Username)
	if err != nil {
		log.Printf("ShowTrash: error - %v", err)
		opErr = err
		return &pb.ShowTrashResponse{Success: false, Error: err.Error()}, nil
	}

	pbEntries := make([]*pb.DirEntry, 0, len(entries))
	for _, entry := range entries {
		pbEntries = append(pbEntries, &pb.DirEntry{
			Fid:  entry.FID.ToProto(),
			Name: entry.Name,
			Type: entry.Type.ToProto(),
			Size: entry.Size,
		})
	}

	return &pb.ShowTrashResponse{Success: true, Entries: pbEntries}, nil
}

// SetQuota updates the storage quota for a user in bytes.
func (h *GRPCHandler) SetQuota(ctx context.Context, req *pb.SetQuotaRequest) (*pb.SetQuotaResponse, error) {
	if req == nil || req.Username == "" {
		return &pb.SetQuotaResponse{Success: false, Error: "username is required"}, nil
	}
	if req.QuotaBytes == 0 {
		return &pb.SetQuotaResponse{Success: false, Error: "quota must be greater than 0"}, nil
	}

	// Restrict SetQuota to administrative identity:
	// 1. Password-based admin authorization via ADMIN_PASSWORD_HASH or ADMIN_PASSWORD
	//    (e.g. from the Admin Web Console or authorized management scripts via metadata)
	// 2. Email-based admin authorization via DVFS_ADMIN_EMAIL for authenticated Google users
	expectedHash := strings.TrimSpace(strings.ToLower(os.Getenv("ADMIN_PASSWORD_HASH")))
	adminEmail := strings.TrimSpace(strings.ToLower(os.Getenv("DVFS_ADMIN_EMAIL")))

	adminAuthorized := false

	// Check metadata for admin password credentials
	if md, hasMD := metadata.FromIncomingContext(ctx); hasMD {
		if hashes := md.Get("x-admin-password-hash"); len(hashes) > 0 && expectedHash != "" {
			if subtle.ConstantTimeCompare([]byte(strings.ToLower(hashes[0])), []byte(expectedHash)) == 1 {
				adminAuthorized = true
			}
		}
		if passes := md.Get("x-admin-password"); len(passes) > 0 {
			pass := passes[0]
			if expectedHash != "" {
				sum := sha256.Sum256([]byte(pass))
				if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(expectedHash)) == 1 {
					adminAuthorized = true
				}
			}
		}
	}

	caller, ok := session.UsernameFromContext(ctx)
	if ok && caller != "" {
		if adminEmail != "" && strings.EqualFold(caller, adminEmail) {
			adminAuthorized = true
		}
		if !adminAuthorized {
			return &pb.SetQuotaResponse{
				Success: false,
				Error:   "permission denied: only cluster administrators can modify quotas",
			}, nil
		}
	}

	err := h.fileServer.SetUserQuota(req.Username, req.QuotaBytes)
	if err != nil {
		log.Printf("SetQuota: error - %v", err)
		return &pb.SetQuotaResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.SetQuotaResponse{Success: true}, nil
}
