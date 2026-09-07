package client

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestNewDVFSUniversalCertPool(t *testing.T) {
	pool, err := NewDVFSUniversalCertPool()
	if err != nil {
		t.Fatalf("NewDVFSUniversalCertPool() failed: %v", err)
	}
	if pool == nil {
		t.Fatal("expected non-nil CertPool")
	}
}

func TestDVFSUniversalCertPool_Isolation(t *testing.T) {
	pool, err := NewDVFSUniversalCertPool()
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// 1. Generate a rogue/external CA key pair
	rogueKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rogue key: %v", err)
	}

	rogueTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(9999),
		Subject: pkix.Name{
			CommonName: "Rogue Unauthorized CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	rogueCACertBytes, err := x509.CreateCertificate(rand.Reader, rogueTemplate, rogueTemplate, &rogueKey.PublicKey, rogueKey)
	if err != nil {
		t.Fatalf("failed to create rogue CA cert: %v", err)
	}
	rogueCACert, err := x509.ParseCertificate(rogueCACertBytes)
	if err != nil {
		t.Fatalf("failed to parse rogue CA cert: %v", err)
	}

	// 2. Issue a leaf certificate from the rogue CA pretending to be dvfs1
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate leaf key: %v", err)
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1001),
		Subject: pkix.Name{
			CommonName: "dvfs1",
		},
		DNSNames:  []string{"dvfs1"},
		NotBefore: time.Now().Add(-10 * time.Minute),
		NotAfter:  time.Now().Add(2 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	leafBytes, err := x509.CreateCertificate(rand.Reader, leafTemplate, rogueCACert, &leafKey.PublicKey, rogueKey)
	if err != nil {
		t.Fatalf("failed to create rogue leaf cert: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leafBytes)
	if err != nil {
		t.Fatalf("failed to parse leaf cert: %v", err)
	}

	// 3. Verify against DVFS Universal CertPool - MUST FAIL
	opts := x509.VerifyOptions{
		DNSName: "dvfs1",
		Roots:   pool,
	}
	if _, err := leafCert.Verify(opts); err == nil {
		t.Fatal("SECURITY ERROR: Rogue certificate was verified by DVFS universal cert pool! System must reject non-DVFS certificates.")
	} else {
		t.Logf("Expected rejection of rogue certificate: %v", err)
	}
}

func TestDVFSUniversalCertPool_ValidNodeVerification(t *testing.T) {
	pool, err := NewDVFSUniversalCertPool()
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Decode RootCAPEM to verify the pool contains it
	block, _ := pem.Decode([]byte(RootCAPEM))
	if block == nil {
		t.Fatal("failed to decode RootCAPEM")
	}
	rootCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse Root CA cert: %v", err)
	}

	// Verify the root cert against itself in pool
	opts := x509.VerifyOptions{
		Roots: pool,
	}
	if _, err := rootCert.Verify(opts); err != nil {
		t.Fatalf("Root CA certificate self-verification against pool failed: %v", err)
	}
}
