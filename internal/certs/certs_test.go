package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeTestCACertPEM(t *testing.T, commonName string) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"DVFS Test"}},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if pemBytes == nil {
		t.Fatalf("failed to encode certificate PEM")
	}

	return pemBytes
}

func TestNewCAPoolLoadsPrimaryAndBundle(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "ca.crt")
	bundlePath := filepath.Join(tempDir, "bundle.crt")

	if err := os.WriteFile(primaryPath, makeTestCACertPEM(t, "primary-ca"), 0644); err != nil {
		t.Fatalf("failed to write primary CA: %v", err)
	}
	if err := os.WriteFile(bundlePath, makeTestCACertPEM(t, "bundle-ca"), 0644); err != nil {
		t.Fatalf("failed to write bundle CA: %v", err)
	}

	t.Setenv("DVFS_CA_CERT_FILE", primaryPath)
	t.Setenv("DVFS_CA_BUNDLE_FILE", bundlePath)

	pool, err := NewCAPool()
	if err != nil {
		t.Fatalf("NewCAPool failed: %v", err)
	}

	if got := len(pool.Subjects()); got < 2 {
		t.Fatalf("expected at least 2 trust roots in pool, got=%d", got)
	}
}

func TestNewCAPoolFailsWhenBundlePathConfiguredButMissing(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "ca.crt")
	if err := os.WriteFile(primaryPath, makeTestCACertPEM(t, "primary-ca"), 0644); err != nil {
		t.Fatalf("failed to write primary CA: %v", err)
	}

	t.Setenv("DVFS_CA_CERT_FILE", primaryPath)
	t.Setenv("DVFS_CA_BUNDLE_FILE", filepath.Join(tempDir, "missing-bundle.crt"))

	if _, err := NewCAPool(); err == nil {
		t.Fatalf("expected NewCAPool to fail when configured bundle file is missing")
	}
}
