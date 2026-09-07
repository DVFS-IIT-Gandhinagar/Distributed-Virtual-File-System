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
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultNodeValidityDays = 730 // 2 years
	DefaultNodeKeyBits      = 4096
)

// NodeCertOptions configures the generation of a node certificate.
type NodeCertOptions struct {
	NodeName     string
	Organization string
	ValidityDays int
	KeyBits      int
	ExtraDNS     []string
	ExtraIPs     []net.IP
}

// NodeCertificate holds in-memory artifacts for a signed node certificate.
type NodeCertificate struct {
	NodeName    string
	Certificate *x509.Certificate
	PrivateKey  *rsa.PrivateKey
	CertPEM     []byte
	KeyPEM      []byte
}

// DefaultNodeCertOptions returns production options with standard SANs for a cluster node.
func DefaultNodeCertOptions(nodeName string) NodeCertOptions {
	return NodeCertOptions{
		NodeName:     nodeName,
		Organization: DefaultCAOrganization,
		ValidityDays: DefaultNodeValidityDays,
		KeyBits:      DefaultNodeKeyBits,
		ExtraDNS: []string{
			nodeName,
			nodeName + ".local",
			nodeName + ".dvfs.cluster",
			"localhost",
		},
		ExtraIPs: []net.IP{
			net.IPv4(127, 0, 0, 1),
		},
	}
}

// GenerateNodeCert generates an RSA private key and a leaf certificate signed by the Root CA.
func GenerateNodeCert(ca *RootCA, opts NodeCertOptions) (*NodeCertificate, error) {
	if ca == nil || ca.Certificate == nil || ca.PrivateKey == nil {
		return nil, fmt.Errorf("invalid Root CA: certificate and private key are required")
	}
	if opts.NodeName == "" {
		return nil, fmt.Errorf("node name cannot be empty")
	}
	if opts.ValidityDays <= 0 {
		opts.ValidityDays = DefaultNodeValidityDays
	}
	if opts.KeyBits <= 0 {
		opts.KeyBits = DefaultNodeKeyBits
	}

	// 1. Generate node private key
	privKey, err := rsa.GenerateKey(rand.Reader, opts.KeyBits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate %d-bit RSA key for node %s: %w", opts.KeyBits, opts.NodeName, err)
	}

	// 2. Serial number with 128-bit entropy
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// 3. Compute Subject Key Identifier for leaf cert
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	ski := sha1.Sum(pubDER)

	notBefore := time.Now().UTC().Add(-5 * time.Minute)
	notAfter := notBefore.Add(time.Duration(opts.ValidityDays) * 24 * time.Hour)

	// Collect unique DNS SANs
	dnsMap := make(map[string]bool)
	dnsMap[opts.NodeName] = true
	dnsMap[opts.NodeName+".local"] = true
	dnsMap[opts.NodeName+".dvfs.cluster"] = true
	dnsMap["localhost"] = true
	for _, dns := range opts.ExtraDNS {
		if dns != "" {
			dnsMap[dns] = true
		}
	}
	dnsNames := make([]string, 0, len(dnsMap))
	for dns := range dnsMap {
		dnsNames = append(dnsNames, dns)
	}

	// Collect unique IP SANs
	ipMap := make(map[string]net.IP)
	ipMap["127.0.0.1"] = net.IPv4(127, 0, 0, 1)
	for _, ip := range opts.ExtraIPs {
		if ip != nil {
			ipMap[ip.String()] = ip
		}
	}
	ips := make([]net.IP, 0, len(ipMap))
	for _, ip := range ipMap {
		ips = append(ips, ip)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   opts.NodeName,
			Organization: []string{opts.Organization},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  false,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
		SubjectKeyId: ski[:],
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}

	// 4. Sign leaf certificate with Root CA
	certBytes, err := x509.CreateCertificate(rand.Reader, template, ca.Certificate, &privKey.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign node certificate for %s: %w", opts.NodeName, err)
	}

	parsedCert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse signed node certificate: %w", err)
	}

	// 5. Immediate cryptographic validation check against CA
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate)
	verifyOpts := x509.VerifyOptions{
		Roots:   roots,
		DNSName: opts.NodeName,
	}
	if _, err := parsedCert.Verify(verifyOpts); err != nil {
		return nil, fmt.Errorf("immediate self-verification failed for node %s: %w", opts.NodeName, err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	return &NodeCertificate{
		NodeName:    opts.NodeName,
		Certificate: parsedCert,
		PrivateKey:  privKey,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	}, nil
}

// Save writes server.crt and server.key into the specified directory.
// server.key is written with 0600 permissions.
func (nc *NodeCertificate) Save(nodeDir string) error {
	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return fmt.Errorf("failed to create node directory %s: %w", nodeDir, err)
	}

	certPath := filepath.Join(nodeDir, "server.crt")
	keyPath := filepath.Join(nodeDir, "server.key")

	if err := os.WriteFile(certPath, nc.CertPEM, 0644); err != nil {
		return fmt.Errorf("failed to write server cert to %s: %w", certPath, err)
	}

	if err := os.WriteFile(keyPath, nc.KeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write server key to %s: %w", keyPath, err)
	}

	return nil
}
