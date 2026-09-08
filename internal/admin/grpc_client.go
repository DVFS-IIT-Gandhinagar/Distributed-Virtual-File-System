package admin

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// CallSetQuota invokes the SetQuota gRPC RPC on the target fileserver address.
// It supports TLS-encrypted connections using the embedded DVFS Root CA pool and resolves
// cluster node ServerNames via DiscoveryResolver, with fallback to plaintext for local mock environments.
func (a *AdminServer) CallSetQuota(address string, username string, quotaBytes uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if a.authManager != nil && a.authManager.GetHash() != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-admin-password-hash", a.authManager.GetHash())
	}

	// 1. Determine TLS ServerName
	serverName := ""
	if a.resolver != nil {
		serverName = a.resolver.ResolveServerName(address)
	}
	if serverName == "" || serverName == address {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			serverName = "localhost"
		} else {
			serverName = host
		}
	}

	// 2. Attempt TLS connection using embedded DVFS Universal Root CA
	var rpcErr error
	if cp, err := client.NewDVFSUniversalCertPool(); err == nil {
		tlsCreds := credentials.NewTLS(&tls.Config{
			RootCAs:    cp,
			ServerName: serverName,
		})
		if conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(tlsCreds)); err == nil {
			defer conn.Close()
			c := pb.NewFileServerClient(conn)
			resp, callErr := c.SetQuota(ctx, &pb.SetQuotaRequest{
				Username:   username,
				QuotaBytes: quotaBytes,
			})
			if callErr == nil {
				if !resp.Success {
					return fmt.Errorf("SetQuota error from fileserver: %s", resp.Error)
				}
				return nil
			}
			rpcErr = callErr
		}
	}

	// 3. Fallback to plaintext insecure dial (e.g. for mock unit tests or non-TLS setups)
	insecureConn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		if rpcErr != nil {
			return fmt.Errorf("failed to connect to fileserver at %s (TLS: %v, Plaintext: %w)", address, rpcErr, err)
		}
		return fmt.Errorf("failed to dial fileserver at %s: %w", address, err)
	}
	defer insecureConn.Close()

	client := pb.NewFileServerClient(insecureConn)
	resp, err := client.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   username,
		QuotaBytes: quotaBytes,
	})
	if err != nil {
		if rpcErr != nil {
			return fmt.Errorf("SetQuota RPC failed on %s (TLS: %v, Plaintext: %w)", address, rpcErr, err)
		}
		return fmt.Errorf("SetQuota RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("SetQuota error from fileserver: %s", resp.Error)
	}

	return nil
}
