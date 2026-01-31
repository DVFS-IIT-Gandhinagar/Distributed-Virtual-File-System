package fileserver

import (
	"fmt"
	"sync"
	"sync/atomic"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

// FIDKey is a string representation of FID for map keys
type FIDKey string

// Inode represents a file or directory
type Inode struct {
	FID      *pb.FID
	Type     pb.InodeType
	Name     string
	OSPath   string
	ACL      *pb.ACL
	Children []*pb.FID // for directories
	Version  uint64    // for cache validation
	mu       sync.RWMutex
}

// fidToKey converts FID to a string key
func (fs *FileServer) fidToKey(fid *pb.FID) FIDKey {
	return FIDKey(fmt.Sprintf("%s_%d_%d", fid.FileServerId, fid.InodeId, fid.GenerationNumber))
}

// allocateFID creates a new FID
func (fs *FileServer) allocateFID() *pb.FID {
	inodeID := atomic.AddUint64(&fs.nextInodeID, 1)
	return &pb.FID{
		FileServerId:     fs.serverID,
		InodeId:          inodeID,
		GenerationNumber: 1,
	}
}

// getInode retrieves an inode by FID
func (fs *FileServer) getInode(fid *pb.FID) (*Inode, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	inode, ok := fs.inodeDB[fs.fidToKey(fid)]
	if !ok {
		return nil, fmt.Errorf("inode not found")
	}
	return inode, nil
}
