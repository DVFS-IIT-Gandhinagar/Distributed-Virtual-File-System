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
	path, err = filepath.Rel(req.User, path)
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

	// get parent FID
	parentFID := domain.FIDFromProto(req.Fid)

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
	log.Printf("DeleteFile: FID=%v, user=%s", req.Fid, req.User)

	// Validate request
	if req.Fid == nil {
		log.Printf("DeleteFile: error - FID is required")
		return &pb.DeleteFileResponse{
			Success: false,
			Error:   "FID is required",
		}, nil
	}

	if req.User == "" {
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
	err := h.fileServer.DeleteFile(fid, req.User, recursive)
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
