package client

import (
	"context"
	"net"

	cbpb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/callback"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/domain"
	"google.golang.org/grpc"
)

const callbackEventFileUpdated uint64 = 1
const callbackEventDirNewFile uint64 = 2
const callbackEventFileDeleted uint64 = 3

type callbackServer struct {
	cbpb.UnimplementedClientCallbackServer
	client *Client
}

func (c *Client) startCallbackServer() (string, func() error, error) {
	lis, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", nil, err
	}

	grpcServer := grpc.NewServer()
	cbpb.RegisterClientCallbackServer(grpcServer, &callbackServer{client: c})

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	stopFn := func() error {
		grpcServer.Stop()
		return lis.Close()
	}

	return lis.Addr().String(), stopFn, nil
}

func (s *callbackServer) Invalidate(ctx context.Context, req *cbpb.InvalidateRequest) (*cbpb.InvalidateResponse, error) {
	_ = ctx
	if req == nil || req.Fid == nil {
		return &cbpb.InvalidateResponse{Success: false}, nil
	}

	fid := domain.FIDFromProto(req.Fid)
	eventType := req.NewVersion
	if eventType == 0 {
		eventType = callbackEventFileUpdated
	}

	if s.client != nil {
		s.client.handleInvalidation(fid, eventType)
	}

	return &cbpb.InvalidateResponse{Success: true}, nil
}

// handleInvalidation applies one invalidation event, whether it arrived over
// the WatchEvents stream or a legacy callback. It runs off the shell
// goroutine, so it only queues work for the cache (applied before the next
// command) and prints a notice.
func (c *Client) handleInvalidation(fid *domain.FID, eventType uint64) {
	if c == nil || fid == nil {
		return
	}
	ch := c.attachedCacheHandler()

	switch eventType {
	case callbackEventDirNewFile:
		ch.QueueDirInvalidation(fid)
		c.Notify("\n[NOTIFY] New file or folder added in your current directory by another user. Cache invalidated; run ls to see it.\n")
	case callbackEventFileDeleted:
		ch.QueueDirInvalidation(fid)
		c.Notify("\n[NOTIFY] A file or folder was deleted in your current directory by another user. Cache invalidated; run ls to see the change.\n")
	default:
		ch.QueueFileInvalidation(fid)
		c.Notify("\n[NOTIFY] File updated by another user. Cache invalidated.\n")
	}
}
