package pki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRootCAConfig defines standard parameters for the DVFS Root CA.
const (
	DefaultCACommonName   = "DVFS Offline Root CA"
	DefaultCAOrganization = "DVFS Project"
	DefaultCAOU           = "Security"
	DefaultCACountry       = "IN"
	DefaultCAProvince     = "Gujarat"
	DefaultCALocality     = "Gandhinagar"
	DefaultCAValidityDays = 3652 // 10 years + 2 leap days
	DefaultCAKeyBits      = 4096
)

// RootCAOptions configures generation of the Root CA.
type RootCAOptions struct {
	CommonName   string
	Organization string
	OU           string
	Country      string
	Province     string
	Locality     string
	ValidityDays int
	KeyBits      int
}

// RootCA holds the generated in-memory certificate, private key, and PEM encodings.
type RootCA struct {
	Certificate *x509.Certificate
	PrivateKey  *rsa.PrivateKey
	CertPEM     []byte
	KeyPEM      []byte
}

// DefaultRootCAOptions returns standard production-grade options for the Root CA.
func DefaultRootCAOptions() RootCAOptions {
	return RootCAOptions{
		CommonName:   DefaultCACommonName,
		Organization: DefaultCAOrganization,
		OU:           DefaultCAOU,
		Country:      DefaultCACountry,
		Province:     DefaultCAProvince,
		Locality:     DefaultCALocality,
		ValidityDays: DefaultCAValidityDays,
		KeyBits:      DefaultCAKeyBits,
	}
}

// GenerateRootCA creates a new self-signed Root CA with a 10+ year validity period.
// The private key must be kept offline and never copied to server or client devices.
func GenerateRootCA(opts RootCAOptions) (*RootCA, error) {
	if opts.CommonName == "" {
		opts.CommonName = DefaultCACommonName
	}
	if opts.Organization == "" {
		opts.Organization = DefaultCAOrganization
	}
	if opts.ValidityDays <= 0 {
		opts.ValidityDays = DefaultCAValidityDays
	}
	if opts.KeyBits <= 0 {
		opts.KeyBits = DefaultCAKeyBits
	}

	// 1. Generate 4096-bit RSA Private Key
	privKey, err := rsa.GenerateKey(rand.Reader, opts.KeyBits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate %d-bit RSA key: %w", opts.KeyBits, err)
	}

	// 2. Generate RFC 5280 compliant random positive 128-bit serial number
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random serial number: %w", err)
	}

	// 3. Compute Subject Key Identifier (RFC 7093 SHA-1 of public key DER)
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	ski := sha1.Sum(pubDER)

	notBefore := time.Now().UTC().Add(-5 * time.Minute) // buffer for clock drift
	notAfter := notBefore.Add(time.Duration(opts.ValidityDays) * 24 * time.Hour)

	subject := pkix.Name{
		CommonName:   opts.CommonName,
		Organization: []string{opts.Organization},
	}
	if opts.OU != "" {
		subject.OrganizationalUnit = []string{opts.OU}
	}
	if opts.Country != "" {
		subject.Country = []string{opts.Country}
	}
	if opts.Province != "" {
		subject.Province = []string{opts.Province}
	}
	if opts.Locality != "" {
		subject.Locality = []string{opts.Locality}
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		SubjectKeyId: ski[:],
	}

	// 4. Self-sign the Root CA certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create self-signed CA certificate: %w", err)
	}

	// Parse back to confirm validity
	parsedCert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse generated certificate: %w", err)
	}

	// 5. Encode to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	return &RootCA{
		Certificate: parsedCert,
		PrivateKey:  privKey,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	}, nil
}

// Save writes ca.crt and ca.key to the target directory.
// ca.key is set to restrictive permissions (0400).
func (ca *RootCA) Save(outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outDir, err)
	}

	certPath := filepath.Join(outDir, "ca.crt")
	keyPath := filepath.Join(outDir, "ca.key")

	if err := os.WriteFile(certPath, ca.CertPEM, 0644); err != nil {
		return fmt.Errorf("failed to write CA certificate to %s: %w", certPath, err)
	}

	// 0400 ensures read-only for owner (or 0600)
	if err := os.WriteFile(keyPath, ca.KeyPEM, 0400); err != nil {
		return fmt.Errorf("failed to write CA private key to %s: %w", keyPath, err)
	}

	return nil
}

// LoadCA loads an existing Root CA certificate and private key from disk.
func LoadCA(certPath, keyPath string) (*RootCA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate from %s: %w", certPath, err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("failed to decode valid PEM certificate from %s", certPath)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA private key from %s: %w", keyPath, err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode valid PEM private key from %s", keyPath)
	}

	var privKey *rsa.PrivateKey
	if keyBlock.Type == "RSA PRIVATE KEY" {
		privKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS#1 private key: %w", err)
		}
	} else if keyBlock.Type == "PRIVATE KEY" {
		parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS#8 private key: %w", err)
		}
		var ok bool
		privKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key in %s is not an RSA private key", keyPath)
		}
	} else {
		return nil, fmt.Errorf("unsupported private key PEM type: %s", keyBlock.Type)
	}

	return &RootCA{
		Certificate: cert,
		PrivateKey:  privKey,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	}, nil
}

// LoadCACert reads and parses only the public CA certificate without requiring the private key.
func LoadCACert(certPath string) (*x509.Certificate, []byte, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read CA cert %s: %w", certPath, err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("failed to decode PEM certificate from %s", certPath)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, certPEM, nil
}

// SyncClientCA finds the repository root and automatically updates
// internal/client/ca.go with the provided certPEM.
func SyncClientCA(certPEM []byte) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		// Running in a temporary directory or test environment without go.mod; skip quietly
		return nil
	}

	targetFile := filepath.Join(repoRoot, "internal", "client", "ca.go")
	content := fmt.Sprintf(`package client

import (
	"crypto/x509"
	"fmt"
)

// RootCAPEM contains the public certificate of the 10-year offline DVFS Root CA.
// Embedded directly into the client binary; no external ca_cert file required.
// Automatically updated whenever Root CA is generated.
const RootCAPEM = %s

// NewDVFSUniversalCertPool creates an isolated x509.CertPool containing strictly
// the DVFS Root CA, completely ignoring the host operating system's root store.
func NewDVFSUniversalCertPool() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(RootCAPEM)) {
		return nil, fmt.Errorf("failed to append hardcoded DVFS Root CA certificate")
	}
	return pool, nil
}
`, "`"+strings.TrimSpace(string(certPEM))+"`")

	return os.WriteFile(targetFile, []byte(content), 0644)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repository root (go.mod) not found")
}
