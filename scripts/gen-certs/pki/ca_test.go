package pki

import (
	"crypto/rsa"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateRootCADefaults(t *testing.T) {
	opts := DefaultRootCAOptions()
	ca, err := GenerateRootCA(opts)
	if err != nil {
		t.Fatalf("GenerateRootCA failed: %v", err)
	}

	if ca == nil {
		t.Fatal("expected non-nil RootCA")
	}

	cert := ca.Certificate
	if cert == nil {
		t.Fatal("expected non-nil Certificate")
	}

	// 1. Verify 10+ year validity
	validityDuration := cert.NotAfter.Sub(cert.NotBefore)
	expectedMinDuration := 10 * 365 * 24 * time.Hour
	if validityDuration < expectedMinDuration {
		t.Errorf("expected validity at least 10 years (%v), got %v", expectedMinDuration, validityDuration)
	}

	// 2. Verify Subject
	if cert.Subject.CommonName != DefaultCACommonName {
		t.Errorf("expected CommonName '%s', got '%s'", DefaultCACommonName, cert.Subject.CommonName)
	}
	if len(cert.Subject.Organization) == 0 || cert.Subject.Organization[0] != DefaultCAOrganization {
		t.Errorf("expected Organization '%s', got '%v'", DefaultCAOrganization, cert.Subject.Organization)
	}

	// 3. Verify Basic Constraints
	if !cert.IsCA {
		t.Error("expected IsCA to be true")
	}
	if !cert.BasicConstraintsValid {
		t.Error("expected BasicConstraintsValid to be true")
	}
	if cert.MaxPathLen != 1 {
		t.Errorf("expected MaxPathLen 1, got %d", cert.MaxPathLen)
	}

	// 4. Verify Key Usages
	expectedKeyUsage := x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature
	if (cert.KeyUsage & expectedKeyUsage) != expectedKeyUsage {
		t.Errorf("expected key usage %v, got %v", expectedKeyUsage, cert.KeyUsage)
	}

	// 5. Verify RSA 4096-bit key
	rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("expected RSA public key, got %T", cert.PublicKey)
	}
	keyBits := rsaKey.N.BitLen()
	if keyBits != 4096 {
		t.Errorf("expected 4096-bit key, got %d", keyBits)
	}

	// 6. Verify Self-Signed Signature
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Errorf("self-signed signature verification failed: %v", err)
	}

	// 7. Verify Random Non-Zero Serial Number
	if cert.SerialNumber == nil || cert.SerialNumber.Sign() <= 0 {
		t.Errorf("expected positive random serial number, got %v", cert.SerialNumber)
	}

	// 8. Verify Subject Key Identifier
	if len(cert.SubjectKeyId) == 0 {
		t.Error("expected non-empty SubjectKeyId")
	}
}

func TestSaveAndLoadRootCA(t *testing.T) {
	tempDir := t.TempDir()

	ca, err := GenerateRootCA(DefaultRootCAOptions())
	if err != nil {
		t.Fatalf("GenerateRootCA failed: %v", err)
	}

	if err := ca.Save(tempDir); err != nil {
		t.Fatalf("ca.Save failed: %v", err)
	}

	certPath := filepath.Join(tempDir, "ca.crt")
	keyPath := filepath.Join(tempDir, "ca.key")

	// Ensure files exist on disk
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("ca.crt missing: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("ca.key missing: %v", err)
	}

	// Load CA with key
	loadedCA, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA failed: %v", err)
	}

	if loadedCA.Certificate.SerialNumber.Cmp(ca.Certificate.SerialNumber) != 0 {
		t.Errorf("serial numbers do not match after loading")
	}
	if loadedCA.PrivateKey.N.Cmp(ca.PrivateKey.N) != 0 {
		t.Errorf("private keys do not match after loading")
	}

	// Load Public Cert only
	pubCert, pemBytes, err := LoadCACert(certPath)
	if err != nil {
		t.Fatalf("LoadCACert failed: %v", err)
	}
	if pubCert.Subject.CommonName != ca.Certificate.Subject.CommonName {
		t.Errorf("expected CN '%s', got '%s'", ca.Certificate.Subject.CommonName, pubCert.Subject.CommonName)
	}
	if len(pemBytes) == 0 {
		t.Error("expected non-empty PEM bytes from LoadCACert")
	}
}
