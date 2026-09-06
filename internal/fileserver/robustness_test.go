package fileserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func TestFileserverRobustness(t *testing.T) {
	// Group tests to organize execution
	t.Run("TestInodeStore_RenamePrefix", testInodeStoreRenamePrefix)
	t.Run("TestInodeStore_LoadError", testInodeStoreLoadError)
	t.Run("TestInodeStore_NormalizePath", testInodeStoreNormalizePath)

	t.Run("TestACLStore_LoadCorrupted", testACLStoreLoadCorrupted)
	t.Run("TestACLStore_SharedNull", testACLStoreSharedNull)
	t.Run("TestACLStore_DirShares", testACLStoreDirShares)

	t.Run("TestOpMetrics", testOpMetrics)

	t.Run("TestCallbackServer", testCallbackServer)

	t.Run("TestQuota", testQuota)

	t.Run("TestFileServer", testFileServer)
}

func testInodeStoreRenamePrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := NewInodeStore(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Assign some IDs
	store.GetOrAssign("foo/bar")
	
	// 1. empty oldPrefix -> no-op ("/" normalizes to "")
	store.RenamePrefix("/", "new")
	_, ok := store.Get("foo/bar")
	if !ok {
		t.Fatalf("expected foo/bar to exist")
	}

	// 2. oldPrefix == newPrefix -> no-op
	store.RenamePrefix("foo/bar", "foo/bar")
	_, ok = store.Get("foo/bar")
	if !ok {
		t.Fatalf("expected foo/bar to exist after no-op rename")
	}

	// 3. empty newPrefix -> no-op ("/" normalizes to "")
	store.RenamePrefix("foo/bar", "/")
	_, ok = store.Get("foo/bar")
	if !ok {
		t.Fatalf("expected foo/bar to exist after no-op rename")
	}
}

func testInodeStoreLoadError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	
	// Create .dvfs_inodes_index.json as a directory so os.ReadFile fails with a non-NotExist error
	indexPath := filepath.Join(dir, inodeIndexFilename)
	err := os.Mkdir(indexPath, 0755)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = NewInodeStore(dir)
	if err == nil || !strings.Contains(err.Error(), "failed to read inode index") {
		t.Fatalf("expected error containing 'failed to read inode index', got: %v", err)
	}
}

func testInodeStoreNormalizePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input    string
		expected string
	}{
		{"./foo/bar", "foo/bar"},
		{"/foo", "foo"},
		{"foo\\bar", "foo/bar"},
		{"", "."},
		{".", "."},
		{"./", "."},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			got := normalizePath(tc.input)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func testACLStoreLoadCorrupted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fs, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create corrupted ACL JSON
	aclPath := filepath.Join(dir, "corrupted_dir")
	err = os.MkdirAll(aclPath, 0755)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = os.WriteFile(filepath.Join(aclPath, aclFileName), []byte("{corrupted json"), 0644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	acl, err := fs.LoadACL("user1", "corrupted_dir")
	if err == nil || !strings.Contains(err.Error(), "failed to parse ACL JSON") {
		t.Fatalf("expected error parsing JSON, got: %v", err)
	}
	if acl.Owner != "user1" || len(acl.Shared) != 0 {
		t.Fatalf("expected default ACL to be returned, got owner: %q, shared length: %d", acl.Owner, len(acl.Shared))
	}
}

func testACLStoreSharedNull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fs, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nullSharedPath := filepath.Join(dir, "null_shared")
	err = os.MkdirAll(nullSharedPath, 0755)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	// Write JSON with Shared: null
	err = os.WriteFile(filepath.Join(nullSharedPath, aclFileName), []byte(`{"owner":"user2","shared":null}`), 0644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	acl, err := fs.LoadACL("user2", "null_shared")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acl.Shared == nil {
		t.Fatalf("expected acl.Shared to be non-nil")
	}
	if len(acl.Shared) != 0 {
		t.Fatalf("expected acl.Shared to be empty")
	}
	if acl.Owner != "user2" {
		t.Fatalf("expected owner user2, got %s", acl.Owner)
	}
}

func testACLStoreDirShares(t *testing.T) {
	t.Parallel()
	fs := &FileServer{} // Empty rootDir

	err := fs.LoadDirShares()
	if err == nil || err.Error() != "rootDir is empty" {
		t.Fatalf("expected 'rootDir is empty' error, got %v", err)
	}

	err = fs.SaveDirShares()
	if err == nil || err.Error() != "rootDir is empty" {
		t.Fatalf("expected 'rootDir is empty' error, got %v", err)
	}

	// Corrupted dirShares
	dir := t.TempDir()
	fs2, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sharesPath := filepath.Join(dir, dirSharesFileName)
	err = os.WriteFile(sharesPath, []byte(`{invalid json`), 0644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = fs2.LoadDirShares()
	if err == nil || !strings.Contains(err.Error(), "failed to parse dirShares JSON") {
		t.Fatalf("expected parse dirShares JSON error, got %v", err)
	}
}

func testOpMetrics(t *testing.T) {
	t.Parallel()
	
	// 1 & 2. NewLatencyReservoir(0) and (-5)
	r1 := NewLatencyReservoir(0)
	if r1.size != 1024 {
		t.Fatalf("expected size 1024, got %d", r1.size)
	}
	r2 := NewLatencyReservoir(-5)
	if r2.size != 1024 {
		t.Fatalf("expected size 1024, got %d", r2.size)
	}

	om := NewOperationMetrics()

	// 3. RecordWrite with negative duration
	om.RecordWrite(100, -1, nil)
	snap := om.Snapshot()
	if snap.WriteOpsTotal != 1 || snap.BytesWrittenTotal != 100 {
		t.Fatalf("unexpected snapshot values: %+v", snap)
	}
	
	wp50, wp95, wp99 := om.writeLatency.Percentiles()
	if wp50 != 0 || wp95 != 0 || wp99 != 0 {
		t.Fatalf("expected latency percentiles to be 0")
	}

	// 4. RecordRead with negative duration
	om.RecordRead(50, -1, nil)
	snap = om.Snapshot()
	if snap.ReadOpsTotal != 1 || snap.BytesReadTotal != 50 {
		t.Fatalf("unexpected snapshot values: %+v", snap)
	}
	
	rp50, rp95, rp99 := om.readLatency.Percentiles()
	if rp50 != 0 || rp95 != 0 || rp99 != 0 {
		t.Fatalf("expected latency percentiles to be 0")
	}

	// 5. Percentiles with exactly 1 sample
	r3 := NewLatencyReservoir(10)
	r3.Add(42.0)
	p50, p95, p99 := r3.Percentiles()
	if p50 != 42.0 || p95 != 42.0 || p99 != 42.0 {
		t.Fatalf("expected 42.0 for all percentiles, got %f, %f, %f", p50, p95, p99)
	}

	// 6. Percentiles with exactly 2 samples
	r4 := NewLatencyReservoir(10)
	r4.Add(10.0)
	r4.Add(20.0)
	p50, p95, p99 = r4.Percentiles()
	if fmt.Sprintf("%.1f", p50) != "15.0" {
		t.Fatalf("expected p50 15.0, got %f", p50)
	}
	if fmt.Sprintf("%.1f", p95) != "19.5" {
		t.Fatalf("expected p95 19.5, got %f", p95)
	}
	if fmt.Sprintf("%.1f", p99) != "19.9" {
		t.Fatalf("expected p99 19.9, got %f", p99)
	}
}

func testCallbackServer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fs, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	
	// 1. isSessionActiveLocked exactly at 45s TTL boundary
	session1 := &clientSession{lastSeenAt: now.Add(-activeSessionTTL)}
	if !fs.isSessionActiveLocked(session1, now) {
		t.Fatalf("expected session to be active")
	}
	
	// 2. isSessionActiveLocked 1ms past 45s TTL
	session2 := &clientSession{lastSeenAt: now.Add(-activeSessionTTL - time.Millisecond)}
	if fs.isSessionActiveLocked(session2, now) {
		t.Fatalf("expected session to be inactive")
	}

	// 3. isSessionActiveLocked well within TTL
	session3 := &clientSession{lastSeenAt: now.Add(-10 * time.Second)}
	if !fs.isSessionActiveLocked(session3, now) {
		t.Fatalf("expected session to be active")
	}

	// 4. RemoveClientSession non-existent user
	fs.RemoveClientSession("nonexistent") // should not panic

	// 5. UpsertClientSession - create then update
	fid := &domain.FID{FileServerID: "srv", InodeID: 1}
	fs.UpsertClientSession("user1", "addr1", fid)
	
	fs.mu.Lock()
	sess, ok := fs.sessions["user1"]
	fs.mu.Unlock()
	if !ok {
		t.Fatalf("expected session for user1 to exist")
	}
	if sess.callbackAddress != "addr1" {
		t.Fatalf("expected addr1, got %s", sess.callbackAddress)
	}
	
	fs.UpsertClientSession("user1", "addr2", nil)
	fs.mu.Lock()
	sess = fs.sessions["user1"]
	fs.mu.Unlock()
	if sess.callbackAddress != "addr2" {
		t.Fatalf("expected addr2, got %s", sess.callbackAddress)
	}

	// 6. TouchClientActivity updates lastSeenAt
	oldTime := sess.lastSeenAt
	time.Sleep(10 * time.Millisecond)
	fs.TouchClientActivity("user1")
	fs.mu.Lock()
	sess = fs.sessions["user1"]
	fs.mu.Unlock()
	if !sess.lastSeenAt.After(oldTime) {
		t.Fatalf("expected lastSeenAt to be updated")
	}

	// 7. TouchClientActivity no-op for non-existent user
	fs.TouchClientActivity("nonexistent")

	// 8. recordCallbackResult - success resets failure count
	sess.consecutiveFailures = 2
	fs.recordCallbackResult("user1", true)
	fs.mu.Lock()
	sess = fs.sessions["user1"]
	fs.mu.Unlock()
	if sess.consecutiveFailures != 0 {
		t.Fatalf("expected consecutiveFailures to be 0")
	}

	// 9. recordCallbackResult - 3 consecutive failures prunes session
	fs.recordCallbackResult("user1", false)
	fs.recordCallbackResult("user1", false)
	fs.recordCallbackResult("user1", false) // 3rd failure
	
	fs.mu.Lock()
	_, ok = fs.sessions["user1"]
	fs.mu.Unlock()
	if ok {
		t.Fatalf("expected session to be pruned")
	}
}

func testQuota(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fs, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. loadQuotas with corrupted JSON
	err = os.WriteFile(filepath.Join(dir, quotaConfigFile), []byte(`{corrupted`), 0644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = fs.loadQuotas()
	if err == nil || !strings.Contains(err.Error(), "failed to parse quota config") {
		t.Fatalf("expected parse quota config error, got %v", err)
	}

	// 2. getUserQuotaLocked with zero-value quota
	fs.mu.Lock()
	fs.quotas = map[string]uint64{"user1": 0}
	q := fs.getUserQuotaLocked("user1")
	fs.mu.Unlock()
	if q != defaultStorageQuota {
		t.Fatalf("expected %d, got %d", defaultStorageQuota, q)
	}

	// 3. SetUserQuota with empty username
	err = fs.SetUserQuota("", 100)
	if err == nil || err.Error() != "username cannot be empty" {
		t.Fatalf("expected username cannot be empty error, got %v", err)
	}

	// 4. SetUserQuota with zero quotaBytes
	err = fs.SetUserQuota("user2", 0)
	if err == nil || err.Error() != "quota must be greater than 0" {
		t.Fatalf("expected quota must be greater than 0 error, got %v", err)
	}
}

func testFileServer(t *testing.T) {
	t.Parallel()
	
	// 1. NewFileServer with non-existent rootDir
	dir := filepath.Join(t.TempDir(), "nonexistent_root")
	fs, err := NewFileServer("server-1", dir, false, "ms-addr", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	// Check directory was created
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory to be created")
	}

	// 2. Session management
	fs.UpsertClientSession("user_a", "addr", nil)
	fs.UpsertClientSession("user_b", "addr", nil)
	
	fs.mu.RLock()
	sessionsCount := len(fs.sessions)
	fs.mu.RUnlock()
	if sessionsCount != 2 {
		t.Fatalf("expected 2 sessions, got %d", sessionsCount)
	}

	// 3 & 4. checkStorageQuota
	err = fs.SetUserQuota("quota_user", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create user root so checkStorageQuota doesn't fail with user not found
	_, err = fs.GetUserRoot("quota_user", "quota_user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// checkStorageQuota calculates the space used by the user's root directory.
	// Since there are no files, the used space is 0, so it should be within quota.
	err = fs.checkStorageQuota("quota_user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// To exceed quota, we need to update the root inode's size since checkStorageQuota
	// uses rootInode.Size directly instead of disk size.
	fs.mu.Lock()
	rootFID := fs.users["quota_user"]
	rootInode := fs.inodes[rootFID.String()]
	rootInode.Size = 101
	fs.mu.Unlock()

	err = fs.checkStorageQuota("quota_user")
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("expected quota exceeded error, got %v", err)
	}
}

