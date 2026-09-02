package fileserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
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

	log.Printf("RegisterClient: success for user %s for the user root %s", req.Username, req.RootPath)
	return &pb.RegisterClientResponse{
		Success:     true,
		UserRootFid: rootFID.ToProto(),
	}, nil
}

// GetAttr gets file attributes
func (h *GRPCHandler) GetAttr(ctx context.Context, req *pb.GetAttrRequest) (*pb.GetAttrResponse, error) {
	log.Printf("GetAttr: FID=%v", req.Fid)

	if req.Fid == nil {
		log.Printf("GetAttr: error - FID is required")
		return &pb.GetAttrResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	inode, err := h.fileServer.GetInode(fid)
	if err != nil {
		log.Printf("GetAttr: error getting inode - %v", err)
		return &pb.GetAttrResponse{
			Success: false,
			Error:   err.Error(),
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

	err := h.fileServer.Share(req.Username, req.ShareWith, domain.FIDFromProto(req.Fid))
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

	err := h.fileServer.Unshare(req.Username, req.UnshareWith, domain.FIDFromProto(req.Fid))
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
	if req.Fid == nil {
		log.Printf("Path: error - FID is required")
		return &pb.PathResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	path, err := h.fileServer.Path(fid)
	if err != nil {
		log.Printf("Path: error getting pwd - %v", err)
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
	new_fid, err := h.fileServer.ChangeDir(fid, req.Path, root_fid)
	if err != nil {
		log.Printf("ChangeDir: error changing directory - %v", err)
		return &pb.ChangeDirResponse{
			Success: false,
			Error:   err.Error(),
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
	log.Printf("ListDir: FID=%v", req.Fid)

	if req.Fid == nil {
		log.Printf("ListDir: error - FID is required")
		return &pb.ListDirResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	children, err := h.fileServer.ListDirectory(fid)
	if err != nil {
		log.Printf("ListDir: error listing directory - %v", err)
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
	log.Printf("CreateFile: name=%s, user=%s, type=%v", req.Name, req.RootUser, req.Type)

	if req.Name == "" || req.RootUser == "" {
		log.Printf("CreateFile: error - name and user are required")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "name and user are required",
		}, nil
	}

	err := h.fileServer.checkStorageQuota(req.RootUser) // check if user has exceeded storage quota before allowing upload
	if err != nil {
		log.Println(err)
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		log.Printf("CreateFile: error - nested paths are not supported")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "nested paths are not supported",
		}, nil
	}

	// get parent FID
	parentFID := domain.FIDFromProto(req.Fid)

	fileType := domain.InodeTypeFromProto(req.Type)
	newFID, err := h.fileServer.CreateFile(parentFID, req.Name, req.RootUser, fileType)
	if err != nil {
		log.Printf("CreateFile: error creating file - %v", err)
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	log.Printf("CreateFile: success - created %s with FID %s", req.Name, newFID.String())
	if fileType == domain.InodeTypeFile {
		h.fileServer.NotifyNewFileInDir(parentFID, req.Name, "")
	}
	return &pb.CreateFileResponse{
		Success: true,
		Fid:     newFID.ToProto(),
	}, nil
}

// UploadFile uploads a file in the working directory (should exist already) with the given name and content
func (h *GRPCHandler) UploadFile(stream pb.FileServer_UploadFileServer) error {

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
			return err
		}

		if req.ParentFid == nil {
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
			ogHash, err = h.fileServer.GetFileHash(parentFID, name) // hash of the original file before upload
			if err != nil {
				return stream.SendAndClose(&pb.UploadFileResponse{
					Success: false,
					Error:   err.Error(),
				})
			}
			first = false
		}

		err = h.fileServer.WriteFile(parentFID, name, req.Offset, req.Chunk)
		if err != nil {
			return stream.SendAndClose(&pb.UploadFileResponse{
				Success: false,
				Error:   err.Error(),
			})
		}
	}
}

// DownloadFile downloads the file by it's name in cwd
func (h *GRPCHandler) DownloadFile(req *pb.DownloadFileRequest, stream pb.FileServer_DownloadFileServer) error {
	log.Printf("DownloadFile: Name=%s", req.Name)
	parentFID := domain.FIDFromProto(req.ParentFid)
	parentInode, err := h.fileServer.GetInode(parentFID)
	if err != nil {
		return err
	}

	inode, err := h.fileServer.GetChildInodeByName(parentInode, req.Name)
	if err != nil {
		log.Printf("DownloadFile: error getting child inode - %v", err)
		return err
	}

	if inode.Type != domain.InodeTypeFile {
		err := fmt.Errorf("Only files can be downloaded")
		return stream.Send(&pb.DownloadFileResponse{
			Success: false,
			Error:   err.Error(),
		})
	}

	path := inode.OSPath
	file, err := os.Open(path)
	if err != nil {
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
				return err
			}

			offset += uint64(n)
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// ReadFile reads data from a file
func (h *GRPCHandler) ReadFile(ctx context.Context, req *pb.ReadFileRequest) (*pb.ReadFileResponse, error) {
	parentFID := domain.FIDFromProto(req.ParentFid)

	log.Printf("ReadFile: Name=%s, offset=%d, length=%d", req.Name, req.Offset, req.Length)
	if req.Name == "" {
		log.Printf("ReadFile: error - Name is required")
		return &pb.ReadFileResponse{
			Success: false,
			Error:   "Name is required",
		}, nil
	}

	data, err := h.fileServer.ReadFile(parentFID, req.Name, req.Offset, req.Length)
	if err != nil {
		log.Printf("ReadFile: error reading file - %v", err)
		return &pb.ReadFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.ReadFileResponse{
		Success: true,
		Data:    data,
	}, nil
}

// WriteFile writes data to a file
func (h *GRPCHandler) WriteFile(ctx context.Context, req *pb.WriteFileRequest) (*pb.WriteFileResponse, error) {
	parentFID := domain.FIDFromProto(req.ParentFid)
	log.Printf("WriteFile: Name=%s, data length=%d", req.Name, len(req.Data))
	if req.Name == "" {
		log.Printf("WriteFile: error - Name is required")
		return &pb.WriteFileResponse{
			Success: false,
			Error:   "Name is required",
		}, nil
	}

	err := h.fileServer.WriteFile(parentFID, req.Name, req.Offset, req.Data)
	if err != nil {
		log.Printf("WriteFile: error writing file - %v", err)
		return &pb.WriteFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	h.fileServer.NotifyFileUpdated(parentFID, req.Name, "")

	return &pb.WriteFileResponse{
		Success: true,
	}, nil
}

// DeleteFile deletes a file or directory
func (h *GRPCHandler) DeleteFile(ctx context.Context, req *pb.DeleteFileRequest) (*pb.DeleteFileResponse, error) {
	log.Printf("DeleteFile: FID=%v, user=%s", req.Fid, req.RootUser)

	// Validate request
	if req.Fid == nil {
		log.Printf("DeleteFile: error - FID is required")
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	if req.RootUser == "" {
		log.Printf("DeleteFile: error - user is required")
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   "user is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)

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
	err := h.fileServer.DeleteFile(fid, req.RootUser, recursive)
	if err != nil {
		log.Printf("DeleteFile: error deleting file - %v", err)
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	h.fileServer.NotifyFileDeletedInDir(parentFIDForNotify, deletedName, req.RootUser)

	log.Printf("DeleteFile: success for FID %s", fid.String())
	return &pb.DeleteFileResponse{
		Success: true,
	}, nil
}

// TrashFile moves a file or directory into the user's trash (soft delete)
func (h *GRPCHandler) TrashFile(ctx context.Context, req *pb.TrashFileRequest) (*pb.TrashFileResponse, error) {
	log.Printf("TrashFile: FID=%v, user=%s", req.Fid, req.RootUser)

	if req.Fid == nil {
		return &pb.TrashFileResponse{Success: false, Error: "FID is required"}, nil
	}
	if req.RootUser == "" {
		return &pb.TrashFileResponse{Success: false, Error: "user is required"}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	trashedName, err := h.fileServer.TrashFile(fid, req.RootUser, req.Recursive)
	if err != nil {
		log.Printf("TrashFile: error - %v", err)
		return &pb.TrashFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.TrashFileResponse{Success: true, TrashedName: trashedName}, nil
}

// RestoreFile restores a file or directory from trash back to its original location
func (h *GRPCHandler) RestoreFile(ctx context.Context, req *pb.RestoreFileRequest) (*pb.RestoreFileResponse, error) {
	log.Printf("RestoreFile: FID=%v, user=%s", req.Fid, req.RootUser)

	if req.Fid == nil {
		return &pb.RestoreFileResponse{Success: false, Error: "FID is required"}, nil
	}
	if req.RootUser == "" {
		return &pb.RestoreFileResponse{Success: false, Error: "user is required"}, nil
	}
	if req.Username == "" {
		return &pb.RestoreFileResponse{Success: false, Error: "username is required"}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	restoredName, err := h.fileServer.RestoreFile(fid, req.RootUser, req.Username)
	if err != nil {
		log.Printf("RestoreFile: error - %v", err)
		return &pb.RestoreFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.RestoreFileResponse{Success: true, RestoredName: restoredName}, nil
}

// ShowTrash lists the current user's trash directory contents.
func (h *GRPCHandler) ShowTrash(ctx context.Context, req *pb.ShowTrashRequest) (*pb.ShowTrashResponse, error) {
	if req.RootUser == "" {
		return &pb.ShowTrashResponse{Success: false, Error: "user is required"}, nil
	}
	if req.Username == "" {
		return &pb.ShowTrashResponse{Success: false, Error: "username is required"}, nil
	}

	entries, err := h.fileServer.ShowTrash(req.RootUser, req.Username)
	if err != nil {
		log.Printf("ShowTrash: error - %v", err)
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
