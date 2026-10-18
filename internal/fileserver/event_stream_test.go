package fileserver

import (
	"testing"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
)

func testFID(id uint64) *domain.FID {
	return &domain.FID{FileServerID: "fs-test", InodeID: id, GenerationNumber: 1}
}

// An open stream keeps a session a notify target even with no recent RPCs,
// and needs no callback address.
func TestEventStreamSessionActiveWithoutCallbackAddress(t *testing.T) {
	fs := newTestFileServer(t)
	es := fs.attachEventStream("bob")
	defer fs.detachEventStream("bob", es)

	dir := testFID(7)
	fs.mu.Lock()
	fs.sessions["bob"].currentDirFID = dir.String()
	fs.sessions["bob"].lastSeenAt = time.Now().Add(-10 * activeSessionTTL)
	fs.mu.Unlock()

	fs.mu.RLock()
	targets := fs.snapshotNotifyTargetsForDirLocked(dir.String(), "alice")
	fs.mu.RUnlock()
	if len(targets) != 1 || targets[0].username != "bob" {
		t.Fatalf("expected bob as stream target, got %+v", targets)
	}

	fs.sendInvalidate(targets[0], dir, callbackEventDirNewFile)
	select {
	case ev := <-es.ch:
		if ev.EventType != callbackEventDirNewFile || domain.FIDFromProto(ev.Fid).String() != dir.String() {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatalf("event was not pushed to the stream")
	}
}

// Every running client of the same user gets the event.
func TestEventStreamFansOutToAllStreamsOfUser(t *testing.T) {
	fs := newTestFileServer(t)
	a := fs.attachEventStream("bob")
	b := fs.attachEventStream("bob")
	defer fs.detachEventStream("bob", a)
	defer fs.detachEventStream("bob", b)

	dir := testFID(9)
	fs.mu.Lock()
	fs.sessions["bob"].currentDirFID = dir.String()
	fs.mu.Unlock()

	fs.mu.RLock()
	targets := fs.snapshotNotifyTargetsForDirLocked(dir.String(), "")
	fs.mu.RUnlock()
	if len(targets) != 1 {
		t.Fatalf("expected one target session, got %d", len(targets))
	}
	fs.sendInvalidate(targets[0], dir, callbackEventFileDeleted)
	for name, es := range map[string]*eventStream{"a": a, "b": b} {
		select {
		case <-es.ch:
		case <-time.After(time.Second):
			t.Fatalf("stream %s did not receive the event", name)
		}
	}
}

// A client too slow to drain its stream is cut off (it resyncs on reconnect)
// rather than blocking the server.
func TestEventStreamFullBufferClosesStream(t *testing.T) {
	fs := newTestFileServer(t)
	es := fs.attachEventStream("bob")
	defer fs.detachEventStream("bob", es)
	target := clientSession{username: "bob", streams: map[uint64]*eventStream{es.id: es}}

	for i := 0; i < eventStreamBuffer+1; i++ {
		fs.sendInvalidate(target, testFID(1), callbackEventFileUpdated)
	}
	select {
	case <-es.done:
	default:
		t.Fatalf("expected overflowing stream to be closed")
	}
}

// Explicit disconnect ends the user's streams; detaching keeps the session.
func TestEventStreamLifecycle(t *testing.T) {
	fs := newTestFileServer(t)
	es := fs.attachEventStream("bob")
	fs.detachEventStream("bob", es)

	fs.mu.RLock()
	s, ok := fs.sessions["bob"]
	open := ok && len(s.streams) > 0
	fs.mu.RUnlock()
	if !ok || open {
		t.Fatalf("after detach: session exists=%v, streams open=%v; want session kept, no streams", ok, open)
	}

	es2 := fs.attachEventStream("bob")
	fs.RemoveClientSession("bob")
	select {
	case <-es2.done:
	default:
		t.Fatalf("RemoveClientSession should close the user's streams")
	}
	fs.detachEventStream("bob", es2) // must not resurrect or panic
	fs.mu.RLock()
	_, ok = fs.sessions["bob"]
	fs.mu.RUnlock()
	if ok {
		t.Fatalf("session should be gone after RemoveClientSession")
	}
}
