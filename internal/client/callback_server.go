package client

import (
	"context"
	"fmt"
	"net"

	cbpb "github.com/umangshikarvar/dvfs/api/callback"
	"github.com/umangshikarvar/dvfs/internal/domain"
	"google.golang.org/grpc"
)

type callbackServer struct {
	cbpb.UnimplementedClientCallbackServer
	client *Client
}

func (c *Client) startCallbackServer() (string, func() error, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
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
	if s.client != nil && s.client.cacheHandler != nil {
		path, ok := s.client.cacheHandler.InvalidateFileByFID(fid)
		if ok {
			fmt.Printf("\n[NOTIFY] File updated by another user. Cache invalidated: %s\n", path)
		} else {
			fmt.Printf("\n[NOTIFY] File updated by another user. Please refresh current view. (FID: %s)\n", fid.String())
		}
	} else {
		fmt.Printf("\n[NOTIFY] File updated by another user. Please refresh current view. (FID: %s)\n", fid.String())
	}

	return &cbpb.InvalidateResponse{Success: true}, nil
}
