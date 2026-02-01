package client

import (
	"context"
	"fmt"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client provides basic VFS client functionality
type Client struct {
	username   string
	rootFID    *domain.FID
	serverConn pb.FileServerClient
}

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
	return nil
}

// ListFiles lists files in user's root directory
func (c *Client) ListFiles() ([]*FileInfo, error) {
	resp, err := c.serverConn.ListDir(context.Background(), &pb.ListDirRequest{
		Fid:  c.rootFID.ToProto(),
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
		Fid:  c.rootFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get attributes: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}

	return &FileInfo{
		FID:  c.rootFID,
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

// CreateDirectory creates a new directory
func (c *Client) CreateDirectory(name string) (*FileInfo, error) {
	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: name,
		User: c.username,
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

// FileInfo represents file information
type FileInfo struct {
	FID  *domain.FID
	Name string
	Type domain.InodeType
	Size uint64
}