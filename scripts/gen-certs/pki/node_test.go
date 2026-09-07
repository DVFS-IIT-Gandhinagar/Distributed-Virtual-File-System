package pki

import (
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateNodeCert(t *testing.T) {
	// 1. Setup in-memory Root CA
	ca, err := GenerateRootCA(DefaultRootCAOptions())
	if err != nil {
		t.Fatalf("GenerateRootCA failed: %v", err)
	}

	// 2. Generate node cert for dvfs1 with extra LAN IP
	opts := DefaultNodeCertOptions("dvfs1")
	opts.ExtraIPs = append(opts.ExtraIPs, net.ParseIP("10.0.171.38"))
	nodeCert, err := GenerateNodeCert(ca, opts)
	if err != nil {
		t.Fatalf("GenerateNodeCert failed: %v", err)
	}

	cert := nodeCert.Certificate
	if cert == nil {
		t.Fatal("expected non-nil node certificate")
	}

	// Verify Subject Common Name
	if cert.Subject.CommonName != "dvfs1" {
		t.Errorf("expected CN 'dvfs1', got '%s'", cert.Subject.CommonName)
	}

	// Verify not a CA
	if cert.IsCA {
		t.Error("expected node cert IsCA to be false")
	}

	// Verify 2-year validity
	validity := cert.NotAfter.Sub(cert.NotBefore)
	expectedValidity := time.Duration(DefaultNodeValidityDays) * 24 * time.Hour
	if validity < expectedValidity-time.Hour || validity > expectedValidity+time.Hour {
		t.Errorf("unexpected validity duration: %v", validity)
	}

	// Verify DNS SANs
	expectedDNS := []string{"dvfs1", "dvfs1.local", "dvfs1.dvfs.cluster", "localhost"}
	for _, exp := range expectedDNS {
		found := false
		for _, dns := range cert.DNSNames {
			if dns == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected DNS SAN: %s", exp)
		}
	}

	// Verify IP SANs
	foundLoopback := false
	foundLAN := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			foundLoopback = true
		}
		if ip.Equal(net.ParseIP("10.0.171.38")) {
			foundLAN = true
		}
	}
	if !foundLoopback {
		t.Error("missing 127.0.0.1 IP SAN")
	}
	if !foundLAN {
		t.Error("missing 10.0.171.38 IP SAN")
	}

	// Verify Chaining against Root CA
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	verifyOpts := x509.VerifyOptions{
		Roots:   roots,
		DNSName: "dvfs1",
	}
	if _, err := cert.Verify(verifyOpts); err != nil {
		t.Errorf("certificate failed verification against Root CA for 'dvfs1': %v", err)
	}

	// Verify verification fails for unlisted name
	badOpts := x509.VerifyOptions{
		Roots:   roots,
		DNSName: "malicious.attacker.com",
	}
	if _, err := cert.Verify(badOpts); err == nil {
		t.Error("expected verification to fail for unlisted domain, got nil")
	}
}

func TestSaveNodeCert(t *testing.T) {
	tempDir := t.TempDir()
	ca, err := GenerateRootCA(DefaultRootCAOptions())
	if err != nil {
		t.Fatalf("GenerateRootCA failed: %v", err)
	}

	nodeCert, err := GenerateNodeCert(ca, DefaultNodeCertOptions("dvfs2"))
	if err != nil {
		t.Fatalf("GenerateNodeCert failed: %v", err)
	}

	nodeDir := filepath.Join(tempDir, "dvfs2")
	if err := nodeCert.Save(nodeDir); err != nil {
		t.Fatalf("nodeCert.Save failed: %v", err)
	}

	certPath := filepath.Join(nodeDir, "server.crt")
	keyPath := filepath.Join(nodeDir, "server.key")

	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("server.crt was not created: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("server.key was not created: %v", err)
	}
}
