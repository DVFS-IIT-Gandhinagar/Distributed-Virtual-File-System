package domain

import (
	"fmt"

	pb "github.com/umangshikarvar/dvfs/api/fileserver"
)

// FID represents a File Identifier
type FID struct {
	FileServerID     string
	InodeID          uint64
	GenerationNumber uint64
}

// String returns string representation of FID
func (f *FID) String() string {
	return fmt.Sprintf("%s_%d_%d", f.FileServerID, f.InodeID, f.GenerationNumber)
}

// ToProto converts domain FID to protobuf FID
func (f *FID) ToProto() *pb.FID {
	return &pb.FID{
		FileServerId:     f.FileServerID,
		InodeId:          f.InodeID,
		GenerationNumber: f.GenerationNumber,
	}
}

// FIDFromProto creates domain FID from protobuf FID
func FIDFromProto(pbFID *pb.FID) *FID {
	if pbFID == nil {
		return nil
	}
	return &FID{
		FileServerID:     pbFID.FileServerId,
		InodeID:          pbFID.InodeId,
		GenerationNumber: pbFID.GenerationNumber,
	}
}

// InodeType represents file or directory
type InodeType int

const (
	InodeTypeFile InodeType = iota
	InodeTypeDirectory
)

// String returns string representation
func (t InodeType) String() string {
	switch t {
	case InodeTypeFile:
		return "file"
	case InodeTypeDirectory:
		return "directory"
	default:
		return "unknown"
	}
}

// ToProto converts to protobuf type
func (t InodeType) ToProto() pb.InodeType {
	switch t {
	case InodeTypeFile:
		return pb.InodeType_FILE
	case InodeTypeDirectory:
		return pb.InodeType_DIRECTORY
	default:
		return pb.InodeType_FILE
	}
}

// InodeTypeFromProto converts from protobuf type
func InodeTypeFromProto(pbType pb.InodeType) InodeType {
	switch pbType {
	case pb.InodeType_FILE:
		return InodeTypeFile
	case pb.InodeType_DIRECTORY:
		return InodeTypeDirectory
	default:
		return InodeTypeFile
	}
}

// Inode represents a file or directory
type Inode struct {
	FID      *FID
	Type     InodeType
	Name     string
	OSPath   string
	ACL      ACL
	Children []*FID // for directories
	Size     uint64 // for files
	Parent	*Inode
}

// Access Control List
type ACL struct {
    Owner string   // user who owns this root
	Shared []string
    // Read  []string // users with read-only access
    // Write []string // users with full access
}

// func (a *ACL) CanRead(user string) bool {
//     if a.Owner == user { return true }
//     for _, u := range a.Read  { if u == user { return true } }
//     for _, u := range a.Write { if u == user { return true } } // write ⊃ read
//     return false
// }

// func (a *ACL) CanWrite(user string) bool {
//     if a.Owner == user { return true }
//     for _, u := range a.Write { if u == user { return true } }
//     return false
// }

// Represents a fileserver stores add and usercount
type FileServerInfo struct {
	Address   string
	UserCount int
}