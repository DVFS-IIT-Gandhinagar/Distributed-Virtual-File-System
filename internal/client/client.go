package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	cbpb "github.com/umangshikarvar/dvfs/api/callback"
	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"google.golang.org/grpc"
)

// MountEntry represents a mount point in the mount table
type MountEntry struct {
	ServerID string
	Address  string // host:port
	RootFID  *pb.FID
}

// CacheEntry represents a cached file
type CacheEntry struct {
	FID       *pb.FID
	Version   uint64
	Data      []byte
	Size      uint64
	Valid     bool
	LastFetch time.Time
	mu        sync.RWMutex
}

// FileHandle represents an open file descriptor
type FileHandle struct {
	FID      *pb.FID
	ServerID string
	Mode     string // "r" or "w"
	CacheKey string
	Offset   uint64
	mu       sync.Mutex
}

// Client represents the VFS client
type Client struct {
	cbpb.UnimplementedClientCallbackServer

	clientID       string
	username       string
	mountTable     map[string]*MountEntry // mountpoint -> server info
	serverConns    map[string]pb.FileServerClient
	cache          map[string]*CacheEntry // FID key -> cache entry
	openFiles      map[int]*FileHandle    // fd -> handle
	nextFD         int
	callbackServer *grpc.Server
	callbackPort   int
	mu             sync.RWMutex
	cacheMu        sync.RWMutex
	fdMu           sync.Mutex
}

// NewClient creates a new VFS client
func NewClient(clientID, username string, callbackPort int) *Client {
	return &Client{
		clientID:     clientID,
		username:     username,
		mountTable:   make(map[string]*MountEntry),
		serverConns:  make(map[string]pb.FileServerClient),
		cache:        make(map[string]*CacheEntry),
		openFiles:    make(map[int]*FileHandle),
		nextFD:       1,
		callbackPort: callbackPort,
	}
}

// AddMount adds a server to the mount table (hardcoded in Phase 1)
func (c *Client) AddMount(mountPoint, serverID, serverAddress string, rootFID *pb.FID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Connect to server
	conn, err := grpc.Dial(serverAddress, grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %v", err)
	}

	client := pb.NewFileServerClient(conn)
	c.serverConns[serverID] = client

	// Register for callbacks and get user root FID
	callbackAddr := fmt.Sprintf("localhost:%d", c.callbackPort)
	resp, err := client.RegisterClient(context.Background(), &pb.RegisterClientRequest{
		ClientId:        c.clientID,
		CallbackAddress: callbackAddr,
		Username:        c.username,
	})
	if err != nil {
		return fmt.Errorf("failed to register client: %v", err)
	}
	if !resp.Success {
		return fmt.Errorf("registration failed: %v", resp.Error)
	}

	// Use the user's root FID from server response
	userRootFID := resp.UserRootFid

	// Add mount entry
	c.mountTable[mountPoint] = &MountEntry{
		ServerID: serverID,
		Address:  serverAddress,
		RootFID:  userRootFID,
	}

	fmt.Printf("Mounted %s at %s (server: %s, user root: %s_%d_%d)\n",
		mountPoint, serverAddress, serverID,
		userRootFID.FileServerId, userRootFID.InodeId, userRootFID.GenerationNumber)
	return nil
}

// StartCallbackServer starts the gRPC server for callbacks
func (c *Client) StartCallbackServer() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", c.callbackPort))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	c.callbackServer = grpc.NewServer()
	cbpb.RegisterClientCallbackServer(c.callbackServer, c)

	go func() {
		fmt.Printf("Callback server listening on port %d\n", c.callbackPort)
		if err := c.callbackServer.Serve(lis); err != nil {
			fmt.Printf("Callback server error: %v\n", err)
		}
	}()

	return nil
}

// StopCallbackServer stops the callback server
func (c *Client) StopCallbackServer() {
	if c.callbackServer != nil {
		c.callbackServer.Stop()
	}
}

// Invalidate handles cache invalidation callbacks from server
func (c *Client) Invalidate(ctx context.Context, req *cbpb.InvalidateRequest) (*cbpb.InvalidateResponse, error) {
	key := c.fidToKey(req.Fid)

	c.cacheMu.Lock()
	if entry, ok := c.cache[key]; ok {
		entry.mu.Lock()
		entry.Valid = false
		entry.mu.Unlock()
		fmt.Printf("Cache invalidated for FID %s, new version: %d\n", key, req.NewVersion)
	}
	c.cacheMu.Unlock()

	return &cbpb.InvalidateResponse{Success: true}, nil
}

// fidToKey converts FID to string key
func (c *Client) fidToKey(fid *pb.FID) string {
	return fmt.Sprintf("%s_%d_%d", fid.FileServerId, fid.InodeId, fid.GenerationNumber)
}

// getServerClient returns the gRPC client for a server
func (c *Client) getServerClient(serverID string) (pb.FileServerClient, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	client, ok := c.serverConns[serverID]
	if !ok {
		return nil, errors.New("server not found in mount table")
	}
	return client, nil
}

// Create creates a new file or directory
func (c *Client) Create(path, name string, isDir bool) (*pb.FID, error) {
	// For Phase 1, we assume path is relative to a mount point
	// In real implementation, we'd parse the path and lookup the parent FID

	// Use first available server (simplified)
	var client pb.FileServerClient
	c.mu.RLock()
	for _, cli := range c.serverConns {
		client = cli
		break
	}
	c.mu.RUnlock()

	if client == nil {
		return nil, errors.New("no servers available")
	}

	fileType := pb.InodeType_FILE
	if isDir {
		fileType = pb.InodeType_DIRECTORY
	}

	resp, err := client.CreateFile(context.Background(), &pb.CreateFileRequest{
		ParentPath: path,
		Name:       name,
		User:       c.username,
		Type:       fileType,
	})

	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, errors.New(resp.Error)
	}

	return resp.Fid, nil
}

// Open opens a file and returns a file descriptor
func (c *Client) Open(fid *pb.FID, mode string) (int, error) {
	client, err := c.getServerClient(fid.FileServerId)
	if err != nil {
		return -1, err
	}

	resp, err := client.OpenFile(context.Background(), &pb.OpenFileRequest{
		Fid:      fid,
		User:     c.username,
		ClientId: c.clientID,
	})

	if err != nil {
		return -1, err
	}

	if !resp.Success {
		return -1, errors.New(resp.Error)
	}

	// Create file handle
	c.fdMu.Lock()
	fd := c.nextFD
	c.nextFD++
	c.openFiles[fd] = &FileHandle{
		FID:      fid,
		ServerID: fid.FileServerId,
		Mode:     mode,
		CacheKey: c.fidToKey(fid),
		Offset:   0,
	}
	c.fdMu.Unlock()

	// Initialize cache entry if reading
	if mode == "r" {
		c.cacheMu.Lock()
		if _, ok := c.cache[c.fidToKey(fid)]; !ok {
			c.cache[c.fidToKey(fid)] = &CacheEntry{
				FID:       fid,
				Version:   resp.Version,
				Size:      resp.FileSize,
				Valid:     false,
				LastFetch: time.Time{},
			}
		}
		c.cacheMu.Unlock()
	}

	fmt.Printf("Opened file FID %s, fd=%d, version=%d\n", c.fidToKey(fid), fd, resp.Version)
	return fd, nil
}

// Read reads data from an open file
func (c *Client) Read(fd int, length uint64) ([]byte, error) {
	c.fdMu.Lock()
	handle, ok := c.openFiles[fd]
	c.fdMu.Unlock()

	if !ok {
		return nil, errors.New("invalid file descriptor")
	}

	handle.mu.Lock()
	offset := handle.Offset
	cacheKey := handle.CacheKey
	fid := handle.FID
	handle.mu.Unlock()

	// Check cache
	c.cacheMu.RLock()
	entry, hasCache := c.cache[cacheKey]
	c.cacheMu.RUnlock()

	if hasCache {
		entry.mu.RLock()
		valid := entry.Valid
		cachedData := entry.Data
		entry.mu.RUnlock()

		if valid && cachedData != nil {
			// Serve from cache
			end := offset + length
			if end > uint64(len(cachedData)) {
				end = uint64(len(cachedData))
			}
			data := cachedData[offset:end]

			handle.mu.Lock()
			handle.Offset += uint64(len(data))
			handle.mu.Unlock()

			fmt.Printf("Read %d bytes from cache (fd=%d)\n", len(data), fd)
			return data, nil
		}
	}

	// Fetch from server
	client, err := c.getServerClient(fid.FileServerId)
	if err != nil {
		return nil, err
	}

	resp, err := client.ReadFile(context.Background(), &pb.ReadFileRequest{
		Fid:    fid,
		Offset: offset,
		Length: length,
		User:   c.username,
	})

	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, errors.New(resp.Error)
	}

	// Update offset
	handle.mu.Lock()
	handle.Offset += uint64(len(resp.Data))
	handle.mu.Unlock()

	fmt.Printf("Read %d bytes from server (fd=%d)\n", len(resp.Data), fd)
	return resp.Data, nil
}

// Write writes data to an open file
func (c *Client) Write(fd int, data []byte) (int, error) {
	c.fdMu.Lock()
	handle, ok := c.openFiles[fd]
	c.fdMu.Unlock()

	if !ok {
		return 0, errors.New("invalid file descriptor")
	}

	handle.mu.Lock()
	offset := handle.Offset
	fid := handle.FID
	cacheKey := handle.CacheKey
	handle.mu.Unlock()

	client, err := c.getServerClient(fid.FileServerId)
	if err != nil {
		return 0, err
	}

	resp, err := client.WriteFile(context.Background(), &pb.WriteFileRequest{
		Fid:      fid,
		Offset:   offset,
		Data:     data,
		User:     c.username,
		ClientId: c.clientID,
	})

	if err != nil {
		return 0, err
	}

	if !resp.Success {
		return 0, errors.New(resp.Error)
	}

	// Update offset
	handle.mu.Lock()
	handle.Offset += uint64(len(data))
	handle.mu.Unlock()

	// Invalidate local cache
	c.cacheMu.Lock()
	if entry, ok := c.cache[cacheKey]; ok {
		entry.mu.Lock()
		entry.Valid = false
		entry.Version = resp.Version
		entry.mu.Unlock()
	}
	c.cacheMu.Unlock()

	fmt.Printf("Wrote %d bytes (fd=%d), new version=%d\n", len(data), fd, resp.Version)
	return len(data), nil
}

// Close closes an open file
func (c *Client) Close(fd int) error {
	c.fdMu.Lock()
	handle, ok := c.openFiles[fd]
	if !ok {
		c.fdMu.Unlock()
		return errors.New("invalid file descriptor")
	}
	delete(c.openFiles, fd)
	c.fdMu.Unlock()

	client, err := c.getServerClient(handle.ServerID)
	if err != nil {
		return err
	}

	resp, err := client.CloseFile(context.Background(), &pb.CloseFileRequest{
		Fid:      handle.FID,
		User:     c.username,
		ClientId: c.clientID,
	})

	if err != nil {
		return err
	}

	if !resp.Success {
		return errors.New(resp.Error)
	}

	fmt.Printf("Closed fd=%d\n", fd)
	return nil
}

// ReadFull reads entire file into cache (AFS-style on open)
func (c *Client) ReadFull(fd int) error {
	c.fdMu.Lock()
	handle, ok := c.openFiles[fd]
	c.fdMu.Unlock()

	if !ok {
		return errors.New("invalid file descriptor")
	}

	handle.mu.Lock()
	fid := handle.FID
	cacheKey := handle.CacheKey
	handle.mu.Unlock()

	// Get file size
	client, err := c.getServerClient(fid.FileServerId)
	if err != nil {
		return err
	}

	attrResp, err := client.GetAttr(context.Background(), &pb.GetAttrRequest{
		Fid:  fid,
		User: c.username,
	})

	if err != nil || !attrResp.Success {
		return fmt.Errorf("failed to get file size: %v", err)
	}

	// Read entire file
	resp, err := client.ReadFile(context.Background(), &pb.ReadFileRequest{
		Fid:    fid,
		Offset: 0,
		Length: attrResp.Size,
		User:   c.username,
	})

	if err != nil {
		return err
	}

	if !resp.Success {
		return errors.New(resp.Error)
	}

	// Cache the data
	c.cacheMu.Lock()
	if entry, ok := c.cache[cacheKey]; ok {
		entry.mu.Lock()
		entry.Data = resp.Data
		entry.Valid = true
		entry.Size = attrResp.Size
		entry.Version = attrResp.Version
		entry.LastFetch = time.Now()
		entry.mu.Unlock()
	} else {
		c.cache[cacheKey] = &CacheEntry{
			FID:       fid,
			Version:   attrResp.Version,
			Data:      resp.Data,
			Size:      attrResp.Size,
			Valid:     true,
			LastFetch: time.Now(),
		}
	}
	c.cacheMu.Unlock()

	fmt.Printf("Cached full file (%d bytes) for fd=%d\n", len(resp.Data), fd)
	return nil
}

// Seek changes the file offset
func (c *Client) Seek(fd int, offset uint64, whence int) (uint64, error) {
	c.fdMu.Lock()
	handle, ok := c.openFiles[fd]
	c.fdMu.Unlock()

	if !ok {
		return 0, errors.New("invalid file descriptor")
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	switch whence {
	case io.SeekStart:
		handle.Offset = offset
	case io.SeekCurrent:
		handle.Offset += offset
	case io.SeekEnd:
		// Would need to query file size
		return 0, errors.New("SeekEnd not implemented")
	}

	return handle.Offset, nil
}

// Lookup looks up a file in a directory
func (c *Client) Lookup(parentFID *pb.FID, name string) (*pb.FID, error) {
	client, err := c.getServerClient(parentFID.FileServerId)
	if err != nil {
		return nil, err
	}

	resp, err := client.Lookup(context.Background(), &pb.LookupRequest{
		ParentFid: parentFID,
		Name:      name,
		User:      c.username,
	})

	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, errors.New(resp.Error)
	}

	return resp.Fid, nil
}

// ListDir lists directory contents
func (c *Client) ListDir(dirFID *pb.FID) ([]*pb.DirEntry, error) {
	client, err := c.getServerClient(dirFID.FileServerId)
	if err != nil {
		return nil, err
	}

	resp, err := client.ListDir(context.Background(), &pb.ListDirRequest{
		Fid:  dirFID,
		User: c.username,
	})

	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, errors.New(resp.Error)
	}

	return resp.Entries, nil
}
