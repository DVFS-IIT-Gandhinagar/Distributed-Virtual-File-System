package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/scripts/gen-certs/pki"
)

func main() {
	hostName := "localhost"
	if len(os.Args) > 1 {
		hostName = os.Args[1]
	}

	certsDir := "certs"
	caCertPath := filepath.Join(certsDir, "ca.crt")
	caKeyPath := filepath.Join(certsDir, "ca.key")

	// 1. Load or Generate Root CA
	var ca *pki.RootCA
	var err error

	if _, errCert := os.Stat(caCertPath); errCert == nil {
		if _, errKey := os.Stat(caKeyPath); errKey == nil {
			ca, err = pki.LoadCA(caCertPath, caKeyPath)
			if err != nil {
				log.Printf("[WARN] Existing CA could not be loaded (%v), generating fresh CA", err)
				ca = nil
			} else {
				log.Printf("Loaded existing Root CA from %s (Subject: %s, Valid until: %s)",
					caCertPath, ca.Certificate.Subject.CommonName, ca.Certificate.NotAfter.Format("2006-01-02"))
			}
		}
	}

	if ca == nil {
		log.Printf("Generating new 10+ year Root CA...")
		ca, err = pki.GenerateRootCA(pki.DefaultRootCAOptions())
		if err != nil {
			log.Fatalf("Failed to generate Root CA: %v", err)
		}
		if err := ca.Save(certsDir); err != nil {
			log.Fatalf("Failed to save Root CA: %v", err)
		}
		log.Printf("Root CA saved to %s", certsDir)
	}

	if hostName == "ca" || hostName == "--ca-only" {
		log.Printf("CA-only mode: Root CA generation completed.")
		return
	}

	// 2. Generate Server Certificate signed by Root CA
	serverSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		serverSerial = big.NewInt(time.Now().UnixNano())
	}

	cert := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject: pkix.Name{
			Organization: []string{"DVFS Project"},
			CommonName:   hostName,
		},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     time.Now().AddDate(2, 0, 0), // 2-year validity for server cert
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	if ip := net.ParseIP(hostName); ip != nil {
		cert.IPAddresses = append(cert.IPAddresses, ip)
	} else {
		cert.DNSNames = append(cert.DNSNames, hostName)
	}

	// Always include localhost and 'server' (for Docker) for convenience
	cert.DNSNames = append(cert.DNSNames, "localhost", "server")
	cert.IPAddresses = append(cert.IPAddresses, net.IPv4(127, 0, 0, 1))

	// Auto-include the machine's outbound LAN IP so remote clients work without extra flags
	if lanIP := getOutboundIP(); lanIP != "127.0.0.1" {
		if parsed := net.ParseIP(lanIP); parsed != nil {
			cert.IPAddresses = append(cert.IPAddresses, parsed)
			log.Printf("Including LAN IP in cert SANs: %s", lanIP)
		}
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatalf("Failed to generate server key: %v", err)
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca.Certificate, &certPrivKey.PublicKey, ca.PrivateKey)
	if err != nil {
		log.Fatalf("Failed to sign server certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	certPrivKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})

	serverCertPath := filepath.Join(certsDir, "server.crt")
	serverKeyPath := filepath.Join(certsDir, "server.key")

	if err := os.WriteFile(serverCertPath, certPEM, 0644); err != nil {
		log.Fatalf("Failed to write server cert: %v", err)
	}
	if err := os.WriteFile(serverKeyPath, certPrivKeyPEM, 0600); err != nil {
		log.Fatalf("Failed to write server key: %v", err)
	}

	fmt.Printf("Certificates generated successfully for %s (signed by '%s')\n",
		hostName, ca.Certificate.Subject.CommonName)
}

// getOutboundIP returns the machine's preferred outbound IP.
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
