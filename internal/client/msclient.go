package client

import (
	"context"
	"fmt"
	"net"
	"strings"

	mspb "github.com/umangshikarvar/dvfs/api/metaserver"
	"github.com/umangshikarvar/dvfs/internal/certs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// GetRoots gets the accessible roots to the user from the metaserver
func (client *Client) GetRoots(msAddr string) ([]SharedRoot, error) {
	if msAddr == "" {
		return []SharedRoot{}, nil
	}

	// Build the same CA-backed TLS config the client uses when talking to FS.
	var opts []grpc.DialOption
	if client.useTLS {
		cp, err := certs.NewCAPool()
		if err != nil {
			return []SharedRoot{}, err
		}

		host, _, err := net.SplitHostPort(msAddr)
		if err != nil {
			host = msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(msAddr, opts...)
	if err != nil {
		return []SharedRoot{}, fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	mc := mspb.NewMetaServerClient(conn)
	resp, err := mc.GetRoots(context.Background(), &mspb.GetRootsRequest{
		Username: client.username,
	})
	if err != nil {
		return []SharedRoot{}, fmt.Errorf("Navigate RPC failed: %w", err)
	}
	if !resp.Success {
		return []SharedRoot{}, fmt.Errorf("meta server rejected get roots: %s", resp.Error)
	}

	SharedRoots := []SharedRoot{}
	for _, root := range resp.Roots {
		SharedRoots = append(SharedRoots, SharedRoot{Path: root.Path, DisplayName: root.DisplayName, Owner: root.Owner})
	}

	return SharedRoots, nil
}

// NavigateToFileServer dials the meta server over TLS and navigates the client to the appropriate file server.
// selfAddr is the host:port that the meta server should store as this FS's address.
// If msAddr is empty this is a no-op.
func (client *Client) NavigateToFileServer(msAddr string) (string, error) {
	if msAddr == "" {
		return "", nil
	}

	// Build the same CA-backed TLS config the client uses when talking to FS.
	var opts []grpc.DialOption
	if client.useTLS {
		cp, err := certs.NewCAPool()
		if err != nil {
			return "", err
		}

		host, _, err := net.SplitHostPort(msAddr)
		if err != nil {
			host = msAddr
		}
		creds := credentials.NewClientTLSFromCert(cp, host)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	conn, err := grpc.NewClient(msAddr, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to connect to meta server: %w", err)
	}
	defer conn.Close()

	mc := mspb.NewMetaServerClient(conn)
	resp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{
		Username: client.username,
		RootUser: client.root_user,
	})
	if err != nil {
		return "", fmt.Errorf("Navigate RPC failed: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("meta server rejected navigation: %s", resp.Error)
	}

	client.SetExpectedServerCertFingerprintSHA256("")
	if client.useTLS {
		expected := strings.TrimSpace(resp.FileserverCertFingerprintSha256)
		if expected == "" {
			return "", fmt.Errorf("metaserver did not provide fileserver certificate fingerprint for %s", resp.Address)
		}
		client.SetExpectedServerCertFingerprintSHA256(expected)
	}

	return resp.Address, nil
}
