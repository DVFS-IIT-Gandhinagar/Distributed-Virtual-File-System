package client

import (
	"context"
	"fmt"
	"net"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

// GetRoots gets the accessible roots to the user from the metaserver
func (client *Client) GetRoots(msAddr string) ([]SharedRoot, error) {
	if msAddr == "" {
		return []SharedRoot{}, nil
	}

	// Build the Root CA-backed TLS config the client uses when talking to servers.
	var opts []grpc.DialOption
	if !client.insecure {
		cp, err := NewDVFSUniversalCertPool()
		if err != nil {
			return []SharedRoot{}, fmt.Errorf("failed to load Root CA: %w", err)
		}

		host := client.serverName
		if host == "" {
			if client.resolver != nil {
				host = client.resolver.ResolveServerName(msAddr)
			} else {
				var splitErr error
				host, _, splitErr = net.SplitHostPort(msAddr)
				if splitErr != nil {
					host = msAddr
				}
			}
			if net.ParseIP(host) != nil {
				if net.ParseIP(host).IsLoopback() {
					host = "localhost"
				} else {
					host = "dvfs1"
				}
			}
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
	ctx := context.Background()
	if client.authToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+client.authToken)
	}
	resp, err := mc.GetRoots(ctx, &mspb.GetRootsRequest{
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

	// Build the Root CA-backed TLS config the client uses when talking to servers.
	var opts []grpc.DialOption
	if !client.insecure {
		cp, err := NewDVFSUniversalCertPool()
		if err != nil {
			return "", fmt.Errorf("failed to load Root CA: %w", err)
		}

		host := client.serverName
		if host == "" {
			if client.resolver != nil {
				host = client.resolver.ResolveServerName(msAddr)
			} else {
				var splitErr error
				host, _, splitErr = net.SplitHostPort(msAddr)
				if splitErr != nil {
					host = msAddr
				}
			}
			if net.ParseIP(host) != nil {
				if net.ParseIP(host).IsLoopback() {
					host = "localhost"
				} else {
					host = "dvfs1"
				}
			}
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
	ctx := context.Background()
	if client.authToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+client.authToken)
	}
	resp, err := mc.Navigate(ctx, &mspb.NavigateRequest{
		Username: client.username,
		RootUser: client.root_user,
	})
	if err != nil {
		return "", fmt.Errorf("Navigate RPC failed: %w", err)
	}
	if !resp.Success {
		return "", fmt.Errorf("meta server rejected navigation: %s", resp.Error)
	}

	return resp.Address, nil
}
