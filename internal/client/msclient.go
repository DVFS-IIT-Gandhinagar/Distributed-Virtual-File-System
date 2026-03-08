package client

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

// NavigateToFileServer dials the meta server over TLS and navigates the client to the appropriate file server.
// selfAddr is the host:port that the meta server should store as this FS's address.
// If msAddr is empty this is a no-op.
func (client *Client) NavigateToFileServer(msAddr, user string) (string, error) {
	if msAddr == "" {
		return "", nil
	}

	// Build the same CA-backed TLS config the client uses when talking to FS.
	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(certs.CACert) {
		return "", fmt.Errorf("failed to append CA certificate")
	}

	host, _, err := net.SplitHostPort(msAddr)
	if err != nil {
		host = msAddr
	}
	creds := credentials.NewClientTLSFromCert(cp, host)

	conn, err := grpc.NewClient(msAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return "", fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	mc := mspb.NewMetaServerClient(conn)
	resp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{
		User: user,
	})
	if err != nil {
		return "", fmt.Errorf("Navigate RPC failed: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("meta server rejected navigation: %s", resp.Error)
	}

	return resp.Address, nil
}
