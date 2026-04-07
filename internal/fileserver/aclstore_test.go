package fileserver

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/umangshikarvar/dvfs/internal/domain"
)

func TestLoadACLReturnsDefaultWhenMissing(t *testing.T) {
	fs := newTestFileServer(t)

	acl, err := fs.LoadACL("alice", filepath.Join("alice", "docs"))
	if err != nil {
		t.Fatalf("LoadACL returned error for missing ACL file: %v", err)
	}

	if acl.Owner != "alice" {
		t.Fatalf("default ACL owner mismatch: got=%s want=alice", acl.Owner)
	}
	if len(acl.Shared) != 0 {
		t.Fatalf("default ACL shared list should be empty, got=%v", acl.Shared)
	}
}

func TestSaveAndLoadACLRoundTrip(t *testing.T) {
	fs := newTestFileServer(t)

	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	if _, err := fs.CreateFile(fs.users["alice"], "docs", "alice", domain.InodeTypeDirectory); err != nil {
		t.Fatalf("CreateFile docs failed: %v", err)
	}

	path := filepath.Join("alice", "docs")
	want := domain.ACL{Owner: "alice", Shared: []string{"bob", "charlie"}}

	if err := fs.SaveACL(path, want); err != nil {
		t.Fatalf("SaveACL failed: %v", err)
	}

	got, err := fs.LoadACL("alice", path)
	if err != nil {
		t.Fatalf("LoadACL failed: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ACL round-trip mismatch: got=%+v want=%+v", got, want)
	}
}

func TestSaveAndLoadDirSharesRoundTrip(t *testing.T) {
	fs := newTestFileServer(t)

	fs.Shared = map[string][]string{
		filepath.Join("alice", "docs"):    {"bob", "charlie"},
		filepath.Join("alice", "project"): {"dave"},
	}

	if err := fs.SaveDirShares(); err != nil {
		t.Fatalf("SaveDirShares failed: %v", err)
	}

	// Clear in-memory state and reload to validate persistence.
	fs.Shared = nil
	if err := fs.LoadDirShares(); err != nil {
		t.Fatalf("LoadDirShares failed: %v", err)
	}

	if len(fs.Shared) != 2 {
		t.Fatalf("dirShares size mismatch: got=%d want=2", len(fs.Shared))
	}

	if !reflect.DeepEqual(fs.Shared[filepath.Join("alice", "docs")], []string{"bob", "charlie"}) {
		t.Fatalf("docs share mismatch: got=%v", fs.Shared[filepath.Join("alice", "docs")])
	}
	if !reflect.DeepEqual(fs.Shared[filepath.Join("alice", "project")], []string{"dave"}) {
		t.Fatalf("project share mismatch: got=%v", fs.Shared[filepath.Join("alice", "project")])
	}
}
