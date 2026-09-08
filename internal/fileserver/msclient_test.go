package fileserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	mspb "github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/api/metaserver"
	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/metaserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func startTestMetaServerForFS(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	ms, err := metaserver.NewMetaServer(t.TempDir() + "/mds_state.json")
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	mspb.RegisterMetaServerServer(grpcServer, metaserver.NewGRPCHandler(ms))
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	cleanup = func() {
		grpcServer.Stop()
		_ = listener.Close()
	}

	return listener.Addr().String(), cleanup
}

func testMetaClientConn(t *testing.T, addr string) mspb.MetaServerClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithInsecure())
	if err != nil {
		t.Fatalf("failed to dial metaserver: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return mspb.NewMetaServerClient(conn)
}

func TestMSClientNoopsWhenMetaAddrEmpty(t *testing.T) {
	fs := newTestFileServer(t)

	if err := fs.RegisterWithMetaServer("127.0.0.1:50052"); err != nil {
		t.Fatalf("RegisterWithMetaServer no-op failed: %v", err)
	}
	if err := fs.RootShare("alice", "root", "/alice", "bob"); err != nil {
		t.Fatalf("RootShare no-op failed: %v", err)
	}
	if err := fs.RootUnshare("alice", "root", "/alice", "bob"); err != nil {
		t.Fatalf("RootUnshare no-op failed: %v", err)
	}
	if err := fs.HeartbeatWithMetaServer("127.0.0.1:50052"); err != nil {
		t.Fatalf("HeartbeatWithMetaServer no-op failed: %v", err)
	}

	stop := fs.StartMetaServerSync("", "127.0.0.1:50052", 10*time.Millisecond, 10*time.Millisecond)
	stop()
}

func TestRegisterWithMetaServerAndHeartbeat(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot alice failed: %v", err)
	}

	selfAddr := "127.0.0.1:50077"
	if err := fs.RegisterWithMetaServer(selfAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	if err := fs.HeartbeatWithMetaServer(selfAddr); err != nil {
		t.Fatalf("HeartbeatWithMetaServer failed: %v", err)
	}

	mc := testMetaClientConn(t, msAddr)
	rootsResp, err := mc.GetRoots(context.Background(), &mspb.GetRootsRequest{Username: "alice"})
	if err != nil {
		t.Fatalf("GetRoots RPC failed: %v", err)
	}
	if !rootsResp.Success {
		t.Fatalf("GetRoots expected success, got error=%q", rootsResp.Error)
	}

	navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "alice", RootUser: "alice"})
	if err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	}
	if !navResp.Success || navResp.Address != selfAddr {
		t.Fatalf("Navigate response mismatch: %+v", navResp)
	}
}

func TestRootShareAndUnshareNotifyMetaServer(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}

	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot alice failed: %v", err)
	}
	if _, err := fs.GetUserRoot("bob", "bob"); err != nil {
		t.Fatalf("GetUserRoot bob failed: %v", err)
	}

	selfAddr := "127.0.0.1:50078"
	if err := fs.RegisterWithMetaServer(selfAddr); err != nil {
		t.Fatalf("RegisterWithMetaServer failed: %v", err)
	}

	if err := fs.RootShare("alice", "alice", "/alice", "bob"); err != nil {
		t.Fatalf("RootShare failed: %v", err)
	}

	mc := testMetaClientConn(t, msAddr)
	if navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "bob", RootUser: "alice"}); err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	} else if !navResp.Success {
		t.Fatalf("expected bob navigation to shared root to succeed: %+v", navResp)
	}

	if err := fs.RootUnshare("alice", "alice", "/alice", "bob"); err != nil {
		t.Fatalf("RootUnshare failed: %v", err)
	}

	if navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "bob", RootUser: "alice"}); err != nil {
		t.Fatalf("Navigate RPC failed: %v", err)
	} else if navResp.Success {
		t.Fatalf("expected bob navigation to fail after unshare")
	}
}

func TestStartMetaServerSyncRegistersEventually(t *testing.T) {
	msAddr, cleanupMDS := startTestMetaServerForFS(t)
	defer cleanupMDS()

	fs, err := NewFileServer("fs-ms", t.TempDir(), msAddr)
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}
	if _, err := fs.GetUserRoot("alice", "alice"); err != nil {
		t.Fatalf("GetUserRoot failed: %v", err)
	}

	selfAddr := "127.0.0.1:50079"
	stop := fs.StartMetaServerSync(msAddr, selfAddr, 20*time.Millisecond, 40*time.Millisecond)
	defer stop()

	mc := testMetaClientConn(t, msAddr)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		navResp, err := mc.Navigate(context.Background(), &mspb.NavigateRequest{Username: "alice", RootUser: "alice"})
		if err == nil && navResp.Success && navResp.Address == selfAddr {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("metaserver sync did not register fileserver before deadline")
}

func TestFileServer_DialMetaServer_mTLSWithClientCert(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Generate Root CA
	caPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey failed: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "DVFS Test Root CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("CreateCertificate CA failed: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})
	caPath := filepath.Join(tempDir, "ca.crt")
	if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
		t.Fatalf("WriteFile ca.crt failed: %v", err)
	}

	// 2. Generate Server Certificate for MetaServer (localhost)
	serverPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey failed: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("CreateCertificate server failed: %v", err)
	}
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverBytes})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverPriv)})
	serverTLS, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair server failed: %v", err)
	}

	// 3. Generate Client Certificate for FileServer (dvfs1)
	clientPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey failed: %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName: "dvfs1",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:    []string{"dvfs1", "localhost"},
	}
	clientBytes, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("CreateCertificate client failed: %v", err)
	}
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientBytes})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientPriv)})
	clientCertPath := filepath.Join(tempDir, "client.crt")
	clientKeyPath := filepath.Join(tempDir, "client.key")
	_ = os.WriteFile(clientCertPath, clientCertPEM, 0644)
	_ = os.WriteFile(clientKeyPath, clientKeyPEM, 0600)

	// 4. Start MetaServer with mTLS (RequireAndVerifyClientCert against ca.crt)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLS},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	creds := credentials.NewTLS(serverTLSConfig)

	ms, err := metaserver.NewMetaServer(filepath.Join(tempDir, "mds_state.json"))
	if err != nil {
		t.Fatalf("NewMetaServer failed: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	mspb.RegisterMetaServerServer(grpcServer, metaserver.NewGRPCHandler(ms))
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	// 5. Create FileServer and configure TLS credentials
	fs, err := NewFileServer("dvfs1", tempDir, listener.Addr().String())
	if err != nil {
		t.Fatalf("NewFileServer failed: %v", err)
	}
	fs.SetTLSCredentials(clientCertPath, clientKeyPath, caPath)

	// 6. Register with MetaServer over mTLS
	if err := fs.RegisterWithMetaServer("127.0.0.1:50052"); err != nil {
		t.Fatalf("RegisterWithMetaServer failed over mTLS: %v", err)
	}
}
