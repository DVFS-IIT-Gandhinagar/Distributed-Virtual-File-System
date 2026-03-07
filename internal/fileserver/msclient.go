package fileserver

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"

	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// RegisterWithMetaServer dials the meta server over TLS and registers this file
// server along with all users it currently knows about.
// selfAddr is the host:port that the meta server should store as this FS's address.
// If msAddr is empty this is a no-op.
func (fs *FileServer) RegisterWithMetaServer(msAddr, selfAddr string) error {
	if msAddr == "" {
		return nil
	}

	// Build the same CA-backed TLS config the client uses when talking to FS.
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(certs.CACert) {
		return fmt.Errorf("failed to append CA certificate")
	}

	host, _, err := net.SplitHostPort(msAddr)
	if err != nil {
		host = msAddr
	}
	creds := credentials.NewClientTLSFromCert(cp, host)

	conn, err := grpc.NewClient(msAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	// Collect known users under the read lock.
	fs.mu.RLock()
	users := make([]string, 0, len(fs.users))
	for u := range fs.users {
		users = append(users, u)
	}
	fs.mu.RUnlock()

	client := mspb.NewMetaServerClient(conn)
	resp, err := client.RegisterFileServer(context.Background(), &mspb.RegisterFileServerRequest{
		Address: selfAddr,
		Users:   users,
	})
	if err != nil {
		return fmt.Errorf("RegisterFileServer RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("meta server rejected registration: %s", resp.Error)
	}

	return nil
}
