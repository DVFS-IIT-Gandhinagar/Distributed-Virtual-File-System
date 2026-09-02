package fileserver

import (
	"context"
	"crypto/x509"
	"log"
	"net"
	"os"
	"time"

	cbpb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/callback"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type clientSession struct {
	username            string
	callbackAddress     string
	rootFID             string
	currentDirFID       string
	lastSeenAt          time.Time
	consecutiveFailures int
}

const activeSessionTTL = 5 * time.Minute
const callbackTimeout = 3 * time.Second
const maxCallbackFailures = 3
const callbackEventFileUpdated uint64 = 1
const callbackEventDirNewFile uint64 = 2
const callbackEventFileDeleted uint64 = 3

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

func (fs *FileServer) isSessionActiveLocked(session *clientSession, now time.Time) bool {
	if session == nil || session.lastSeenAt.IsZero() {
		return false
	}
	return now.Sub(session.lastSeenAt) <= activeSessionTTL
}

func (fs *FileServer) snapshotNotifyTargetsForDirLocked(currentDirFID, originUsername string) []clientSession {
	now := time.Now()
	targets := make([]clientSession, 0)

	for _, session := range fs.sessions {
		if session == nil || session.callbackAddress == "" {
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
		targets = append(targets, *session)
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
	if target.callbackAddress == "" || changedFID == nil {
		return
	}

	var opts []grpc.DialOption
	if fs.useTLS {
		cp := x509.NewCertPool()
		caBytes, err := os.ReadFile(fs.caCertPath)
		if err != nil {
			log.Printf("Callback: failed to read CA cert file: %v", err)
			fs.recordCallbackResult(target.username, false)
			return
		}
		if !cp.AppendCertsFromPEM(caBytes) {
			log.Printf("Callback: failed to append CA cert for user=%s", target.username)
			fs.recordCallbackResult(target.username, false)
			return
		}

		host, _, err := net.SplitHostPort(target.callbackAddress)
		if err != nil {
			host = target.callbackAddress
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

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
