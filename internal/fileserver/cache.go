package fileserver

import (
	"context"
	"fmt"

	cbpb "github.com/umangshikarvar/dvfs/api/callback"
	pb "github.com/umangshikarvar/dvfs/api/fileserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// invalidateCache sends invalidation callbacks to other clients
func (fs *FileServer) invalidateCache(fid *pb.FID, writingClientID string, newVersion uint64) {
	fs.openFilesMu.RLock()
	key := fs.fidToKey(fid)
	clients := make([]string, 0)
	for clientID := range fs.openFiles[key] {
		if clientID != writingClientID {
			clients = append(clients, clientID)
		}
	}
	fs.openFilesMu.RUnlock()

	fs.clientsMu.RLock()
	defer fs.clientsMu.RUnlock()

	for _, clientID := range clients {
		address, ok := fs.clients[clientID]
		if !ok {
			continue
		}

		go func(addr string, cID string) {
			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				fmt.Printf("Failed to connect to client %s: %v\n", cID, err)
				return
			}
			defer conn.Close()

			client := cbpb.NewClientCallbackClient(conn)
			_, err = client.Invalidate(context.Background(), &cbpb.InvalidateRequest{
				Fid:        fid,
				NewVersion: newVersion,
			})
			if err != nil {
				fmt.Printf("Failed to invalidate cache on client %s: %v\n", cID, err)
			} else {
				fmt.Printf("Invalidated cache on client %s\n", cID)
			}
		}(address, clientID)
	}
}
