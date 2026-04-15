package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	caCertValidityYears      = 10
	serverCertValidityDays   = 365
	defaultFSCertValidityHrs = 24
)

func EnsureMetaServerPKI(serverIP string) error {
	if net.ParseIP(serverIP) == nil {
		return fmt.Errorf("invalid metaserver public IP: %s", serverIP)
	}

	caCert, caKey, err := ensureCA()
	if err != nil {
		return err
	}

	if err := ensureServerCertForIP(serverIP, caCert, caKey); err != nil {
		return err
	}

	return nil
}

func SignFileServerCSR(csrPEM []byte, fileserverID, address string, validFor time.Duration) ([]byte, string, time.Time, error) {
	if len(csrPEM) == 0 {
		return nil, "", time.Time{}, errors.New("empty CSR")
	}
	if fileserverID == "" {
		return nil, "", time.Time{}, errors.New("fileserver_id is required")
	}
	if address == "" {
		return nil, "", time.Time{}, errors.New("address is required")
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return nil, "", time.Time{}, fmt.Errorf("address must contain an IP host for strict IP identity: %s", address)
	}

	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, "", time.Time{}, errors.New("failed to decode CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("failed to parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", time.Time{}, fmt.Errorf("CSR signature check failed: %w", err)
	}

	caCert, caKey, err := ensureCA()
	if err != nil {
		return nil, "", time.Time{}, err
	}

	if validFor <= 0 {
		validFor = defaultFSCertValidityHrs * time.Hour
	}
	if validFor > 7*24*time.Hour {
		validFor = 7 * 24 * time.Hour
	}

	notBefore := time.Now().Add(-2 * time.Minute)
	notAfter := notBefore.Add(validFor)
	serial, err := randomSerialNumber()
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   fileserverID,
			Organization: []string{"DVFS FileServer"},
		},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		IPAddresses: []net.IP{ip},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("failed to sign file server certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if certPEM == nil {
		return nil, "", time.Time{}, errors.New("failed to encode signed certificate PEM")
	}

	return certPEM, serial.Text(16), notAfter, nil
}

func ensureCA() (*x509.Certificate, *rsa.PrivateKey, error) {
	caCertPath := CAPath()
	caKeyPath := CAKeyPath()

	certBytes, certErr := os.ReadFile(caCertPath)
	keyBytes, keyErr := os.ReadFile(caKeyPath)
	if certErr == nil && keyErr == nil {
		cert, err := parseCertPEM(certBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid CA certificate at %s: %w", caCertPath, err)
		}
		key, err := parseRSAPrivateKeyPEM(keyBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid CA private key at %s: %w", caKeyPath, err)
		}
		return cert, key, nil
	}

	if certErr == nil || keyErr == nil {
		return nil, nil, fmt.Errorf("incomplete CA material: cert=%s key=%s", caCertPath, caKeyPath)
	}

	if !errors.Is(certErr, os.ErrNotExist) || !errors.Is(keyErr, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("failed to read CA material: certErr=%v keyErr=%v", certErr, keyErr)
	}

	if err := ensureParentDir(caCertPath); err != nil {
		return nil, nil, err
	}
	if err := ensureParentDir(caKeyPath); err != nil {
		return nil, nil, err
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA private key: %w", err)
	}

	now := time.Now()
	serial, err := randomSerialNumber()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate CA serial number: %w", err)
	}

	caTpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "DVFS MetaServer Root CA",
			Organization: []string{"DVFS"},
		},
		NotBefore:             now.Add(-10 * time.Minute),
		NotAfter:              now.AddDate(caCertValidityYears, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLenZero:        true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to self-sign CA certificate: %w", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if caCertPEM == nil {
		return nil, nil, errors.New("failed to encode CA cert PEM")
	}
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)})
	if caKeyPEM == nil {
		return nil, nil, errors.New("failed to encode CA key PEM")
	}

	if err := os.WriteFile(caCertPath, caCertPEM, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA cert to %s: %w", caCertPath, err)
	}
	if err := os.WriteFile(caKeyPath, caKeyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA key to %s: %w", caKeyPath, err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse generated CA cert: %w", err)
	}

	return caCert, caKey, nil
}

func ensureServerCertForIP(serverIP string, caCert *x509.Certificate, caKey *rsa.PrivateKey) error {
	certPath := ServerCertPath()
	keyPath := ServerKeyPath()

	if certBytes, certErr := os.ReadFile(certPath); certErr == nil {
		if keyBytes, keyErr := os.ReadFile(keyPath); keyErr == nil {
			cert, parseCertErr := parseCertPEM(certBytes)
			if parseCertErr == nil {
				if isCertValidForIPAndCA(cert, serverIP, caCert) {
					if _, parseKeyErr := parseRSAPrivateKeyPEM(keyBytes); parseKeyErr == nil {
						return nil
					}
				}
			}
		}
	}

	if err := ensureParentDir(certPath); err != nil {
		return err
	}
	if err := ensureParentDir(keyPath); err != nil {
		return err
	}

	srvKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate metaserver key: %w", err)
	}

	now := time.Now()
	serial, err := randomSerialNumber()
	if err != nil {
		return fmt.Errorf("failed to generate metaserver serial number: %w", err)
	}

	ip := net.ParseIP(serverIP)
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   serverIP,
			Organization: []string{"DVFS MetaServer"},
		},
		NotBefore:    now.Add(-10 * time.Minute),
		NotAfter:     now.AddDate(0, 0, serverCertValidityDays),
		IPAddresses:  []net.IP{ip},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		SubjectKeyId: []byte{1, 1, 2, 3, 5, 8},
	}

	srvDER, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("failed to sign metaserver cert: %w", err)
	}

	srvCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	srvKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(srvKey)})
	if srvCertPEM == nil || srvKeyPEM == nil {
		return errors.New("failed to encode metaserver cert material")
	}

	if err := writeFileAtomically(certPath, srvCertPEM, 0644); err != nil {
		return fmt.Errorf("failed to write metaserver cert to %s: %w", certPath, err)
	}
	if err := writeFileAtomically(keyPath, srvKeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write metaserver key to %s: %w", keyPath, err)
	}

	return nil
}

func GenerateAndStoreServerCSR(commonName, address string) ([]byte, error) {
	if commonName == "" {
		return nil, errors.New("commonName is required")
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return nil, fmt.Errorf("address must contain an IP host for strict IP identity: %s", address)
	}

	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server private key: %w", err)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"DVFS FileServer"},
		},
		IPAddresses: []net.IP{ip},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSR: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	if keyPEM == nil || csrPEM == nil {
		return nil, errors.New("failed to encode key or CSR PEM")
	}

	if err := ensureParentDir(ServerKeyPath()); err != nil {
		return nil, err
	}
	if err := writeFileAtomically(ServerKeyPath(), keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("failed to write server private key to %s: %w", ServerKeyPath(), err)
	}

	return csrPEM, nil
}

func StoreServerCertificatePEM(certPEM []byte) error {
	if len(certPEM) == 0 {
		return errors.New("empty certificate PEM")
	}

	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return fmt.Errorf("invalid certificate PEM: %w", err)
	}
	if time.Now().After(cert.NotAfter) {
		return errors.New("refusing to store expired server certificate")
	}

	if err := ensureParentDir(ServerCertPath()); err != nil {
		return err
	}
	if err := writeFileAtomically(ServerCertPath(), certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write server certificate to %s: %w", ServerCertPath(), err)
	}

	return nil
}

func ServerCertNeedsRenewalForAddress(address string, renewBefore time.Duration) (bool, string, error) {
	if renewBefore <= 0 {
		renewBefore = 6 * time.Hour
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.TrimSpace(host)
	ip := net.ParseIP(host)
	if ip == nil {
		return true, "invalid strict-IP host", nil
	}

	certPEM, err := os.ReadFile(ServerCertPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "server cert missing", nil
		}
		return false, "", fmt.Errorf("failed to read server certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(ServerKeyPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "server key missing", nil
		}
		return false, "", fmt.Errorf("failed to read server key: %w", err)
	}

	if _, err := parseRSAPrivateKeyPEM(keyPEM); err != nil {
		return true, "invalid server key", nil
	}
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return true, "invalid server cert", nil
	}

	now := time.Now()
	if now.After(cert.NotAfter) {
		return true, "server cert expired", nil
	}
	if cert.NotAfter.Sub(now) <= renewBefore {
		return true, "server cert expiring soon", nil
	}

	if err := cert.VerifyHostname(host); err != nil {
		return true, "server cert SAN mismatch", nil
	}

	caPEM, err := LoadCACertPEM()
	if err != nil {
		return true, "missing local CA", nil
	}
	caCert, err := parseCertPEM(caPEM)
	if err != nil {
		return true, "invalid local CA", nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return true, "server cert not signed by local CA", nil
	}

	return false, "", nil
}

func PersistCAIfMissing(caPEM []byte) error {
	if len(caPEM) == 0 {
		return nil
	}

	if _, err := os.Stat(CAPath()); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to stat local CA file: %w", err)
	}

	if err := ensureParentDir(CAPath()); err != nil {
		return err
	}
	if err := writeFileAtomically(CAPath(), caPEM, 0644); err != nil {
		return fmt.Errorf("failed to persist CA file at %s: %w", CAPath(), err)
	}

	return nil
}

func CertFingerprintSHA256(certPEM []byte) (string, error) {
	cert, err := parseCertPEM(certPEM)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(cert.Raw)
	return fmt.Sprintf("%x", sum[:]), nil
}

func CurrentServerCertFingerprintSHA256() (string, error) {
	certPEM, err := os.ReadFile(ServerCertPath())
	if err != nil {
		return "", err
	}
	return CertFingerprintSHA256(certPEM)
}

func IsCertificateFingerprintMatch(certPEM []byte, expectedHex string) (bool, error) {
	actual, err := CertFingerprintSHA256(certPEM)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(actual, strings.TrimSpace(expectedHex)), nil
}

func IsCAPemEqual(caA, caB []byte) bool {
	return bytes.Equal(bytes.TrimSpace(caA), bytes.TrimSpace(caB))
}

func isCertValidForIPAndCA(cert *x509.Certificate, ip string, caCert *x509.Certificate) bool {
	if cert == nil || caCert == nil {
		return false
	}
	if time.Now().After(cert.NotAfter) {
		return false
	}
	if err := cert.VerifyHostname(ip); err != nil {
		return false
	}

	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return false
	}

	return true
}

func parseCertPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("PEM decode failed")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseRSAPrivateKeyPEM(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("PEM decode failed")
	}

	if block.Type == "RSA PRIVATE KEY" {
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rsaKey, nil
}

func randomSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, err
	}
	if serial.Sign() == 0 {
		return big.NewInt(1), nil
	}
	return serial, nil
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return nil
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	if err := ensureParentDir(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "dvfs-tls-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	return nil
}
