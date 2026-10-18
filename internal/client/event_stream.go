package client

import (
	"context"
	"log"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Cache invalidations are pushed by the fileserver over a WatchEvents stream
// that this client opens. Because the client dials out (like every other RPC),
// it works behind NAT, campus Wi-Fi and client firewalls, and needs no
// listening port. Servers that predate WatchEvents fall back to the legacy
// callback listener (callback_server.go).

const (
	// eventStreamReadyTimeout bounds how long Connect waits for the stream to
	// come up before carrying on and retrying in the background.
	eventStreamReadyTimeout = 5 * time.Second
	eventStreamMinBackoff   = 1 * time.Second
	eventStreamMaxBackoff   = 30 * time.Second
)

// startEventStream opens the WatchEvents stream and waits briefly for the
// server's ready message. If the server does not implement WatchEvents it
// switches to legacy callbacks.
func (c *Client) startEventStream() {
	c.stopEventStream()
	if c.serverConn == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	ready := make(chan error, 1)
	c.watchCancel = cancel
	c.watchDone = done

	go c.runEventStream(ctx, c.serverConn, ready, done)

	select {
	case err := <-ready:
		if status.Code(err) == codes.Unimplemented {
			c.stopEventStream()
			c.enableLegacyCallbacks()
		}
	case <-time.After(eventStreamReadyTimeout):
		log.Printf("[EVENTS] server did not confirm the change-notification stream yet; retrying in the background")
	}
}

// stopEventStream cancels the stream goroutine and waits for it to exit.
func (c *Client) stopEventStream() {
	if c.watchCancel == nil {
		return
	}
	c.watchCancel()
	<-c.watchDone
	c.watchCancel = nil
	c.watchDone = nil
}

// runEventStream keeps a WatchEvents stream open until ctx is cancelled,
// reconnecting with exponential backoff. The first attempt's outcome is
// reported on ready (nil once the server's ready message arrives).
func (c *Client) runEventStream(ctx context.Context, conn pb.FileServerClient, ready chan<- error, done chan<- struct{}) {
	defer close(done)

	reported := false
	report := func(err error) {
		if !reported {
			reported = true
			ready <- err
		}
	}

	backoff := eventStreamMinBackoff
	connectedBefore := false
	warnedAuth := false
	for {
		err := c.watchOnce(ctx, conn, func() {
			if connectedBefore {
				c.onEventStreamReconnected(conn)
			}
			connectedBefore = true
			warnedAuth = false
			backoff = eventStreamMinBackoff
			report(nil)
		})
		if ctx.Err() != nil {
			return
		}
		if status.Code(err) == codes.Unimplemented {
			report(err)
			return
		}
		report(err)
		if code := status.Code(err); code == codes.Unauthenticated || code == codes.PermissionDenied {
			// The server no longer accepts this session (expired, or this
			// machine's IP changed). Retrying can't fix that until the user
			// re-registers, which `refresh` does; keep retrying slowly.
			if !warnedAuth {
				warnedAuth = true
				c.Notify("\n[NOTIFY] Live updates paused: the server no longer accepts this session. Run refresh to reconnect.\n")
			}
			backoff = eventStreamMaxBackoff
		} else {
			log.Printf("[EVENTS] change-notification stream lost (%v); reconnecting in %s", err, backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > eventStreamMaxBackoff {
			backoff = eventStreamMaxBackoff
		}
	}
}

// watchOnce runs a single stream until it ends. onReady is called when the
// server's ready message (event_type 0) arrives.
func (c *Client) watchOnce(ctx context.Context, conn pb.FileServerClient, onReady func()) error {
	stream, err := conn.WatchEvents(ctx, &pb.WatchEventsRequest{
		ClientId: c.clientID,
		Username: c.username,
	})
	if err != nil {
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	onReady()
	if first.GetEventType() != 0 {
		c.handleInvalidation(domain.FIDFromProto(first.GetFid()), first.GetEventType())
	}

	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		if ev.GetEventType() == 0 || ev.GetFid() == nil {
			continue // keepalive
		}
		c.handleInvalidation(domain.FIDFromProto(ev.GetFid()), ev.GetEventType())
	}
}

// onEventStreamReconnected resyncs after a gap: events may have been missed
// while the stream was down, so the current directory is re-fetched on the
// next command, and the server is told which directory we are in (the
// session may have been recreated without it).
func (c *Client) onEventStreamReconnected(conn pb.FileServerClient) {
	c.sendHeartbeat(conn)

	c.fidMu.Lock()
	cur := c.currentFID
	c.fidMu.Unlock()
	if ch := c.attachedCacheHandler(); ch != nil && cur != nil {
		ch.QueueDirInvalidation(cur)
	}
}

// enableLegacyCallbacks starts the inbound callback listener and re-registers
// so the server can dial it. Only used with servers that predate WatchEvents;
// this path needs the fileserver to reach this machine directly.
func (c *Client) enableLegacyCallbacks() {
	if c.legacyCallbacks {
		return
	}
	if c.stopCallback == nil {
		addr, stopFn, err := c.startCallbackServer()
		if err != nil {
			log.Printf("[EVENTS] could not start legacy callback listener: %v", err)
			return
		}
		c.callbackAddr = addr
		c.stopCallback = stopFn
	}
	c.legacyCallbacks = true
	log.Printf("[EVENTS] server does not support pushed notifications; using legacy callbacks (needs inbound access to this machine)")
	if err := c.ReRegister(); err != nil {
		log.Printf("[EVENTS] re-register for legacy callbacks failed: %v", err)
	}
}
