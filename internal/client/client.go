package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client provides basic VFS client functionality
type Client struct {
	username   string
	rootFID    *domain.FID
	currentFID *domain.FID
	serverConn pb.FileServerClient
}

const chunkSize = 1024 * 32 // 32KB

// NewClient creates a new VFS client
func NewClient(username string) *Client {
	return &Client{
		username: username,
	}
}

// Connect connects to a file server and gets user root
func (c *Client) Connect(serverAddress string) error {
	// Connect to server
	conn, err := grpc.NewClient(serverAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	c.serverConn = pb.NewFileServerClient(conn)

	// Register and get root FID
	resp, err := c.serverConn.RegisterClient(context.Background(), &pb.RegisterClientRequest{
		Username: c.username,
	})
	if err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("registration failed: %s", resp.Error)
	}

	c.rootFID = domain.FIDFromProto(resp.UserRootFid)
	c.currentFID = c.rootFID
	return nil
}

// Returns current user path
func (c *Client) Path() (string, error) {
	resp, err := c.serverConn.Path(context.Background(), &pb.PathRequest{
		Fid:  c.currentFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get path: %w", err)
	}

	if !resp.Success {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}

	return resp.Path, nil
}

// CreateDirectory changes the current directory
func (c *Client) ChangeDirectory(relative_path string) error {
	resp, err := c.serverConn.ChangeDir(context.Background(), &pb.ChangeDirRequest{
		Fid:     c.currentFID.ToProto(),
		RootFid: c.rootFID.ToProto(),
		Path:    relative_path,
	})
	if err != nil {
		return fmt.Errorf("failed to change directory: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	c.currentFID = domain.FIDFromProto(resp.NewFid)
	return nil
}

// ListFiles lists files in user's root directory
func (c *Client) ListFiles() ([]*FileInfo, error) {
	resp, err := c.serverConn.ListDir(context.Background(), &pb.ListDirRequest{
		Fid:  c.currentFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	files := make([]*FileInfo, len(resp.Entries))
	for i, child := range resp.Entries {
		files[i] = &FileInfo{
			FID:  domain.FIDFromProto(child.Fid),
			Name: child.Name,
			Type: domain.InodeTypeFromProto(child.Type),
			Size: 0, // Size not available in DirEntry, would need separate GetAttr call
		}
	}

	return files, nil
}

// GetFileInfo gets information about user's root directory
func (c *Client) GetFileInfo() (*FileInfo, error) {
	resp, err := c.serverConn.GetAttr(context.Background(), &pb.GetAttrRequest{
		Fid:  c.currentFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get attributes: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	return &FileInfo{
		FID:  c.currentFID,
		Name: resp.Name,
		Type: domain.InodeTypeFromProto(resp.Type),
		Size: resp.Size,
	}, nil
}

// CreateFile creates a new file
func (c *Client) CreateFile(name string) (*FileInfo, error) {
	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: name,
		User: c.username,
		Fid:  c.currentFID.ToProto(),
		Type: pb.InodeType_FILE,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	return &FileInfo{
		FID:  domain.FIDFromProto(resp.Fid),
		Name: name,
		Type: domain.InodeTypeFile,
		Size: 0,
	}, nil
}

// UploadeFile uploads a file
func (c *Client) UploadFile(path string) error {

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	name := filepath.Base(path)

	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: name,
		User: c.username,
		Fid:  c.currentFID.ToProto(),
		Type: pb.InodeType_FILE,
	})

	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf(resp.Error)
	}

	stream, err := c.serverConn.UploadFile(context.Background())
	if err != nil {
		return err
	}

	buf := make([]byte, chunkSize)

	offset := uint64(0)

	for {
		n, err := file.Read(buf)

		if n > 0 {
			req := &pb.UploadFileRequest{
				Chunk:  buf[:n],
				Offset: offset,
				Name: name,
				User: c.username,
				ParentFid: c.currentFID.ToProto(),
			}

			if err := stream.Send(req); err != nil {
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

	res, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}

	if !res.Success {
		return fmt.Errorf("upload failed: %s", res.Error)
	}

	return nil
}

// CreateDirectory creates a new directory
func (c *Client) CreateDirectory(name string) (*FileInfo, error) {
	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: name,
		User: c.username,
		Fid:  c.currentFID.ToProto(),
		Type: pb.InodeType_DIRECTORY,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	return &FileInfo{
		FID:  domain.FIDFromProto(resp.Fid),
		Name: name,
		Type: domain.InodeTypeDirectory,
		Size: 0,
	}, nil
}

// ReadFile reads a file
func (c *Client) ReadFile(name string) ([]byte, error) {
	resp, err := c.serverConn.ReadFile(context.Background(), &pb.ReadFileRequest{
		ParentFid: c.currentFID.ToProto(),
		Name:      name,
		Offset:    0,
		Length:    0, // 0 means read whole file
		User:      c.username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.Data, nil
}

// WriteFile writes given data to a file
func (c *Client) WriteFile(name string, data []byte) error {
	resp, err := c.serverConn.WriteFile(context.Background(), &pb.WriteFileRequest{
		ParentFid: c.currentFID.ToProto(),
		Name:      name,
		Data:      data,
		User:      c.username,
		Offset:    0,
	})
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}
	return nil
}

// FileInfo represents file information
type FileInfo struct {
	FID  *domain.FID
	Name string
	Type domain.InodeType
	Size uint64
}
