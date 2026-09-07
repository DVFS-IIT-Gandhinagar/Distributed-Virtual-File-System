//go:build use_google_auth

package fileserver

import (
	"context"
	"strings"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GetServerAuthInterceptor enforces Google OAuth token verification on fileserver client RPCs.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	cfg := auth.LoadDesktopConfig()

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization metadata in request")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 || !strings.HasPrefix(authHeaders[0], "Bearer ") {
			return nil, status.Errorf(codes.Unauthenticated, "missing or malformed Bearer authorization header")
		}

		rawToken := strings.TrimPrefix(authHeaders[0], "Bearer ")

		// Extract target username from client request if explicitly specified
		var expectedUser string
		switch r := req.(type) {
		case *pb.RegisterClientRequest:
			expectedUser = r.Username
		case *pb.UnregisterClientRequest:
			expectedUser = r.Username
		case *pb.ShareRequest:
			expectedUser = r.Username
		case *pb.UnshareRequest:
			expectedUser = r.Username
		case *pb.RestoreFileRequest:
			expectedUser = r.Username
		case *pb.ShowTrashRequest:
			expectedUser = r.Username
		case *pb.SetQuotaRequest:
			expectedUser = r.Username
		}

		_, err := auth.VerifyToken(ctx, rawToken, expectedUser, cfg.ClientID)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "google authentication failed: %v", err)
		}

		return handler(ctx, req)
	}
}
