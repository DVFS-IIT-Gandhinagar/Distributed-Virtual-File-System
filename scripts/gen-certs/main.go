package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	hostName := "localhost"
	if len(os.Args) > 1 {
		hostName = os.Args[1]
	}

	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2026),
		Subject: pkix.Name{
			Organization: []string{"DVFS project"},
			CommonName:   "DVFS CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatal(err)
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		log.Fatal(err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	caPrivKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivKey),
	})

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2027),
		Subject: pkix.Name{
			Organization: []string{"DVFS project"},
			CommonName:   hostName,
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
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
		cert.IPAddresses = append(cert.IPAddresses, net.ParseIP(lanIP))
		log.Printf("Including LAN IP in cert SANs: %s", lanIP)
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatal(err)
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)
	if err != nil {
		log.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	certPrivKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})

	_ = os.WriteFile("internal/certs/ca.crt", caPEM, 0644)
	_ = os.WriteFile("internal/certs/ca.key", caPrivKeyPEM, 0644)
	_ = os.WriteFile("internal/certs/server.crt", certPEM, 0644)
	_ = os.WriteFile("internal/certs/server.key", certPrivKeyPEM, 0644)

	log.Printf("Certificates generated successfully for %s (including 'server' and 'localhost')\n", hostName)
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
