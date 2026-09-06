package domain

import (
	"encoding/json"
	"math"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRobustness_FileServerInfo_JSON_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		info FileServerInfo
	}{
		{
			name: "AllFieldsSet",
			info: FileServerInfo{
				Address:           "127.0.0.1:8080",
				UserCount:         42,
				LastHeartbeatUnix: 1234567890,
				Status:            "healthy",
			},
		},
		{
			name: "OmitEmptyFields",
			info: FileServerInfo{
				Address:   "127.0.0.1:8081",
				UserCount: 10,
				// LastHeartbeatUnix is 0, should be omitted
				// Status is empty, should be omitted
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.info)
			require.NoError(t, err)

			var unmarshaled FileServerInfo
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err)

			assert.Equal(t, tt.info, unmarshaled)

			// Specifically check omitempty
			if tt.name == "OmitEmptyFields" {
				strData := string(data)
				assert.NotContains(t, strData, "last_heartbeat_unix")
				assert.NotContains(t, strData, "status")
			}
		})
	}
}

func TestRobustness_FID_String_ZeroValues(t *testing.T) {
	f := &FID{
		FileServerID:     "",
		InodeID:          0,
		GenerationNumber: 0,
	}
	assert.Equal(t, "_0_0", f.String())
}

func TestRobustness_FID_ToProto_RoundTrip_MaxValues(t *testing.T) {
	f := &FID{
		FileServerID:     "server_max",
		InodeID:          math.MaxUint64,
		GenerationNumber: math.MaxUint64,
	}

	protoF := f.ToProto()
	require.NotNil(t, protoF)
	assert.Equal(t, "server_max", protoF.FileServerId)
	assert.Equal(t, uint64(math.MaxUint64), protoF.InodeId)
	assert.Equal(t, uint64(math.MaxUint64), protoF.GenerationNumber)

	f2 := FIDFromProto(protoF)
	require.NotNil(t, f2)
	assert.Equal(t, f.FileServerID, f2.FileServerID)
	assert.Equal(t, f.InodeID, f2.InodeID)
	assert.Equal(t, f.GenerationNumber, f2.GenerationNumber)
}

func TestRobustness_InodeType_String(t *testing.T) {
	tests := []struct {
		name     string
		t        InodeType
		expected string
	}{
		{"File", InodeTypeFile, "file"},
		{"Directory", InodeTypeDirectory, "directory"},
		{"Unknown", InodeType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.t.String())
		})
	}
}

func TestRobustness_InodeType_ToProto_UnknownDefaultsToFile(t *testing.T) {
	typ := InodeType(99)
	protoType := typ.ToProto()
	assert.Equal(t, pb.InodeType_FILE, protoType)
}

func TestRobustness_InodeTypeFromProto_UnknownDefaultsToFile(t *testing.T) {
	protoType := pb.InodeType(99)
	typ := InodeTypeFromProto(protoType)
	assert.Equal(t, InodeTypeFile, typ)
}

func TestRobustness_ACL_StructAccess(t *testing.T) {
	acl := ACL{
		Owner:  "userA",
		Shared: []string{"userB", "userC"},
	}

	assert.Equal(t, "userA", acl.Owner)
	assert.Len(t, acl.Shared, 2)
	assert.Equal(t, "userB", acl.Shared[0])
	assert.Equal(t, "userC", acl.Shared[1])
}

func TestRobustness_Inode_Struct_NilPointers(t *testing.T) {
	inode := Inode{
		FID:      nil,
		Type:     InodeTypeFile,
		Name:     "test.txt",
		OSPath:   "/path/to/test.txt",
		ACL:      ACL{Owner: "root"},
		Children: nil,
		Size:     1024,
		Parent:   nil,
	}

	assert.Nil(t, inode.FID)
	assert.Nil(t, inode.Children)
	assert.Nil(t, inode.Parent)
	assert.Equal(t, "test.txt", inode.Name)
	assert.Equal(t, uint64(1024), inode.Size)
}

