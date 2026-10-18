//go:build use_google_auth

package fileserver

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// WatchEvents goes through the same SST stream auth as uploads/downloads, and
// the stream is bound to the authenticated user, not the username in the request.
func TestWatchEventsRequiresSSTAndBindsToCaller(t *testing.T) {
	fs := newTestFileServer(t)
	store := fs.SessionStore()
	if store == nil {
		store = session.NewStore(15*time.Minute, 60*time.Second)
		t.Cleanup(func() { store.Close() })
	}
	SetGlobalSessionStore(store)
	srv := grpc.NewServer(grpc.StreamInterceptor(GetServerStreamAuthInterceptor()))
	pb.RegisterFileServerServer(srv, NewGRPCHandler(fs))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewFileServerClient(conn)

	// No token: rejected.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := client.WatchEvents(ctx, &pb.WatchEventsRequest{Username: "bob@example.com"})
	require.NoError(t, err)
	_, err = st.Recv()
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	// Valid SST for bob, but the request claims to be alice.
	rawSST, err := session.GenerateSST()
	require.NoError(t, err)
	_, err = store.CreateSession(rawSST, "bob@example.com", "client-bob", "", "", "", time.Now().Add(time.Hour))
	require.NoError(t, err)

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+rawSST)
	st, err = client.WatchEvents(authCtx, &pb.WatchEventsRequest{Username: "alice@example.com"})
	require.NoError(t, err)
	ready, err := st.Recv()
	require.NoError(t, err)
	require.EqualValues(t, 0, ready.GetEventType())

	fs.mu.RLock()
	_, aliceAttached := fs.sessions["alice@example.com"]
	bob := fs.sessions["bob@example.com"]
	bobStreams := 0
	if bob != nil {
		bobStreams = len(bob.streams)
	}
	fs.mu.RUnlock()
	require.False(t, aliceAttached, "request username must not override the authenticated caller")
	require.Equal(t, 1, bobStreams)

	// A change in bob's directory is pushed down the stream.
	dir := testFID(42)
	fs.mu.Lock()
	bob.currentDirFID = dir.String()
	fs.mu.Unlock()
	fs.NotifyFileDeletedInDir(dir, "gone.txt", "alice@example.com")

	ev, err := st.Recv()
	require.NoError(t, err)
	require.EqualValues(t, callbackEventFileDeleted, ev.GetEventType())
}
