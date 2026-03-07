package metaserver

import (
	"sync"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

type MetaServer struct {
	fileservers map[uint64]*domain.FileServerInfo // fs_id -> fs
	users       map[string]uint64 // username -> fs_id
	nextFsID    uint64
	mu          sync.RWMutex
}

// NewFileServer creates a new file server object, either blank or loading from existing data
func NewMetaServer() (*MetaServer, error) {
	ms := &MetaServer{
		fileservers: make(map[uint64]*domain.FileServerInfo),
		users:       make(map[string]uint64),
		nextFsID:    0,
	}
	
	return ms, nil
}