package admin

import (
	"context"
	"fmt"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// CallSetQuota invokes the SetQuota gRPC RPC on the target fileserver address.
func (a *AdminServer) CallSetQuota(address string, username string, quotaBytes uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return fmt.Errorf("failed to connect to fileserver at %s: %w", address, err)
	}
	defer conn.Close()

	client := pb.NewFileServerClient(conn)
	resp, err := client.SetQuota(ctx, &pb.SetQuotaRequest{
		Username:   username,
		QuotaBytes: quotaBytes,
	})
	if err != nil {
		return fmt.Errorf("SetQuota RPC failed: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("SetQuota error from fileserver: %s", resp.Error)
	}

	return nil
}
