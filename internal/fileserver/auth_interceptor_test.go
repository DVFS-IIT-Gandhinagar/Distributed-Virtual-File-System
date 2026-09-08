//go:build use_google_auth

package fileserver

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/fileserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/fileserver/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestUnaryAuthInterceptor_HandshakeAndSST(t *testing.T) {
	t.Setenv("DVFS_AUTH_MOCK", "true")

	store := session.NewStore(15*time.Minute, 60*time.Second)
	t.Cleanup(func() { store.Close() })
	SetGlobalSessionStore(store)

	interceptor := GetServerAuthInterceptor()

	// 1. Handshake tests (/fileserver.FileServer/RegisterClient)
	infoRegister := &grpc.UnaryServerInfo{
		FullMethod: "/fileserver.FileServer/RegisterClient",
	}

	reqRegister := &pb.RegisterClientRequest{
		Username: "alice@example.com",
	}

	// 1a. Missing metadata
	_, err := interceptor(context.Background(), reqRegister, infoRegister, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// 1b. Missing Bearer prefix
	badMD := metadata.New(map[string]string{"authorization": "Basic abcdef"})
	ctxBadMD := metadata.NewIncomingContext(context.Background(), badMD)
	_, err = interceptor(ctxBadMD, reqRegister, infoRegister, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// 1c. Valid Handshake with Google Mock Token
	mockGoogleToken := auth.MockToken("alice@example.com")
	goodMD := metadata.New(map[string]string{"authorization": "Bearer " + mockGoogleToken})
	ctxGoodMD := metadata.NewIncomingContext(context.Background(), goodMD)

	var capturedUser string
	res, err := interceptor(ctxGoodMD, reqRegister, infoRegister, func(ctx context.Context, req interface{}) (interface{}, error) {
		if val := ctx.Value(authContextKey{}); val != nil {
			capturedUser = val.(string)
		}
		return "registered", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "registered", res)
	assert.Equal(t, "alice@example.com", capturedUser)

	// 2. Subsequent RPC tests (/fileserver.FileServer/CreateFile)
	infoCreate := &grpc.UnaryServerInfo{
		FullMethod: "/fileserver.FileServer/CreateFile",
	}

	rawSST, err := session.GenerateSST()
	require.NoError(t, err)

	sess, err := store.CreateSession(rawSST, "alice@example.com", "client-1", "127.0.0.1:5000", "127.0.0.1:6000", "fs-root-1", time.Now().Add(1*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Context with matching peer IP
	peerAlice := &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}}
	ctxWithPeer := peer.NewContext(context.Background(), peerAlice)

	// 2a. Unregistered session token
	unregMD := metadata.New(map[string]string{"authorization": "Bearer invalid-sst-token-12345"})
	ctxUnreg := metadata.NewIncomingContext(ctxWithPeer, unregMD)
	_, err = interceptor(ctxUnreg, nil, infoCreate, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// 2b. Valid SST with matching peer IP
	sstMD := metadata.New(map[string]string{"authorization": "Bearer " + rawSST})
	ctxSST := metadata.NewIncomingContext(ctxWithPeer, sstMD)

	var sessionUser string
	res, err = interceptor(ctxSST, nil, infoCreate, func(ctx context.Context, req interface{}) (interface{}, error) {
		if s, ok := session.FromContext(ctx); ok && s != nil {
			sessionUser = s.Username
		}
		return "file_created", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "file_created", res)
	assert.Equal(t, "alice@example.com", sessionUser)

	// 2c. Session hijacking attempt (IP mismatch)
	peerAttacker := &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.99"), Port: 5000}}
	ctxAttacker := peer.NewContext(context.Background(), peerAttacker)
	ctxHijack := metadata.NewIncomingContext(ctxAttacker, sstMD)

	_, err = interceptor(ctxHijack, nil, infoCreate, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "should_not_reach", nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err), "IP mismatch must return PermissionDenied")
}

func TestStreamAuthInterceptor_SSTEnforcement(t *testing.T) {
	store := session.NewStore(15*time.Minute, 60*time.Second)
	t.Cleanup(func() { store.Close() })
	SetGlobalSessionStore(store)

	streamInterceptor := GetServerStreamAuthInterceptor()

	rawSST, err := session.GenerateSST()
	require.NoError(t, err)

	_, err = store.CreateSession(rawSST, "alice@example.com", "client-1", "127.0.0.1:5000", "", "", time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	peerAlice := &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000}}
	baseCtx := peer.NewContext(context.Background(), peerAlice)

	// 1. Missing Authorization metadata
	mockSSMissing := &mockServerStream{ctx: baseCtx}
	err = streamInterceptor(nil, mockSSMissing, &grpc.StreamServerInfo{FullMethod: "/fileserver.FileServer/UploadFile"}, func(srv interface{}, stream grpc.ServerStream) error {
		return nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// 2. Valid SST token in stream request
	streamMD := metadata.New(map[string]string{"authorization": "Bearer " + rawSST})
	ctxWithMD := metadata.NewIncomingContext(baseCtx, streamMD)
	mockSSValid := &mockServerStream{ctx: ctxWithMD}

	var streamCaller string
	err = streamInterceptor(nil, mockSSValid, &grpc.StreamServerInfo{FullMethod: "/fileserver.FileServer/UploadFile"}, func(srv interface{}, stream grpc.ServerStream) error {
		if s, ok := session.FromContext(stream.Context()); ok && s != nil {
			streamCaller = s.Username
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", streamCaller)

	// 3. Raw Google token on stream without legacy fallback must be rejected
	mockGoogleToken := auth.MockToken("alice@example.com")
	streamGoogleMD := metadata.New(map[string]string{"authorization": "Bearer " + mockGoogleToken})
	ctxWithGoogleMD := metadata.NewIncomingContext(baseCtx, streamGoogleMD)
	mockSSGoogle := &mockServerStream{ctx: ctxWithGoogleMD}
	err = streamInterceptor(nil, mockSSGoogle, &grpc.StreamServerInfo{FullMethod: "/fileserver.FileServer/UploadFile"}, func(srv interface{}, stream grpc.ServerStream) error {
		return nil
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err), "Raw Google token must be rejected on streaming RPCs")
}

func TestGRPCHandler_SessionLifecycle(t *testing.T) {
	fs := newTestFileServer(t)
	handler := NewGRPCHandler(fs)

	peerAlice := &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}}
	ctxWithPeer := peer.NewContext(context.Background(), peerAlice)

	// 1. RegisterClient issues SST
	regResp, err := handler.RegisterClient(ctxWithPeer, &pb.RegisterClientRequest{
		Username:        "alice@example.com",
		RootUser:        "alice@example.com",
		RootPath:        "alice@example.com",
		ClientId:        "client-alice-lifecycle-1",
		CallbackAddress: "127.0.0.1:54322",
	})
	require.NoError(t, err)
	assert.True(t, regResp.Success)
	assert.NotEmpty(t, regResp.SessionToken, "RegisterClient must return non-empty SessionToken")
	assert.True(t, regResp.ExpiresAt > 0, "RegisterClient must return positive ExpiresAt timestamp")

	// 2. Validate session token in SessionStore
	sess, err := fs.SessionStore().ValidateSession(regResp.SessionToken, "127.0.0.1:54321")
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", sess.Username)
	assert.Equal(t, "client-alice-lifecycle-1", sess.ClientID)

	// 3. UnregisterClient revokes session
	unregResp, err := handler.UnregisterClient(ctxWithPeer, &pb.UnregisterClientRequest{
		Username: "alice@example.com",
		ClientId: "client-alice-lifecycle-1",
	})
	require.NoError(t, err)
	assert.True(t, unregResp.Success)

	// 4. Session lookup must fail immediately after unregistration
	_, err = fs.SessionStore().ValidateSession(regResp.SessionToken, "127.0.0.1:54321")
	assert.Equal(t, session.ErrSessionNotFound, err, "Session must be revoked upon UnregisterClient")
}

