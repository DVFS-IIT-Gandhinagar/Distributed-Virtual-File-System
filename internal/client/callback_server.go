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

	if eventType == callbackEventDirNewFile {
		if s.client != nil && s.client.cacheHandler != nil {
			if path, ok := s.client.cacheHandler.InvalidateFileByFID(fid); ok {
				s.client.Notify("\n[NOTIFY] New file uploaded in directory %s. Please run refresh.\n", path)
			} else {
				s.client.Notify("\n[NOTIFY] New file uploaded in your current directory. Please run refresh.\n")
			}
		} else {
			s.client.Notify("\n[NOTIFY] New file uploaded in your current directory. Please run refresh.\n")
		}
		return &cbpb.InvalidateResponse{Success: true}, nil
	}

	if eventType == callbackEventFileDeleted {
		if s.client != nil && s.client.cacheHandler != nil {
			if path, ok := s.client.cacheHandler.InvalidateFileByFID(fid); ok {
				s.client.Notify("\n[NOTIFY] A file was deleted in directory %s. Please run refresh.\n", path)
			} else {
				s.client.Notify("\n[NOTIFY] A file was deleted in your current directory. Please run refresh.\n")
			}
		} else {
			s.client.Notify("\n[NOTIFY] A file was deleted in your current directory. Please run refresh.\n")
		}
		return &cbpb.InvalidateResponse{Success: true}, nil
	}

	if s.client != nil && s.client.cacheHandler != nil {
		path, ok := s.client.cacheHandler.InvalidateFileByFID(fid)
		if ok {
			s.client.Notify("\n[NOTIFY] File updated by another user. Cache invalidated: %s\n", path)
		} else {
			s.client.Notify("\n[NOTIFY] File updated by another user. Please refresh current view. (FID: %s)\n", fid.String())
		}
	} else {
		s.client.Notify("\n[NOTIFY] File updated by another user. Please refresh current view. (FID: %s)\n", fid.String())
	}

	return &cbpb.InvalidateResponse{Success: true}, nil
}
