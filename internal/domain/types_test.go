package domain

import (
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
)

func TestFIDStringAndProtoRoundTrip(t *testing.T) {
	fid := &FID{FileServerID: "fs1", InodeID: 42, GenerationNumber: 7}

	if got, want := fid.String(), "fs1_42_7"; got != want {
		t.Fatalf("FID.String mismatch: got=%q want=%q", got, want)
	}

	pbFID := fid.ToProto()
	if pbFID == nil {
		t.Fatalf("ToProto returned nil")
	}

	roundTrip := FIDFromProto(pbFID)
	if roundTrip == nil {
		t.Fatalf("FIDFromProto returned nil for non-nil protobuf")
	}

	if roundTrip.FileServerID != fid.FileServerID ||
		roundTrip.InodeID != fid.InodeID ||
		roundTrip.GenerationNumber != fid.GenerationNumber {
		t.Fatalf("round-trip mismatch: got=%+v want=%+v", roundTrip, fid)
	}
}

func TestFIDFromProtoNil(t *testing.T) {
	if got := FIDFromProto(nil); got != nil {
		t.Fatalf("expected nil FID from nil protobuf, got=%+v", got)
	}
}

func TestInodeTypeConversions(t *testing.T) {
	tests := []struct {
		name      string
		inodeType InodeType
		wantStr   string
		wantProto pb.InodeType
	}{
		{name: "file", inodeType: InodeTypeFile, wantStr: "file", wantProto: pb.InodeType_FILE},
		{name: "directory", inodeType: InodeTypeDirectory, wantStr: "directory", wantProto: pb.InodeType_DIRECTORY},
		{name: "unknown defaults to file proto", inodeType: InodeType(999), wantStr: "unknown", wantProto: pb.InodeType_FILE},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.inodeType.String(); got != tt.wantStr {
				t.Fatalf("String mismatch: got=%q want=%q", got, tt.wantStr)
			}

			if got := tt.inodeType.ToProto(); got != tt.wantProto {
				t.Fatalf("ToProto mismatch: got=%v want=%v", got, tt.wantProto)
			}
		})
	}
}

func TestInodeTypeFromProto(t *testing.T) {
	tests := []struct {
		name     string
		pbType   pb.InodeType
		wantType InodeType
	}{
		{name: "file", pbType: pb.InodeType_FILE, wantType: InodeTypeFile},
		{name: "directory", pbType: pb.InodeType_DIRECTORY, wantType: InodeTypeDirectory},
		{name: "unknown defaults to file", pbType: pb.InodeType(999), wantType: InodeTypeFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InodeTypeFromProto(tt.pbType); got != tt.wantType {
				t.Fatalf("InodeTypeFromProto mismatch: got=%v want=%v", got, tt.wantType)
			}
		})
	}
}
