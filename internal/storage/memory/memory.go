// Package memory provides an in-process storage.MetaStore.
//
// It is the test double for the Mongo backend, in the same role MockSSHExecutor
// plays for RemoteSSHExecutor: it keeps the test suite hermetic and Mongo-free
// while running against the identical conformance suite as the real backend.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
)

// Store is a thread-safe in-memory MetaStore.
type Store struct {
	mu            sync.RWMutex
	fileServers   map[string]storage.FileServerRecord // nodeID -> record
	users         map[string]string                   // username -> nodeID
	shares        map[shareKey]storage.ShareRecord
	nextNumericID uint64
	closed        bool
}

type shareKey struct {
	grantee string
	owner   string
	path    string
}

// New returns an empty in-memory store.
func New() *Store {
	return &Store{
		fileServers: make(map[string]storage.FileServerRecord),
		users:       make(map[string]string),
		shares:      make(map[shareKey]storage.ShareRecord),
	}
}

func (s *Store) LoadSnapshot(ctx context.Context) (*storage.MetaSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := &storage.MetaSnapshot{
		FileServers: make([]storage.FileServerRecord, 0, len(s.fileServers)),
		Users:       make([]storage.UserRecord, 0, len(s.users)),
		Shares:      make([]storage.ShareRecord, 0, len(s.shares)),
	}
	for _, rec := range s.fileServers {
		snap.FileServers = append(snap.FileServers, rec)
	}
	for username, nodeID := range s.users {
		snap.Users = append(snap.Users, storage.UserRecord{Username: username, HomeNodeID: nodeID})
	}
	for _, sh := range s.shares {
		snap.Shares = append(snap.Shares, sh)
	}

	// Deterministic ordering keeps tests and logs stable.
	sort.Slice(snap.FileServers, func(i, j int) bool { return snap.FileServers[i].NodeID < snap.FileServers[j].NodeID })
	sort.Slice(snap.Users, func(i, j int) bool { return snap.Users[i].Username < snap.Users[j].Username })
	sort.Slice(snap.Shares, func(i, j int) bool {
		a, b := snap.Shares[i], snap.Shares[j]
		if a.Grantee != b.Grantee {
			return a.Grantee < b.Grantee
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.Path < b.Path
	})
	return snap, nil
}

func (s *Store) UpsertFileServer(ctx context.Context, rec storage.FileServerRecord) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.fileServers[rec.NodeID]; ok {
		// Preserve the allocated numeric identity; update everything else.
		rec.NumericID = existing.NumericID
	} else {
		rec.NumericID = s.nextNumericID
		s.nextNumericID++
	}
	s.fileServers[rec.NodeID] = rec
	return rec.NumericID, nil
}

func (s *Store) RemoveFileServer(ctx context.Context, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fileServers, nodeID)
	return nil
}

func (s *Store) RecordHeartbeat(ctx context.Context, nodeID string, at time.Time, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.fileServers[nodeID]
	if !ok {
		return storage.ErrNotFound
	}
	rec.LastHeartbeatUnix = at.Unix()
	if status != "" {
		rec.Status = status
	}
	s.fileServers[nodeID] = rec
	return nil
}

func (s *Store) SetFileServerStatus(ctx context.Context, nodeID, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.fileServers[nodeID]
	if !ok {
		return storage.ErrNotFound
	}
	rec.Status = status
	s.fileServers[nodeID] = rec
	return nil
}

func (s *Store) RenameFileServer(ctx context.Context, oldNodeID, newNodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if oldNodeID == newNodeID {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.fileServers[oldNodeID]
	if !ok {
		return storage.ErrNotFound
	}
	for username, home := range s.users {
		if home == oldNodeID {
			s.users[username] = newNodeID
		}
	}
	delete(s.fileServers, oldNodeID)
	if _, exists := s.fileServers[newNodeID]; !exists {
		rec.NodeID = newNodeID
		s.fileServers[newNodeID] = rec
	}
	return nil
}

func (s *Store) AssignUser(ctx context.Context, username, nodeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[username] = nodeID
	return nil
}

func (s *Store) AssignUsers(ctx context.Context, users []storage.UserRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range users {
		s.users[u.Username] = u.HomeNodeID
	}
	return nil
}

func (s *Store) RemoveUsers(ctx context.Context, usernames []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range usernames {
		delete(s.users, u)
	}
	return nil
}

func (s *Store) AddShare(ctx context.Context, sh storage.ShareRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sh.Path = storage.NormalizeSharePath(sh.Path)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.shares[shareKey{grantee: sh.Grantee, owner: sh.Owner, path: sh.Path}] = sh
	return nil
}

func (s *Store) AddShares(ctx context.Context, shares []storage.ShareRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sh := range shares {
		sh.Path = storage.NormalizeSharePath(sh.Path)
		s.shares[shareKey{grantee: sh.Grantee, owner: sh.Owner, path: sh.Path}] = sh
	}
	return nil
}

func (s *Store) RemoveShare(ctx context.Context, grantee, owner, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.shares, shareKey{grantee: grantee, owner: owner, path: storage.NormalizeSharePath(path)})
	return nil
}

func (s *Store) RemoveSharesInvolving(ctx context.Context, usernames ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(usernames) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(usernames))
	for _, u := range usernames {
		set[u] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.shares {
		_, g := set[k.grantee]
		_, o := set[k.owner]
		if g || o {
			delete(s.shares, k)
		}
	}
	return nil
}

func (s *Store) RemoveSharesByOwner(ctx context.Context, owners ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(owners) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(owners))
	for _, o := range owners {
		set[o] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.shares {
		if _, ok := set[k.owner]; ok {
			delete(s.shares, k)
		}
	}
	return nil
}

func (s *Store) EnsureIndexes(ctx context.Context) error { return ctx.Err() }

func (s *Store) Ping(ctx context.Context) error { return ctx.Err() }

func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
