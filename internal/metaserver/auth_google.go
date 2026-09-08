//go:build use_google_auth

package metaserver

import (
	"context"
	"os"
	"strings"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// GetServerAuthInterceptor enforces authentication on MetaServer RPCs.
func GetServerAuthInterceptor() grpc.UnaryServerInterceptor {
	cfg := auth.LoadDesktopConfig()

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Cluster control plane RPCs (daemon synchronization and heartbeats)
		switch info.FullMethod {
		case "/metaserver.MetaServer/RegisterFileServer",
			"/metaserver.MetaServer/Heartbeat",
			"/metaserver.MetaServer/RootShare",
			"/metaserver.MetaServer/RootUnshare":
			if err := verifyClusterPeer(ctx); err != nil {
				return nil, err
			}
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
		}

		_, err := auth.VerifyToken(ctx, rawToken, expectedUser, cfg.ClientID)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "google authentication failed: %v", err)
		}

		return handler(ctx, req)
	}
}

func verifyClusterPeer(ctx context.Context) error {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		if os.Getenv("DVFS_AUTH_MOCK") == "true" || os.Getenv("DVFS_AUTH_MOCK") == "1" {
			return nil
		}
		return status.Errorf(codes.Unauthenticated, "cluster RPC requires mutual TLS peer certificate")
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		if os.Getenv("DVFS_AUTH_MOCK") == "true" || os.Getenv("DVFS_AUTH_MOCK") == "1" {
			return nil
		}
		return status.Errorf(codes.Unauthenticated, "cluster RPC requires TLS transport")
	}

	// Validate node identity
	if len(tlsInfo.State.PeerCertificates) == 0 {
		if os.Getenv("DVFS_AUTH_MOCK") == "true" || os.Getenv("DVFS_AUTH_MOCK") == "1" {
			return nil
		}
		return status.Errorf(codes.Unauthenticated, "missing peer client certificate for cluster RPC")
	}

	cert := tlsInfo.State.PeerCertificates[0]
	authorized := isAuthorizedClusterIdentity(cert.Subject.CommonName)
	if !authorized {
		for _, dns := range cert.DNSNames {
			if isAuthorizedClusterIdentity(dns) {
				authorized = true
				break
			}
		}
	}
	if !authorized {
		return status.Errorf(codes.PermissionDenied, "unauthorized cluster node certificate: %s", cert.Subject.CommonName)
	}

	return nil
}

func isAuthorizedClusterIdentity(name string) bool {
	if name == "" {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "localhost" || name == "127.0.0.1" || name == "fileserver" || name == "metaserver" || name == "mds" {
		return true
	}
	if strings.HasPrefix(name, "dvfs") || strings.HasPrefix(name, "fs") {
		return true
	}
	if strings.HasSuffix(name, ".cluster.local") ||
		strings.HasSuffix(name, ".dvfs.cluster") ||
		strings.HasSuffix(name, ".local") {
		return true
	}
	return false
}
