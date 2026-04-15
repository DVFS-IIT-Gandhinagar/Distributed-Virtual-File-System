package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultCertDir = "internal/certs"
)

func resolvePath(envKey, defaultFile string) string {
	if fromEnv := os.Getenv(envKey); fromEnv != "" {
		return fromEnv
	}

	if dir := os.Getenv("DVFS_CERT_DIR"); dir != "" {
		return filepath.Join(dir, defaultFile)
	}

	return filepath.Join(defaultCertDir, defaultFile)
}

func CAPath() string {
	return resolvePath("DVFS_CA_CERT_FILE", "ca.crt")
}

func CAKeyPath() string {
	return resolvePath("DVFS_CA_KEY_FILE", "ca.key")
}

func ServerCertPath() string {
	return resolvePath("DVFS_SERVER_CERT_FILE", "server.crt")
}

func ServerKeyPath() string {
	return resolvePath("DVFS_SERVER_KEY_FILE", "server.key")
}

func LoadCACertPEM() ([]byte, error) {
	caPath := CAPath()
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate from %s: %w", caPath, err)
	}
	return caPEM, nil
}

func NewCAPool() (*x509.CertPool, error) {
	caPEM, err := LoadCACertPEM()
	if err != nil {
		return nil, err
	}

	cp := x509.NewCertPool()
	if !cp.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to append CA certificate from %s", CAPath())
	}

	return cp, nil
}

func LoadServerTLSCert() (tls.Certificate, error) {
	certPath := ServerCertPath()
	keyPath := ServerKeyPath()
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load TLS key pair cert=%s key=%s: %w", certPath, keyPath, err)
	}

	return tlsCert, nil
}
