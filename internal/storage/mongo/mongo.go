// Package mongo implements storage.MetaStore on MongoDB.
//
// Every write is a targeted single-document operation ($set on one field,
// $setOnInsert for allocation, a keyed delete). Nothing here re-serialises
// whole-cluster state, which is the property that removes the write
// amplification of the previous JSON snapshot file.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/storage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
)

const (
	// DefaultDatabase is the database name used when the URI does not carry one.
	DefaultDatabase = "dvfs"

	collFileServers = "fileservers"
	collUsers       = "users"
	collShares      = "shares"
	collCounters    = "counters"

	counterNumericFsID = "numeric_fs_id"

	defaultServerSelectionTimeout = 5 * time.Second
	defaultOpTimeout              = 3 * time.Second
)

// Config configures the Mongo-backed MetaStore.
type Config struct {
	// URI is the standard MongoDB connection string. For the campus replica set:
	// mongodb://user:pass@dvfs1:27017,dvfs2:27017,dvfs3:27017/dvfs?replicaSet=rs0
	URI string
	// Database overrides the database name. When empty, the database is taken
	// from the URI path (the conventional place to put it), and only then does
	// it fall back to DefaultDatabase.
	Database string
	// AppName appears in Mongo server logs and profiler output.
	AppName string
	// ServerSelectionTimeout bounds how long a call waits for a reachable
	// primary. Kept short so a replica-set election surfaces as a fast error
	// rather than a stalled RPC.
	ServerSelectionTimeout time.Duration
	// OpTimeout bounds any single operation that the caller did not already
	// bound with its own context deadline.
	OpTimeout time.Duration
}

func (c *Config) applyDefaults() {
	if c.AppName == "" {
		c.AppName = "dvfs"
	}
	if c.ServerSelectionTimeout <= 0 {
		c.ServerSelectionTimeout = defaultServerSelectionTimeout
	}
	if c.OpTimeout <= 0 {
		c.OpTimeout = defaultOpTimeout
	}
}

// Store is a MongoDB-backed storage.MetaStore.
type Store struct {
	client    *mongo.Client
	db        *mongo.Database
	opTimeout time.Duration

	fileServers *mongo.Collection
	// heartbeats writes to the same collection with w:1. Losing a heartbeat
	// write is harmless (in-memory state already has it, and liveness is
	// re-established within one interval), so it is not worth a majority ack.
	heartbeats *mongo.Collection
	users      *mongo.Collection
	shares     *mongo.Collection
	counters   *mongo.Collection
}

// Open connects to MongoDB and verifies reachability.
//
// The metaserver treats a failure here as fatal: starting with empty routing
// state would silently strand every user.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	cfg.applyDefaults()
	if cfg.URI == "" {
		return nil, errors.New("mongo: URI is required")
	}
	dbName, err := resolveDatabase(cfg)
	if err != nil {
		return nil, err
	}

	clientOpts := options.Client().
		ApplyURI(cfg.URI).
		SetAppName(cfg.AppName).
		SetServerSelectionTimeout(cfg.ServerSelectionTimeout).
		// Shares and user assignment must survive a primary failover; a lost
		// grant is a correctness bug, not a cosmetic one.
		SetWriteConcern(writeconcern.Majority())

	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ServerSelectionTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo: ping %s: %w", dbName, err)
	}

	db := client.Database(dbName)
	s := &Store{
		client:      client,
		db:          db,
		opTimeout:   cfg.OpTimeout,
		fileServers: db.Collection(collFileServers),
		heartbeats:  db.Collection(collFileServers, options.Collection().SetWriteConcern(writeconcern.W1())),
		users:       db.Collection(collUsers),
		shares:      db.Collection(collShares),
		counters:    db.Collection(collCounters),
	}
	return s, nil
}

// resolveDatabase decides which database to use.
//
// Precedence is explicit config, then the database in the URI path, then the
// default. Honouring the URI matters because putting the database there is the
// MongoDB convention, and silently ignoring it would send writes to the wrong
// place with no error.
func resolveDatabase(cfg Config) (string, error) {
	if cfg.Database != "" {
		return cfg.Database, nil
	}
	cs, err := connstring.Parse(cfg.URI)
	if err != nil {
		return "", fmt.Errorf("mongo: parse URI: %w", err)
	}
	if cs.Database != "" {
		return cs.Database, nil
	}
	return DefaultDatabase, nil
}

// DatabaseName reports the database this store is bound to.
func (s *Store) DatabaseName() string { return s.db.Name() }

// withTimeout applies the configured per-operation bound unless the caller
// already set a deadline.
func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.opTimeout)
}

// --- documents -------------------------------------------------------------

type fileServerDoc struct {
	NodeID            string    `bson:"_id"`
	NumericID         uint64    `bson:"numeric_id"`
	Address           string    `bson:"address"`
	UserCount         int       `bson:"user_count"`
	LastHeartbeatUnix int64     `bson:"last_heartbeat_unix"`
	Status            string    `bson:"status"`
	UpdatedAt         time.Time `bson:"updated_at"`
}

type userDoc struct {
	Username   string    `bson:"_id"`
	HomeNodeID string    `bson:"home_node_id"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

// shareKeyDoc is the compound _id of a share. Making the natural key the _id
// gives uniqueness for free and turns revocation into a keyed delete, which is
// what makes duplicate grants and missed unshares structurally impossible.
//
// WARNING: MongoDB compares subdocument _id values by exact binary equality,
// which includes key order. {grantee,owner,path} and {owner,grantee,path} are
// two different keys: a lookup with the fields reordered matches nothing, and
// an insert with them reordered creates a *duplicate* grant rather than being
// rejected. Verified against MongoDB 7.
//
// Every read and write therefore marshals this struct, never a bson.M (whose
// key order is unspecified) and never an inline bson.D. Do not reorder these
// fields, and do not construct a share _id any other way — silently broken
// revocation is the failure mode.
type shareKeyDoc struct {
	Grantee string `bson:"grantee"`
	Owner   string `bson:"owner"`
	Path    string `bson:"path"`
}

type shareDoc struct {
	Key         shareKeyDoc `bson:"_id"`
	DisplayName string      `bson:"display_name"`
	CreatedAt   time.Time   `bson:"created_at"`
}

type counterDoc struct {
	ID  string `bson:"_id"`
	Seq uint64 `bson:"seq"`
}

// --- MetaStore -------------------------------------------------------------

func (s *Store) LoadSnapshot(ctx context.Context) (*storage.MetaSnapshot, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	snap := &storage.MetaSnapshot{}

	fsCursor, err := s.fileServers.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongo: find fileservers: %w", err)
	}
	var fsDocs []fileServerDoc
	if err := fsCursor.All(ctx, &fsDocs); err != nil {
		return nil, fmt.Errorf("mongo: decode fileservers: %w", err)
	}
	for _, d := range fsDocs {
		snap.FileServers = append(snap.FileServers, storage.FileServerRecord{
			NodeID:            d.NodeID,
			NumericID:         d.NumericID,
			Address:           d.Address,
			UserCount:         d.UserCount,
			LastHeartbeatUnix: d.LastHeartbeatUnix,
			Status:            d.Status,
		})
	}

	userCursor, err := s.users.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongo: find users: %w", err)
	}
	var userDocs []userDoc
	if err := userCursor.All(ctx, &userDocs); err != nil {
		return nil, fmt.Errorf("mongo: decode users: %w", err)
	}
	for _, d := range userDocs {
		snap.Users = append(snap.Users, storage.UserRecord{
			Username:   d.Username,
			HomeNodeID: d.HomeNodeID,
		})
	}

	shareCursor, err := s.shares.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongo: find shares: %w", err)
	}
	var shareDocs []shareDoc
	if err := shareCursor.All(ctx, &shareDocs); err != nil {
		return nil, fmt.Errorf("mongo: decode shares: %w", err)
	}
	for _, d := range shareDocs {
		snap.Shares = append(snap.Shares, storage.ShareRecord{
			Grantee:     d.Key.Grantee,
			Owner:       d.Key.Owner,
			Path:        d.Key.Path,
			DisplayName: d.DisplayName,
		})
	}

	return snap, nil
}

func (s *Store) UpsertFileServer(ctx context.Context, rec storage.FileServerRecord) (uint64, error) {
	if rec.NodeID == "" {
		return 0, errors.New("mongo: UpsertFileServer requires a NodeID")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	mutable := bson.D{
		{Key: "address", Value: rec.Address},
		{Key: "user_count", Value: rec.UserCount},
		{Key: "last_heartbeat_unix", Value: rec.LastHeartbeatUnix},
		{Key: "status", Value: rec.Status},
		{Key: "updated_at", Value: time.Now().UTC()},
	}
	filter := bson.D{{Key: "_id", Value: rec.NodeID}}

	// Fast path: the node is already known. Update its mutable fields but keep
	// the allocated numeric identity, so an address change (new DHCP lease) is
	// an in-place update rather than a second, phantom node.
	var existing fileServerDoc
	err := s.fileServers.FindOne(ctx, filter).Decode(&existing)
	if err == nil {
		if _, err := s.fileServers.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: mutable}}); err != nil {
			return 0, fmt.Errorf("mongo: update fileserver %s: %w", rec.NodeID, err)
		}
		return existing.NumericID, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return 0, fmt.Errorf("mongo: lookup fileserver %s: %w", rec.NodeID, err)
	}

	// First sight of this node: allocate a dense numeric id for the admin UI.
	numericID, err := s.nextSeq(ctx, counterNumericFsID)
	if err != nil {
		return 0, err
	}
	update := bson.D{
		{Key: "$setOnInsert", Value: bson.D{{Key: "numeric_id", Value: numericID}}},
		{Key: "$set", Value: mutable},
	}
	if _, err := s.fileServers.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		return 0, fmt.Errorf("mongo: insert fileserver %s: %w", rec.NodeID, err)
	}

	// Re-read rather than trusting our allocation: a concurrent registration of
	// the same node may have won the insert, in which case its id is the real
	// one and ours is simply a skipped sequence value.
	if err := s.fileServers.FindOne(ctx, filter).Decode(&existing); err != nil {
		return 0, fmt.Errorf("mongo: confirm fileserver %s: %w", rec.NodeID, err)
	}
	return existing.NumericID, nil
}

// nextSeq atomically allocates the next value of a named counter. The sequence
// is 1-based on the server so that a missing document upserts cleanly; callers
// see a 0-based id, matching the legacy numbering.
func (s *Store) nextSeq(ctx context.Context, name string) (uint64, error) {
	var doc counterDoc
	err := s.counters.FindOneAndUpdate(
		ctx,
		bson.D{{Key: "_id", Value: name}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: int64(1)}}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("mongo: allocate counter %s: %w", name, err)
	}
	if doc.Seq == 0 {
		return 0, fmt.Errorf("mongo: counter %s returned zero", name)
	}
	return doc.Seq - 1, nil
}

func (s *Store) RemoveFileServer(ctx context.Context, nodeID string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if _, err := s.fileServers.DeleteOne(ctx, bson.D{{Key: "_id", Value: nodeID}}); err != nil {
		return fmt.Errorf("mongo: remove fileserver %s: %w", nodeID, err)
	}
	return nil
}

func (s *Store) RecordHeartbeat(ctx context.Context, nodeID string, at time.Time, status string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	set := bson.D{
		{Key: "last_heartbeat_unix", Value: at.Unix()},
		{Key: "updated_at", Value: time.Now().UTC()},
	}
	if status != "" {
		set = append(set, bson.E{Key: "status", Value: status})
	}

	res, err := s.heartbeats.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: nodeID}},
		bson.D{{Key: "$set", Value: set}},
	)
	if err != nil {
		return fmt.Errorf("mongo: heartbeat %s: %w", nodeID, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *Store) SetFileServerStatus(ctx context.Context, nodeID, status string) error {
	return s.setFileServerField(ctx, nodeID, "status", status)
}

func (s *Store) SetUserCount(ctx context.Context, nodeID string, count int) error {
	return s.setFileServerField(ctx, nodeID, "user_count", count)
}

func (s *Store) setFileServerField(ctx context.Context, nodeID, field string, value any) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	res, err := s.fileServers.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: nodeID}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: field, Value: value},
			{Key: "updated_at", Value: time.Now().UTC()},
		}}},
	)
	if err != nil {
		return fmt.Errorf("mongo: set %s on %s: %w", field, nodeID, err)
	}
	if res.MatchedCount == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *Store) AssignUser(ctx context.Context, username, nodeID string) error {
	if username == "" {
		return errors.New("mongo: AssignUser requires a username")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	_, err := s.users.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: username}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "home_node_id", Value: nodeID},
			{Key: "updated_at", Value: time.Now().UTC()},
		}}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo: assign user %s: %w", username, err)
	}
	return nil
}

func (s *Store) RemoveUsers(ctx context.Context, usernames []string) error {
	if len(usernames) == 0 {
		return nil
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	_, err := s.users.DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: usernames}}}})
	if err != nil {
		return fmt.Errorf("mongo: remove users: %w", err)
	}
	return nil
}

func (s *Store) AddShare(ctx context.Context, sh storage.ShareRecord) error {
	if sh.Grantee == "" || sh.Owner == "" {
		return errors.New("mongo: AddShare requires a grantee and an owner")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	key := shareKeyDoc{
		Grantee: sh.Grantee,
		Owner:   sh.Owner,
		Path:    storage.NormalizeSharePath(sh.Path),
	}
	_, err := s.shares.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: key}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "display_name", Value: sh.DisplayName}}},
			{Key: "$setOnInsert", Value: bson.D{{Key: "created_at", Value: time.Now().UTC()}}},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo: add share %s->%s %s: %w", sh.Owner, sh.Grantee, key.Path, err)
	}
	return nil
}

func (s *Store) RemoveShare(ctx context.Context, grantee, owner, path string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	key := shareKeyDoc{Grantee: grantee, Owner: owner, Path: storage.NormalizeSharePath(path)}
	if _, err := s.shares.DeleteOne(ctx, bson.D{{Key: "_id", Value: key}}); err != nil {
		return fmt.Errorf("mongo: remove share %s->%s %s: %w", owner, grantee, key.Path, err)
	}
	return nil
}

func (s *Store) RemoveSharesInvolving(ctx context.Context, username string) error {
	if username == "" {
		return nil
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	filter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "_id.grantee", Value: username}},
		bson.D{{Key: "_id.owner", Value: username}},
	}}}
	if _, err := s.shares.DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("mongo: remove shares involving %s: %w", username, err)
	}
	return nil
}

func (s *Store) RemoveSharesByOwner(ctx context.Context, owner string) error {
	if owner == "" {
		return nil
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	if _, err := s.shares.DeleteMany(ctx, bson.D{{Key: "_id.owner", Value: owner}}); err != nil {
		return fmt.Errorf("mongo: remove shares owned by %s: %w", owner, err)
	}
	return nil
}

func (s *Store) EnsureIndexes(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if _, err := s.fileServers.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			// Sparse, because a unique index treats a *missing* field as null
			// and would then reject the second document that lacks it. Every
			// document this package writes sets numeric_id, but a hand-edited
			// or externally-imported record that omits it would otherwise make
			// the next node registration fail with a duplicate-key error.
			Keys:    bson.D{{Key: "numeric_id", Value: 1}},
			Options: options.Index().SetName("numeric_id_unique").SetUnique(true).SetSparse(true),
		},
		{
			Keys:    bson.D{{Key: "address", Value: 1}},
			Options: options.Index().SetName("address"),
		},
		{
			Keys:    bson.D{{Key: "status", Value: 1}, {Key: "last_heartbeat_unix", Value: 1}},
			Options: options.Index().SetName("liveness"),
		},
	}); err != nil {
		return fmt.Errorf("mongo: create fileserver indexes: %w", err)
	}

	if _, err := s.users.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "home_node_id", Value: 1}},
			Options: options.Index().SetName("home_node"),
		},
	}); err != nil {
		return fmt.Errorf("mongo: create user indexes: %w", err)
	}

	// The (grantee, owner, path) uniqueness is already enforced by the compound
	// _id; these only accelerate the per-user and per-owner lookups used by the
	// migration and verification tooling.
	if _, err := s.shares.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "_id.grantee", Value: 1}},
			Options: options.Index().SetName("by_grantee"),
		},
		{
			Keys:    bson.D{{Key: "_id.owner", Value: 1}},
			Options: options.Index().SetName("by_owner"),
		},
	}); err != nil {
		return fmt.Errorf("mongo: create share indexes: %w", err)
	}

	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if err := s.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}
	return nil
}

func (s *Store) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// Drop deletes every collection this store owns. Test and tooling helper only.
func (s *Store) Drop(ctx context.Context) error {
	for _, c := range []*mongo.Collection{s.fileServers, s.users, s.shares, s.counters} {
		if err := c.Drop(ctx); err != nil {
			return err
		}
	}
	return nil
}
