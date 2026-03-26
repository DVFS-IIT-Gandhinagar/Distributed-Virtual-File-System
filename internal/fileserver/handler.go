package fileserver

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
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

// RegisterClient handles client registration and returns user root FID
func (h *GRPCHandler) RegisterClient(ctx context.Context, req *pb.RegisterClientRequest) (*pb.RegisterClientResponse, error) {
	log.Printf("RegisterClient: username=%s requesting for user root=%s", req.Username, req.RootUser)

	if req.Username == "" || req.RootUser == "" {
		log.Printf("RegisterClient: error - username and request user root are required")
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
		if !isPresent{
			log.Printf("RegisterClient: error - user root %s is not present on this fs", req.RootUser)
			return &pb.RegisterClientResponse{
				Success: false,
				Error:   fmt.Sprintf("user root %s is not present on this fs", req.RootUser),
			}, nil
		}
	}
	rootFID, err := h.fileServer.GetUserRoot(req.RootUser)
	if err != nil {
		log.Printf("RegisterClient: error getting requested root - %v", err)
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	rootInode, err := h.fileServer.GetInode(rootFID)
	if err != nil {
		log.Printf("RegisterClient: error getting root inode - %v", err)
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	h.fileServer.mu.RLock()
	defer h.fileServer.mu.RUnlock()

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
			log.Printf("RegisterClient: error - user %s is not allowed to access root %s", req.Username, req.RootUser)
			return &pb.RegisterClientResponse{
				Success: false,
				Error:   fmt.Sprintf("user %s is not allowed to access root %s", req.Username, req.RootUser),
			}, nil
		}
	}

	log.Printf("RegisterClient: success for user %s for the user root %s", req.Username, req.RootUser)
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
	if req.Username == "" || req.RootUser == "" || req.ShareWith == "" {
		log.Printf("Share: error - username, user_root and share with username are required")
		return &pb.ShareResponse{
			Success: false,
			Error:   "username, user_root and share with username are required",
		}, nil
	}

	err := h.fileServer.Share(req.Username, req.RootUser, req.ShareWith)
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
	if req.Username == "" || req.RootUser == "" || req.ShareWith == "" {
		log.Printf("Unshare: error - username, user_root and share with username are required")
		return &pb.UnshareResponse{
			Success: false,
			Error:   "username, user_root and share with username are required",
		}, nil
	}

	err := h.fileServer.Unshare(req.Username, req.RootUser, req.ShareWith)
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
	path = filepath.Join("mydrive", path)
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
	return &pb.CreateFileResponse{
		Success: true,
		Fid:     newFID.ToProto(),
	}, nil
}

// UploadFile uploads a file in the working directory
func (h *GRPCHandler) UploadFile(stream pb.FileServer_UploadFileServer) error {

	var name string
	first := true

	for {
		req, err := stream.Recv()

		if err == io.EOF {
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

		parentFID := domain.FIDFromProto(req.ParentFid)

		if first {
			name = req.Name
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

	fid := domain.FIDFromProto(req.Fid)
	restoredName, err := h.fileServer.RestoreFile(fid, req.RootUser)
	if err != nil {
		log.Printf("RestoreFile: error - %v", err)
		return &pb.RestoreFileResponse{Success: false, Error: err.Error()}, nil
	}

	return &pb.RestoreFileResponse{Success: true, RestoredName: restoredName}, nil
}
