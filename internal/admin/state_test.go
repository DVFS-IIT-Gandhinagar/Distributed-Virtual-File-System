package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMetaStateValid(t *testing.T) {
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "metaserver_state.json")

	content := `{
  "fileservers": {
    "0": {
      "address": "10.7.52.85:50052",
      "user_count": 1,
      "last_heartbeat_unix": 1788260431,
      "status": "healthy"
    }
  },
  "users": {
    "alice": 0,
    "bob": 0
  },
  "next_fs_id": 1
}`

	if err := os.WriteFile(stateFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test state file: %v", err)
	}

	state, err := LoadMetaState(stateFile)
	if err != nil {
		t.Fatalf("LoadMetaState failed: %v", err)
	}

	if len(state.FileServers) != 1 {
		t.Errorf("expected 1 fileserver, got %d", len(state.FileServers))
	}
	fs0, ok := state.FileServers["0"]
	if !ok {
		t.Fatalf("expected fileserver '0' to exist")
	}
	if fs0.Address != "10.7.52.85:50052" {
		t.Errorf("expected address '10.7.52.85:50052', got '%s'", fs0.Address)
	}
	if fs0.Status != "healthy" {
		t.Errorf("expected status 'healthy', got '%s'", fs0.Status)
	}
	if len(state.Users) != 2 {
		t.Errorf("expected 2 users, got %d", len(state.Users))
	}
	if state.Users["alice"] != 0 || state.Users["bob"] != 0 {
		t.Errorf("unexpected user mapping: %v", state.Users)
	}
	if state.NextFsID != 1 {
		t.Errorf("expected NextFsID=1, got %d", state.NextFsID)
	}
}

func TestLoadMetaStateNotFound(t *testing.T) {
	_, err := LoadMetaState("non_existent_file_xyz.json")
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}
}

func TestLoadMetaStateInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "bad.json")
	_ = os.WriteFile(stateFile, []byte("{invalid-json"), 0644)

	_, err := LoadMetaState(stateFile)
	if err == nil {
		t.Errorf("expected error for invalid json, got nil")
	}
}
