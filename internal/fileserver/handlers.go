package fileserver

import (
	"context"

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
	if req.Username == "" {
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   "username is required",
		}, nil
	}

	rootFID, err := h.fileServer.GetUserRoot(req.Username)
	if err != nil {
		return &pb.RegisterClientResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.RegisterClientResponse{
		Success: true,
		UserRootFid: rootFID.ToProto(),
	}, nil
}

// GetAttr gets file attributes
func (h *GRPCHandler) GetAttr(ctx context.Context, req *pb.GetAttrRequest) (*pb.GetAttrResponse, error) {
	if req.Fid == nil {
		return &pb.GetAttrResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	inode, err := h.fileServer.GetInode(fid)
	if err != nil {
		return &pb.GetAttrResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.GetAttrResponse{
		Success: true,
		Name:    inode.Name,
		Type:    inode.Type.ToProto(),
		Size:    inode.Size,
	}, nil
}

// ListDir lists directory contents
func (h *GRPCHandler) ListDir(ctx context.Context, req *pb.ListDirRequest) (*pb.ListDirResponse, error) {
	if req.Fid == nil {
		return &pb.ListDirResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	children, err := h.fileServer.ListDirectory(fid)
	if err != nil {
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

	return &pb.ListDirResponse{
		Success: true,
		Entries: pbChildren,
	}, nil
}

// CreateFile creates a new file or directory
func (h *GRPCHandler) CreateFile(ctx context.Context, req *pb.CreateFileRequest) (*pb.CreateFileResponse, error) {
	if req.Name == "" || req.User == "" {
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "name and user are required",
		}, nil
	}

	// For simplicity, assume parent is user root if not specified
	// In real implementation, you'd parse parent_path to get parent FID
	parentFID, err := h.fileServer.GetUserRoot(req.User)
	if err != nil {
		return &pb.CreateFileResponse{
			Success: false,
			Error:   "failed to get user root: " + err.Error(),
		}, nil
	}

	fileType := domain.InodeTypeFromProto(req.Type)
	newFID, err := h.fileServer.CreateFile(parentFID, req.Name, req.User, fileType)
	if err != nil {
		return &pb.CreateFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.CreateFileResponse{
		Success: true,
		Fid:     newFID.ToProto(),
	}, nil
}

// OpenFile handles file opening (simplified - just returns success)
func (h *GRPCHandler) OpenFile(ctx context.Context, req *pb.OpenFileRequest) (*pb.OpenFileResponse, error) {
	if req.Fid == nil {
		return &pb.OpenFileResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	_, err := h.fileServer.GetInode(fid)
	if err != nil {
		return &pb.OpenFileResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return &pb.OpenFileResponse{
		Success: true,
	}, nil
}