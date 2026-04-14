package client

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"github.com/umangshikarvar/dvfs/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client provides basic VFS client functionality
type Client struct {
	username     string
	root_user    string
	root_path    string
	display_name string
	clientID     string
	callbackAddr string
	rootFID      *domain.FID
	currentFID   *domain.FID
	serverConn   pb.FileServerClient
	useTLS       bool
	cacheHandler *CacheHandler
	stopCallback func() error
}

// Shared Roots with the user
type SharedRoot struct {
	Path        string
	DisplayName string
	Owner       string
}

const chunkSize = 1024 * 1024 * 4 // 4MB
const DownloadDir = "./Download"
const trashDirName = ".trash"

func pathContainsTrashSegment(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == trashDirName {
			return true
		}
	}
	return false
}

// NewClient creates a new VFS client
func NewClient(username string, useTLS bool) *Client {
	return &Client{
		username: username,
		useTLS:   useTLS,
		clientID: fmt.Sprintf("%s-%d", username, time.Now().UnixNano()),
	}
}

// AttachCacheHandler wires cache invalidation callbacks to the active cache handler.
func (c *Client) AttachCacheHandler(cacheHandler *CacheHandler) {
	c.cacheHandler = cacheHandler
}

// Set user root
func (c *Client) SetRootUser(root_user string) {
	c.root_user = root_user
}

// Set root path
func (c *Client) SetRootPath(display_name, path string) {
	// Normalize separators so cross-platform shared root paths resolve correctly.
	c.root_path = strings.ReplaceAll(path, "\\", "/")
	c.display_name = display_name
}

// Connect connects to a file server and gets user root and files/dir in the root
func (c *Client) Connect(serverAddress string) (*domain.FID, error) {
	if c.stopCallback == nil {
		callbackAddr, stopFn, err := c.startCallbackServer()
		if err != nil {
			return nil, fmt.Errorf("failed to start callback server: %w", err)
		}
		c.callbackAddr = callbackAddr
		c.stopCallback = stopFn
	}

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
		ClientId:        c.clientID,
		CallbackAddress: c.callbackAddr,
		Username:        c.username,
		RootUser:        c.root_user,
		RootPath:        c.root_path,
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

// Share another user the root dir only if current user is owner
func (c *Client) Share(share_with string) error {
	resp, err := c.serverConn.Share(context.Background(), &pb.ShareRequest{
		Username:  c.username,
		Fid:       c.currentFID.ToProto(),
		ShareWith: share_with,
	})
	if err != nil {
		return fmt.Errorf("failed to share: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	return nil
}

// Unshare another user the root dir only if current user is owner
func (c *Client) Unshare(unshare_with string) error {
	resp, err := c.serverConn.Unshare(context.Background(), &pb.UnshareRequest{
		Username:    c.username,
		Fid:         c.currentFID.ToProto(),
		UnshareWith: unshare_with,
	})
	if err != nil {
		return fmt.Errorf("failed to share: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	return nil
}

// Returns current user path
func (c *Client) Path() (string, error) {
	resp, err := c.serverConn.Path(context.Background(), &pb.PathRequest{
		Fid:         c.currentFID.ToProto(),
		DisplayName: c.display_name,
		RootUser:    c.username,
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
	return c.ListFilesAt(c.currentFID)
}

// ListFilesAt lists files/directories for the provided directory FID.
// It does not change the client's current directory.
func (c *Client) ListFilesAt(dirFID *domain.FID) ([]*FileInfo, error) {
	if dirFID == nil {
		return nil, fmt.Errorf("invalid directory fid")
	}
	resp, err := c.serverConn.ListDir(context.Background(), &pb.ListDirRequest{
		Fid: dirFID.ToProto(),
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
			Size: child.Size,
		}
	}

	return files, nil
}

// GetFileInfo gets information about user's current directory
func (c *Client) GetFileInfo() (*FileInfo, error) {
	resp, err := c.serverConn.GetAttr(context.Background(), &pb.GetAttrRequest{
		Fid: c.currentFID.ToProto(),
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
		Name:     name,
		RootUser: c.root_user,
		Fid:      c.currentFID.ToProto(),
		Type:     pb.InodeType_FILE,
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

// Upload uploads a file or a directory recursively, returning the FID
func (c *Client) Upload(localPath string) (*domain.FID, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() { // file, will be handled by uploadFileInternal
		return c.uploadFileInternal(localPath, c.currentFID)
	}

	// It's a directory
	return c.uploadRecursive(localPath, c.currentFID)
}

func (c *Client) uploadRecursive(localPath string, parentFID *domain.FID) (*domain.FID, error) {
	baseName := filepath.Base(localPath)

	// 1. Create directory on server
	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     baseName,
		RootUser: c.root_user,
		Fid:      parentFID.ToProto(),
		Type:     pb.InodeType_DIRECTORY,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", baseName, err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("server error creating directory %s: %s", baseName, resp.Error)
	}

	dirFID := domain.FIDFromProto(resp.Fid)

	// 2. Read local directory contents
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local directory %s: %w", localPath, err)
	}

	for _, entry := range entries {
		fullPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if _, err := c.uploadRecursive(fullPath, dirFID); err != nil {
				return nil, err
			}
		} else {
			if _, err := c.uploadFileInternal(fullPath, dirFID); err != nil {
				return nil, err
			}
		}
	}

	return dirFID, nil
}

// uploadFileInternal uploads a single file to a specific parent directory and returns the new file's FID. The file will be saved with the same name as the local file.
func (c *Client) uploadFileInternal(path string, parentFID *domain.FID) (*domain.FID, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	name := filepath.Base(path)

	resp, err := c.serverConn.CreateFile(context.Background(), &pb.CreateFileRequest{
		Name:     name,
		RootUser: c.root_user,
		Fid:      parentFID.ToProto(),
		Type:     pb.InodeType_FILE,
	})

	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error %s", resp.Error)
	}

	stream, err := c.serverConn.UploadFile(context.Background())
	if err != nil {
		return nil, err
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
				return nil, err
			}
			offset += uint64(n)
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	res, err := stream.CloseAndRecv()
	if err != nil {
		return nil, err
	}

	if !res.Success {
		return nil, fmt.Errorf("upload failed: %s", res.Error)
	}

	return domain.FIDFromProto(resp.Fid), nil
}

// GetFIDForPath returns the FID of a path relative to current or root directory
func (c *Client) GetFIDForPath(path string) (*domain.FID, error) {
	if pathContainsTrashSegment(path) {
		return nil, fmt.Errorf("access denied: use show_trash to view trash contents")
	}

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
	if pathContainsTrashSegment(path) {
		return fmt.Errorf("access denied: use show_trash to view trash contents")
	}

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
		Fid: parentFID.ToProto(),
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
		Fid: childFID.ToProto(),
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
		Name:     name,
		RootUser: c.root_user,
		Fid:      c.currentFID.ToProto(),
		Type:     pb.InodeType_DIRECTORY,
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
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.Data, nil
}

// DeleteFile deletes a file or directory by name in the current directory
// If recursive is true, non-empty directories will be deleted with all contents
func (c *Client) DeleteFile(name string, recursive bool) error {
	// First, we need to get the FID of the file to delete
	// We'll list the directory and find the matching file
	files, err := c.ListFiles()
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}

	var targetFID *domain.FID
	for _, file := range files {
		if file.Name == name {
			targetFID = file.FID
			break
		}
	}

	if targetFID == nil {
		return fmt.Errorf("file or directory '%s' not found", name)
	}

	// Call delete on the server
	resp, err := c.serverConn.DeleteFile(context.Background(), &pb.DeleteFileRequest{
		Fid:       targetFID.ToProto(),
		RootUser:  c.root_user,
		Recursive: recursive,
	})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	return nil
}

// TrashFile moves a file or directory (by name in current directory) to the user's trash.
// If recursive is true, non-empty directories can be trashed.
func (c *Client) TrashFile(name string, recursive bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	files, err := c.ListFiles()
	if err != nil {
		return "", fmt.Errorf("failed to list directory: %w", err)
	}

	var targetFID *domain.FID
	for _, file := range files {
		if file.Name == name {
			targetFID = file.FID
			break
		}
	}
	if targetFID == nil {
		return "", fmt.Errorf("file or directory '%s' not found", name)
	}

	resp, err := c.serverConn.TrashFile(context.Background(), &pb.TrashFileRequest{
		Fid:       targetFID.ToProto(),
		RootUser:  c.root_user,
		Recursive: recursive,
	})
	if err != nil {
		return "", fmt.Errorf("failed to trash: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.TrashedName, nil
}

// RestoreFile restores a trashed file/directory (by name in .trash) back to its original location.
func (c *Client) RestoreFile(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	entries, err := c.ShowTrash()
	if err != nil {
		return "", fmt.Errorf("failed to list trash directory: %w", err)
	}

	var targetFID *domain.FID
	for _, entry := range entries {
		if entry.Name == name {
			targetFID = entry.FID
			break
		}
	}
	if targetFID == nil {
		return "", fmt.Errorf("'%s' not found in trash", name)
	}

	resp, err := c.serverConn.RestoreFile(context.Background(), &pb.RestoreFileRequest{
		Fid:      targetFID.ToProto(),
		RootUser: c.root_user,
	})
	if err != nil {
		return "", fmt.Errorf("failed to restore: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.RestoredName, nil
}

// ShowTrash lists the user's trash contents without navigating into .trash.
func (c *Client) ShowTrash() ([]*FileInfo, error) {
	resp, err := c.serverConn.ShowTrash(context.Background(), &pb.ShowTrashRequest{RootUser: c.root_user})
	if err != nil {
		return nil, fmt.Errorf("failed to show trash: %w", err)
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
			Size: child.Size,
		}
	}

	return files, nil
}

// ClearTrash permanently deletes every entry currently present in trash.
func (c *Client) ClearTrash() (int, error) {
	entries, err := c.ShowTrash()
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	failed := make([]string, 0)
	deleted := 0

	for _, entry := range entries {
		resp, err := c.serverConn.DeleteFile(context.Background(), &pb.DeleteFileRequest{
			Fid:       entry.FID.ToProto(),
			RootUser:  c.root_user,
			Recursive: true,
		})
		if err != nil || !resp.Success {
			failed = append(failed, entry.Name)
			continue
		}
		deleted++
	}

	if len(failed) > 0 {
		sort.Strings(failed)
		return deleted, fmt.Errorf("failed to delete trash entries: %s", strings.Join(failed, ", "))
	}

	return deleted, nil
}

// WriteFile writes given data to a file
func (c *Client) WriteFile(name string, data []byte) error {
	resp, err := c.serverConn.WriteFile(context.Background(), &pb.WriteFileRequest{
		ParentFid: c.currentFID.ToProto(),
		Name:      name,
		Data:      data,
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
