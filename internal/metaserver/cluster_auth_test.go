//go:build use_google_auth

package metaserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestClusterAuthInterceptor_mTLSEnforcement(t *testing.T) {
	interceptor := GetServerAuthInterceptor()
	clusterInfo := &grpc.UnaryServerInfo{
		FullMethod: "/metaserver.MetaServer/RegisterFileServer",
	}
	req := &pb.RegisterFileServerRequest{Address: "127.0.0.1:50052"}

	// 1. In production mode without mock: missing peer info fails with Unauthenticated
	t.Run("MissingPeerFailsUnauthenticatedInProd", func(t *testing.T) {
		t.Setenv("DVFS_AUTH_MOCK", "false")

		_, err := interceptor(context.Background(), req, clusterInfo, func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	// 2. Peer with TLS but no peer certificates fails with Unauthenticated
	t.Run("PeerWithoutCertFailsUnauthenticatedInProd", func(t *testing.T) {
		t.Setenv("DVFS_AUTH_MOCK", "false")

		tlsInfo := credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{},
			},
		}
		p := &peer.Peer{AuthInfo: tlsInfo}
		ctxWithPeer := peer.NewContext(context.Background(), p)

		_, err := interceptor(ctxWithPeer, req, clusterInfo, func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	// 3. Peer with unauthorized certificate fails with PermissionDenied
	t.Run("UnauthorizedCertFailsPermissionDeniedInProd", func(t *testing.T) {
		t.Setenv("DVFS_AUTH_MOCK", "false")

		rogueCert := &x509.Certificate{
			Subject:  pkix.Name{CommonName: "rogue-server.external.com"},
			DNSNames: []string{"rogue-server.external.com"},
		}
		tlsInfo := credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{rogueCert},
			},
		}
		p := &peer.Peer{AuthInfo: tlsInfo}
		ctxWithPeer := peer.NewContext(context.Background(), p)

		_, err := interceptor(ctxWithPeer, req, clusterInfo, func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})

	// 4. Peer with authorized cluster SAN succeeds
	t.Run("AuthorizedClusterSANPasses", func(t *testing.T) {
		t.Setenv("DVFS_AUTH_MOCK", "false")

		validCert := &x509.Certificate{
			Subject:  pkix.Name{CommonName: "fs1.cluster.local"},
			DNSNames: []string{"fs1.cluster.local"},
		}
		tlsInfo := credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{validCert},
			},
		}
		p := &peer.Peer{AuthInfo: tlsInfo}
		ctxWithPeer := peer.NewContext(context.Background(), p)

		res, err := interceptor(ctxWithPeer, req, clusterInfo, func(ctx context.Context, req interface{}) (interface{}, error) {
			return "cluster_registered", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "cluster_registered", res)
	})

	// 5. In mock mode, missing peer info is allowed
	t.Run("MockModeAllowsNoPeer", func(t *testing.T) {
		t.Setenv("DVFS_AUTH_MOCK", "true")

		res, err := interceptor(context.Background(), req, clusterInfo, func(ctx context.Context, req interface{}) (interface{}, error) {
			return "mock_ok", nil
		})
		require.NoError(t, err)
		assert.Equal(t, "mock_ok", res)
	})
}
