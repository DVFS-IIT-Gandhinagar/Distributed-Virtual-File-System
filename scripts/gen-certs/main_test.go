package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGetOutboundIPReturnsValue(t *testing.T) {
	ip := getOutboundIP()
	if ip == "" {
		t.Fatalf("expected non-empty outbound IP")
	}
}

func TestMainGeneratesCertificateArtifacts(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	originalArgs := os.Args

	tempWD := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempWD, "certs"), 0755); err != nil {
		t.Fatalf("failed to create temp cert dir: %v", err)
	}

	if err := os.Chdir(tempWD); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalWD)
		os.Args = originalArgs
	}()

	os.Args = []string{"gen-certs", "localhost"}
	main()

	paths := []string{
		filepath.Join("certs", "ca.crt"),
		filepath.Join("certs", "ca.key"),
		filepath.Join("certs", "server.crt"),
		filepath.Join("certs", "server.key"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected generated file %s to exist: %v", p, err)
		}
		if len(data) == 0 {
			t.Fatalf("expected generated file %s to be non-empty", p)
		}
	}

	serverCertPEM, err := os.ReadFile(filepath.Join("certs", "server.crt"))
	if err != nil {
		t.Fatalf("failed to read server cert: %v", err)
	}
	block, _ := pem.Decode(serverCertPEM)
	if block == nil {
		t.Fatalf("failed to decode PEM block from generated server certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Fatalf("generated server certificate is invalid: %v", err)
	}
}
