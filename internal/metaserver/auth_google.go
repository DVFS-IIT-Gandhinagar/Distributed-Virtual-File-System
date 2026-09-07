//go:build use_google_auth

package metaserver

import (
	"context"
	"strings"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GetServerAuthInterceptor enforces Google OAuth token verification on client RPCs.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	cfg := auth.LoadDesktopConfig()

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Exempt internal cluster RPCs (fileserver registration and heartbeats)
		switch info.FullMethod {
		case "/metaserver.MetaServer/RegisterFileServer", "/metaserver.MetaServer/Heartbeat":
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization metadata in request")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 || !strings.HasPrefix(authHeaders[0], "Bearer ") {
			return nil, status.Errorf(codes.Unauthenticated, "missing or malformed Bearer authorization header")
		}

		rawToken := strings.TrimPrefix(authHeaders[0], "Bearer ")

		// Extract target username from client request if present
		var expectedUser string
		switch r := req.(type) {
		case *pb.GetRootsRequest:
			expectedUser = r.Username
		case *pb.NavigateRequest:
			expectedUser = r.Username
		case *pb.RootShareRequest:
			expectedUser = r.Owner
		case *pb.RootUnshareRequest:
			expectedUser = r.Owner
		}

		_, err := auth.VerifyToken(ctx, rawToken, expectedUser, cfg.ClientID)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "google authentication failed: %v", err)
		}

		return handler(ctx, req)
	}
}
