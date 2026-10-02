// Package storage is the persistence seam for DVFS cluster metadata. The
// metaserver keeps its routing tables in memory and uses a MetaStore only as
// their durable record. Two rules bind every implementation:
//
//  1. LoadSnapshot is the only read, once at boot. Requests are then served
//     from memory, so a slow or unreachable backend never stalls a client.
//  2. Every write is a targeted, idempotent change to one record. Callers
//     write after releasing their mutex, so concurrent writes can arrive in
//     any order. A whole-state save would let a stale one overwrite a newer.
package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a targeted update names a record that is absent.
var ErrNotFound = errors.New("storage: record not found")

// FileServerRecord is the durable identity and liveness state of one storage node.
//
// NodeID is the operator-assigned identity from the fileserver's -id flag
// ("fs1"). It is the primary key: keying on Address instead lets a DHCP lease
// change register the same physical node twice.
//
// NumericID is the legacy dense identifier the admin console and its React UI
// still derive display names from. It is allocated once per NodeID and is
// stable thereafter.
type FileServerRecord struct {
	NodeID            string
	NumericID         uint64
	Address           string
	UserCount         int
	LastHeartbeatUnix int64
	Status            string
}

// UserRecord maps a user to the node hosting their home directory.
type UserRecord struct {
	Username   string
	HomeNodeID string
}

// ShareRecord is a single grant: one owner directory visible to one grantee.
// (Grantee, Owner, Path) is the unique key.
type ShareRecord struct {
	Grantee     string
	Owner       string
	Path        string
	DisplayName string
}

// MetaSnapshot is the complete metaserver state, read once at startup.
type MetaSnapshot struct {
	FileServers []FileServerRecord
	Users       []UserRecord
	Shares      []ShareRecord
}

// MetaStore persists metaserver routing state.
//
// Implementations must be safe for concurrent use. Callers invoke these methods
// with no DVFS mutex held.
type MetaStore interface {
	// LoadSnapshot reads the entire state. Called once, at boot.
	LoadSnapshot(ctx context.Context) (*MetaSnapshot, error)

	// UpsertFileServer records a node, allocating a NumericID on first sight and
	// returning the stable one thereafter.
	UpsertFileServer(ctx context.Context, rec FileServerRecord) (uint64, error)

	// RemoveFileServer deletes a node's record. Removing an absent node is not
	// an error, so an admin can retry a decommission safely.
	RemoveFileServer(ctx context.Context, nodeID string) error

	// RecordHeartbeat refreshes liveness for one node. Best-effort: the caller
	// treats failure as non-fatal because in-memory state already has the update.
	RecordHeartbeat(ctx context.Context, nodeID string, at time.Time, status string) error

	// SetFileServerStatus marks a node healthy or stale.
	SetFileServerStatus(ctx context.Context, nodeID, status string) error

	// SetAddress records a node's new address, leaving every other field alone.
	// in case of a DHCP lease change.
	SetAddress(ctx context.Context, nodeID, address string) error

	// SetUserCount records how many users a node currently hosts.
	SetUserCount(ctx context.Context, nodeID string, count int) error

	// AssignUser binds a user to their home node.
	AssignUser(ctx context.Context, username, nodeID string) error

	// RemoveUsers deletes user records. It does not touch their shares.
	RemoveUsers(ctx context.Context, usernames []string) error

	// AddShare grants access. Idempotent on (Grantee, Owner, Path).
	AddShare(ctx context.Context, s ShareRecord) error

	// RemoveShare revokes one grant. Absent grants are not an error.
	RemoveShare(ctx context.Context, grantee, owner, path string) error

	// RemoveSharesInvolving drops every grant where the user is either side.
	// Used when a user is deleted outright.
	RemoveSharesInvolving(ctx context.Context, username string) error

	// RemoveSharesByOwner drops every grant published by one owner, leaving
	// grants they merely receive intact. Used when a fileserver re-registers
	// and republishes the authoritative share set for the users it hosts.
	RemoveSharesByOwner(ctx context.Context, owner string) error

	// EnsureIndexes creates whatever indexes the backend needs. Idempotent.
	EnsureIndexes(ctx context.Context) error

	// Ping reports whether the backend is reachable.
	Ping(ctx context.Context) error

	// Close releases backend resources.
	Close(ctx context.Context) error
}

// NormalizeSharePath canonicalises a share path to the single on-the-wire form:
// forward slashes, no leading or trailing slash, no "." segments.
func NormalizeSharePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	segments := strings.Split(p, "/")
	kept := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" || s == "." {
			continue
		}
		kept = append(kept, s)
	}
	return strings.Join(kept, "/")
}
