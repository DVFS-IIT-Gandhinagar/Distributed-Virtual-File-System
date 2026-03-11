package client

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client provides basic VFS client functionality
type Client struct {
	username   string
	rootFID    *domain.FID
	currentFID *domain.FID
	serverConn pb.FileServerClient
	useTLS     bool
}

const chunkSize = 1024 * 1024 * 4 // 4MB
const DownloadDir = "./Download"

// NewClient creates a new VFS client
func NewClient(username string, useTLS bool) *Client {
	return &Client{
		username: username,
		useTLS:   useTLS,
	}
}

// Connect connects to a file server and gets user root and files/dir in the root
func (c *Client) Connect(serverAddress string) (*domain.FID, error) {
	// TLS configuration
	var opts []grpc.DialOption
	if c.useTLS {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(certs.CACert) {
			return nil, fmt.Errorf("failed to append CA certificate")
		}

		// Extract host for TLS verification
		host, _, err := net.SplitHostPort(serverAddress)
		if err != nil {
			host = serverAddress // Fallback if no port specified
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	// Connect to server
	conn, err := grpc.NewClient(serverAddress, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	c.serverConn = pb.NewFileServerClient(conn)

	// Register and get root FID
	resp, err := c.serverConn.RegisterClient(context.Background(), &pb.RegisterClientRequest{
		Username: c.username,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to register: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("registration failed: %s", resp.Error)
	}

	c.rootFID = domain.FIDFromProto(resp.UserRootFid)
	c.currentFID = c.rootFID
	return c.currentFID, nil
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
func (c *Client) ChangeDirectory(relative_path string) (domain.FID, error) {
	resp, err := c.serverConn.ChangeDir(context.Background(), &pb.ChangeDirRequest{
		Fid:     c.currentFID.ToProto(),
		RootFid: c.rootFID.ToProto(),
		Path:    relative_path,
	})
	if err != nil {
		return domain.FID{}, fmt.Errorf("failed to change directory: %w", err)
	}

	if !resp.Success {
		return domain.FID{}, fmt.Errorf("server error: %s", resp.Error)
	}

	c.currentFID = domain.FIDFromProto(resp.NewFid)
	return *c.currentFID, nil
}

func (c *Client) ChangeCurrentFID(fid *domain.FID) {
	c.currentFID = fid
}

// ListFiles lists files in user's current directory
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

// Upload uploads a file or a directory recursively
func (c *Client) Upload(localPath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return c.uploadFileInternal(localPath, c.currentFID)
	}

	// It's a directory
	return c.uploadRecursive(localPath, c.currentFID)
}

func (c *Client) uploadRecursive(localPath string, parentFID *domain.FID) error {
	baseName := filepath.Base(localPath)

	// 1. Create directory on server
	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: baseName,
		User: c.username,
		Fid:  parentFID.ToProto(),
		Type: pb.InodeType_DIRECTORY,
	})
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %w", baseName, err)
	}
	if !resp.Success {
		return fmt.Errorf("server error creating directory %s: %s", baseName, resp.Error)
	}

	dirFID := domain.FIDFromProto(resp.Fid)

	// 2. Read local directory contents
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return fmt.Errorf("failed to read local directory %s: %w", localPath, err)
	}

	for _, entry := range entries {
		fullPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if err := c.uploadRecursive(fullPath, dirFID); err != nil {
				return err
			}
		} else {
			if err := c.uploadFileInternal(fullPath, dirFID); err != nil {
				return err
			}
		}
	}

	return nil
}

// uploadFileInternal uploads a single file to a specific parent directory
func (c *Client) uploadFileInternal(path string, parentFID *domain.FID) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	name := filepath.Base(path)

	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name: name,
		User: c.username,
		Fid:  parentFID.ToProto(),
		Type: pb.InodeType_FILE,
	})

	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("server error %s", resp.Error)
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
				Chunk:     buf[:n],
				Offset:    offset,
				Name:      name,
				User:      c.username,
				ParentFid: parentFID.ToProto(),
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

func (c *Client) UploadFile(path string) error {
	return c.Upload(path)
}

// GetFIDForPath returns the FID of a path relative to current or root directory
func (c *Client) GetFIDForPath(path string) (*domain.FID, error) {
	resp, err := c.serverConn.ChangeDir(context.Background(), &pb.ChangeDirRequest{
		Fid:     c.currentFID.ToProto(),
		RootFid: c.rootFID.ToProto(),
		Path:    path,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return domain.FIDFromProto(resp.NewFid), nil
}

// Download downloads a file or a directory recursively
func (c *Client) Download(path string) error {
	// Handle path components
	dirPath := filepath.Dir(path)
	baseName := filepath.Base(path)

	parentFID := c.currentFID
	if dirPath != "." && dirPath != "/" && dirPath != "\\" {
		var err error
		parentFID, err = c.GetFIDForPath(dirPath)
		if err != nil {
			return fmt.Errorf("failed to resolve path %s: %w", dirPath, err)
		}
	} else if dirPath == "/" || dirPath == "\\" {
		parentFID = c.rootFID
	}

	// get current directory contents to find 'baseName'
	resp, err := c.serverConn.ListDir(context.Background(), &pb.ListDirRequest{
		Fid:  parentFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	var target *pb.DirEntry
	for _, entry := range resp.Entries {
		if entry.Name == baseName {
			target = entry
			break
		}
	}

	if target == nil {
		return fmt.Errorf("'%s' not found", path)
	}

	return c.downloadRecursive(parentFID, target, DownloadDir)
}

func (c *Client) downloadRecursive(parentFID *domain.FID, entry *pb.DirEntry, localParentDir string) error {
	if domain.InodeTypeFromProto(entry.Type) == domain.InodeTypeFile {
		return c.downloadFileInternalAs(parentFID, entry.Name, localParentDir, entry.Name)
	}

	// It's a directory
	localDirPath := filepath.Join(localParentDir, entry.Name)
	if err := os.MkdirAll(localDirPath, 0755); err != nil {
		return fmt.Errorf("failed to create local directory %s: %w", localDirPath, err)
	}

	// List children of this directory
	childFID := domain.FIDFromProto(entry.Fid)
	resp, err := c.serverConn.ListDir(context.Background(), &pb.ListDirRequest{
		Fid:  childFID.ToProto(),
		User: c.username,
	})
	if err != nil {
		return fmt.Errorf("failed to list directory %s: %w", entry.Name, err)
	}
	if !resp.Success {
		return fmt.Errorf("server error listing %s: %s", entry.Name, resp.Error)
	}

	for _, child := range resp.Entries {
		if err := c.downloadRecursive(childFID, child, localDirPath); err != nil {
			return err
		}
	}

	return nil
}

// downloadFileInternal downloads a single file with `name` from a specific parent directory to the localDir and saves it with the name `saveAs`
func (c *Client) downloadFileInternalAs(parentFID *domain.FID, name, localDir, saveAs string) error {
	req := &pb.DownloadFileRequest{
		Name:      name,
		User:      c.username,
		ParentFid: parentFID.ToProto(),
	}

	stream, err := c.serverConn.DownloadFile(context.Background(), req)
	if err != nil {
		return err
	}

	osPath := filepath.Join(localDir, saveAs)
	// ensure directory exists
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return err
	}

	file, err := os.Create(osPath)
	if err != nil {
		return err
	}
	defer file.Close()

	for {
		res, err := stream.Recv()

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		if !res.Success {
			return fmt.Errorf("server error %s", res.Error)
		}

		_, err = file.WriteAt(res.Chunk, int64(res.Offset))
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
	}

	return nil
}

func (c *Client) DownloadFile(name string) error {
	return c.Download(name)
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
