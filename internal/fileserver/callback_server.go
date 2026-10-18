package fileserver

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	cbpb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/callback"
	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"google.golang.org/grpc"
)

type clientSession struct {
	username            string
	callbackAddress     string
	rootFID             string
	currentDirFID       string
	lastSeenAt          time.Time
	consecutiveFailures int
	// streams are client-opened WatchEvents streams (one per running client of
	// this user). When present, invalidations are pushed over them instead of
	// dialling callbackAddress, which NAT and client firewalls block.
	streams map[uint64]*eventStream
}

// eventStream is the server side of one WatchEvents stream.
type eventStream struct {
	id   uint64
	ch   chan *pb.WatchEvent
	done chan struct{}
	once sync.Once
}

func (e *eventStream) close() { e.once.Do(func() { close(e.done) }) }

// eventStreamBuffer bounds queued events per stream; a client that falls this
// far behind is disconnected and refreshes when it reconnects.
const eventStreamBuffer = 256

var nextEventStreamID atomic.Uint64

// attachEventStream registers a new event stream for username, creating the
// session if the user has none yet (e.g. it was pruned while offline).
func (fs *FileServer) attachEventStream(username string) *eventStream {
	es := &eventStream{
		id:   nextEventStreamID.Add(1),
		ch:   make(chan *pb.WatchEvent, eventStreamBuffer),
		done: make(chan struct{}),
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.sessions[username]
	if !ok {
		s = &clientSession{username: username}
		fs.sessions[username] = s
	}
	if s.streams == nil {
		s.streams = make(map[uint64]*eventStream)
	}
	s.streams[es.id] = es
	s.lastSeenAt = time.Now()
	return es
}

// detachEventStream removes a stream when its RPC ends.
func (fs *FileServer) detachEventStream(username string, es *eventStream) {
	es.close()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if s, ok := fs.sessions[username]; ok && s.streams != nil {
		delete(s.streams, es.id)
		// Treat the stream's end as last activity so the session stays a
		// notify target for one TTL while the client reconnects.
		s.lastSeenAt = time.Now()
	}
}

const activeSessionTTL = 45 * time.Second
const callbackTimeout = 3 * time.Second
const maxCallbackFailures = 3
const callbackEventFileUpdated uint64 = 1
const callbackEventDirNewFile uint64 = 2
const callbackEventFileDeleted uint64 = 3

// RemoveClientSession removes a client session when the client explicitly disconnects.
func (fs *FileServer) RemoveClientSession(username string) {
	if username == "" {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if s, ok := fs.sessions[username]; ok {
		for _, es := range s.streams {
			es.close()
		}
	}
	delete(fs.sessions, username)
}

// UpsertClientSession records callback address and activity metadata for a user.
func (fs *FileServer) UpsertClientSession(username, callbackAddress string, rootFID *domain.FID) {
	if username == "" {
		return
	}

	now := time.Now()
	fs.mu.Lock()
	defer fs.mu.Unlock()

	session, ok := fs.sessions[username]
	if !ok {
		session = &clientSession{username: username}
		fs.sessions[username] = session
	}

	if callbackAddress != "" {
		session.callbackAddress = callbackAddress
	}
	if rootFID != nil {
		session.rootFID = rootFID.String()
		session.currentDirFID = rootFID.String()
	}
	session.lastSeenAt = now
	session.consecutiveFailures = 0
}

// TouchClientActivity updates the last-seen timestamp for a user.
func (fs *FileServer) TouchClientActivity(username string) {
	if username == "" {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if session, ok := fs.sessions[username]; ok {
		session.lastSeenAt = time.Now()
	}
}

// TouchClientActivityByRootFID updates activity for sessions mapped to this root.
func (fs *FileServer) TouchClientActivityByRootFID(rootFID *domain.FID) {
	if rootFID == nil {
		return
	}

	rootFIDStr := rootFID.String()
	now := time.Now()

	fs.mu.Lock()
	defer fs.mu.Unlock()

	for _, session := range fs.sessions {
		if session.rootFID == rootFIDStr {
			session.lastSeenAt = now
		}
	}
}

// UpdateClientCurrentDirByRootFID updates current directory for sessions on this root.
func (fs *FileServer) UpdateClientCurrentDirByRootFID(rootFID, currentDirFID *domain.FID) {
	if rootFID == nil || currentDirFID == nil {
		return
	}

	rootFIDStr := rootFID.String()
	currentDirFIDStr := currentDirFID.String()
	now := time.Now()

	fs.mu.Lock()
	defer fs.mu.Unlock()

	for _, session := range fs.sessions {
		if session.rootFID == rootFIDStr {
			session.currentDirFID = currentDirFIDStr
			session.lastSeenAt = now
		}
	}
}

// UpdateClientCurrentDir updates the current directory (and activity) of a single
// user's session. Returns false when that user has no session on this server.
func (fs *FileServer) UpdateClientCurrentDir(username string, currentDirFID *domain.FID) bool {
	if username == "" || currentDirFID == nil {
		return false
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	s, ok := fs.sessions[username]
	if !ok {
		return false
	}
	s.currentDirFID = currentDirFID.String()
	s.lastSeenAt = time.Now()
	return true
}

func (fs *FileServer) isSessionActiveLocked(session *clientSession, now time.Time) bool {
	if session == nil {
		return false
	}
	if len(session.streams) > 0 {
		return true // an open stream is proof of life
	}
	if session.lastSeenAt.IsZero() {
		return false
	}
	return now.Sub(session.lastSeenAt) <= activeSessionTTL
}

func (fs *FileServer) snapshotNotifyTargetsForDirLocked(currentDirFID, originUsername string) []clientSession {
	now := time.Now()
	targets := make([]clientSession, 0)

	for _, session := range fs.sessions {
		if session == nil || (session.callbackAddress == "" && len(session.streams) == 0) {
			continue
		}
		if session.username == originUsername {
			continue
		}
		if !fs.isSessionActiveLocked(session, now) {
			continue
		}
		if session.currentDirFID != currentDirFID {
			continue
		}
		target := *session
		if len(session.streams) > 0 {
			// Copy the stream set so senders don't race with attach/detach.
			target.streams = make(map[uint64]*eventStream, len(session.streams))
			for id, es := range session.streams {
				target.streams[id] = es
			}
		}
		targets = append(targets, target)
	}

	return targets
}

func (fs *FileServer) recordCallbackResult(username string, success bool) {
	if username == "" {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	session, ok := fs.sessions[username]
	if !ok {
		return
	}

	if success {
		session.consecutiveFailures = 0
		return
	}

	session.consecutiveFailures++
	if session.consecutiveFailures >= maxCallbackFailures {
		log.Printf("Callback: pruning inactive/unreachable session for user=%s", username)
		delete(fs.sessions, username)
	}
}

func (fs *FileServer) sendInvalidate(target clientSession, changedFID *domain.FID, eventType uint64) {
	if changedFID == nil {
		return
	}

	if len(target.streams) > 0 {
		ev := &pb.WatchEvent{EventType: eventType, Fid: changedFID.ToProto()}
		for _, es := range target.streams {
			select {
			case es.ch <- ev:
			case <-es.done:
			default:
				log.Printf("Callback: event stream for user=%s is full; closing it so the client resyncs", target.username)
				es.close()
			}
		}
		return
	}

	if target.callbackAddress == "" {
		return
	}

	// Callbacks are lightweight invalidation pulses; dial over plaintext (Option A)
	opts := []grpc.DialOption{grpc.WithInsecure()}

	conn, err := grpc.NewClient(target.callbackAddress, opts...)
	if err != nil {
		log.Printf("Callback: dial failed for user=%s addr=%s err=%v", target.username, target.callbackAddress, err)
		fs.recordCallbackResult(target.username, false)
		return
	}
	defer conn.Close()

	client := cbpb.NewClientCallbackClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cancel()

	_, err = client.Invalidate(ctx, &cbpb.InvalidateRequest{
		Fid:        changedFID.ToProto(),
		NewVersion: eventType,
	})
	if err != nil {
		log.Printf("Callback: invalidate failed for user=%s addr=%s err=%v", target.username, target.callbackAddress, err)
		fs.recordCallbackResult(target.username, false)
		return
	}

	fs.recordCallbackResult(target.username, true)
}

// parentAndName returns a copy of the inode's parent FID and its current name,
// or (nil, "") if the inode or its parent is unknown.
func (fs *FileServer) parentAndName(fid *domain.FID) (*domain.FID, string) {
	if fid == nil {
		return nil, ""
	}
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	inode, err := fs.GetInode(fid)
	if err != nil || inode == nil || inode.Parent == nil || inode.Parent.FID == nil {
		return nil, ""
	}
	p := inode.Parent.FID
	return &domain.FID{
		FileServerID:     p.FileServerID,
		InodeID:          p.InodeID,
		GenerationNumber: p.GenerationNumber,
	}, inode.Name
}

// NotifyFileUpdated notifies active users in the same current directory about file changes.
func (fs *FileServer) NotifyFileUpdated(parentFID *domain.FID, name, originUsername string) {
	if parentFID == nil || name == "" {
		return
	}

	fs.mu.RLock()
	parentInode, err := fs.GetInode(parentFID)
	if err != nil {
		fs.mu.RUnlock()
		log.Printf("NotifyFileUpdated: parent inode lookup failed: %v", err)
		return
	}

	changedInode, err := fs.GetChildInodeByName(parentInode, name)
	if err != nil {
		fs.mu.RUnlock()
		log.Printf("NotifyFileUpdated: changed inode lookup failed: %v", err)
		return
	}
	if changedInode.FID == nil {
		fs.mu.RUnlock()
		return
	}

	changedFID := &domain.FID{
		FileServerID:     changedInode.FID.FileServerID,
		InodeID:          changedInode.FID.InodeID,
		GenerationNumber: changedInode.FID.GenerationNumber,
	}
	targets := fs.snapshotNotifyTargetsForDirLocked(parentFID.String(), originUsername)
	fs.mu.RUnlock()

	for _, target := range targets {
		targetCopy := target
		go fs.sendInvalidate(targetCopy, changedFID, callbackEventFileUpdated)
	}
}

// NotifyNewFileInDir notifies active users in the same directory that a new file was uploaded.
func (fs *FileServer) NotifyNewFileInDir(parentFID *domain.FID, name, originUsername string) {
	if parentFID == nil || name == "" {
		return
	}

	parentFIDCopy := &domain.FID{
		FileServerID:     parentFID.FileServerID,
		InodeID:          parentFID.InodeID,
		GenerationNumber: parentFID.GenerationNumber,
	}

	fs.mu.RLock()
	targets := fs.snapshotNotifyTargetsForDirLocked(parentFID.String(), originUsername)
	fs.mu.RUnlock()

	for _, target := range targets {
		targetCopy := target
		go fs.sendInvalidate(targetCopy, parentFIDCopy, callbackEventDirNewFile)
	}
}

// NotifyFileDeletedInDir notifies active users in the same directory that a file was deleted.
func (fs *FileServer) NotifyFileDeletedInDir(parentFID *domain.FID, name, originUsername string) {
	if parentFID == nil || name == "" {
		return
	}

	parentFIDCopy := &domain.FID{
		FileServerID:     parentFID.FileServerID,
		InodeID:          parentFID.InodeID,
		GenerationNumber: parentFID.GenerationNumber,
	}

	fs.mu.RLock()
	targets := fs.snapshotNotifyTargetsForDirLocked(parentFID.String(), originUsername)
	fs.mu.RUnlock()

	for _, target := range targets {
		targetCopy := target
		go fs.sendInvalidate(targetCopy, parentFIDCopy, callbackEventFileDeleted)
	}
}
