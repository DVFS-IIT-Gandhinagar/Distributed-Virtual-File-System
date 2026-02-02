package fileserver

import (
	"context"
	"log"
	"strings"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

// GRPCHandler implements the gRPC file server interface
type GRPCHandler struct {
	pb.UnimplementedFileServerServer
	fileServer *FileServer
}

// NewGRPCHandler creates a new gRPC handler
func NewGRPCHandler(fileServer *FileServer) *GRPCHandler {
	return &GRPCHandler{
		fileServer: fileServer,
	}
}

// RegisterClient handles client registration and returns user root FID
func (h *GRPCHandler) RegisterClient(ctx context.Context, req *pb.RegisterClientRequest) (*pb.RegisterClientResponse, error) {
	log.Printf("RegisterClient: username=%s", req.Username)

	if req.Username == "" {
		log.Printf("RegisterClient: error - username is required")
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   "username is required",
		}, nil
	}

	rootFID, err := h.fileServer.GetUserRoot(req.Username)
	if err != nil {
		log.Printf("RegisterClient: error getting user root - %v", err)
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	log.Printf("RegisterClient: success for user %s", req.Username)
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

// Returns current path
func (h *GRPCHandler) Path(ctx context.Context, req *pb.PathRequest) (*pb.PathResponse, error) {
	if req.Fid == nil {
		log.Printf("ListDir: error - FID is required")
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

	return &pb.PathResponse{
		Path:    path,
		Success: true,
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
	log.Printf("CreateFile: name=%s, user=%s, type=%v", req.Name, req.User, req.Type)

	if req.Name == "" || req.User == "" {
		log.Printf("CreateFile: error - name and user are required")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "name and user are required",
		}, nil
	}

	if strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		log.Printf("CreateFile: error - nested paths are not supported")
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "nested paths are not supported",
		}, nil
	}

	// For simplicity, assume parent is user root if not specified
	// In real implementation, you'd parse parent_path to get parent FID
	parentFID, err := h.fileServer.GetUserRoot(req.User)
	if err != nil {
		log.Printf("CreateFile: error getting user root - %v", err)
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "failed to get user root: " + err.Error(),
		}, nil
	}

	fileType := domain.InodeTypeFromProto(req.Type)
	newFID, err := h.fileServer.CreateFile(parentFID, req.Name, req.User, fileType)
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

// OpenFile handles file opening (simplified - just returns success)
func (h *GRPCHandler) OpenFile(ctx context.Context, req *pb.OpenFileRequest) (*pb.OpenFileResponse, error) {
	log.Printf("OpenFile: FID=%v", req.Fid)

	if req.Fid == nil {
		log.Printf("OpenFile: error - FID is required")
		return &pb.OpenFileResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	_, err := h.fileServer.GetInode(fid)
	if err != nil {
		log.Printf("OpenFile: error getting inode - %v", err)
		return &pb.OpenFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	log.Printf("OpenFile: success")
	return &pb.OpenFileResponse{
		Success: true,
	}, nil
}
