package memory_test

import (
	"testing"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/memory"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage/storagetest"
)

func TestMemoryStoreConformance(t *testing.T) {
	storagetest.RunMetaStoreConformance(t, func(t *testing.T) storage.MetaStore {
		return memory.New()
	})
}
