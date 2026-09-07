package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/scripts/gen-certs/pki"
)

const defaultNodes = "dvfs1,dvfs2,dvfs3,dvfs4,dvfs5,dvfs6,dvfs7,dvfs8,dvfs9,localhost,fs1,mds"

func main() {
	caCertPath := flag.String("ca-cert", "certs/ca.crt", "Path to Root CA public certificate")
	caKeyPath := flag.String("ca-key", "certs/ca.key", "Path to Root CA private key (offline storage)")
	outDir := flag.String("out", "deploy_certs", "Directory where per-node bundles will be generated")
	nodeList := flag.String("nodes", defaultNodes, "Comma-separated list of node identities to generate")
	days := flag.Int("days", pki.DefaultNodeValidityDays, "Certificate validity in days")
	bits := flag.Int("bits", pki.DefaultNodeKeyBits, "RSA key size in bits")
	flag.Parse()

	log.Printf("=================================================================")
	log.Printf("       DVFS MULTI-NODE CLUSTER CERTIFICATE GENERATOR            ")
	log.Printf("=================================================================")
	log.Printf("Loading Root CA from: %s", *caCertPath)

	ca, err := pki.LoadCA(*caCertPath, *caKeyPath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load Root CA: %v", err)
	}

	log.Printf("Root CA loaded: Subject='%s', Valid until: %s",
		ca.Certificate.Subject.CommonName, ca.Certificate.NotAfter.Format("2006-01-02"))

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("[FATAL] Failed to create output directory %s: %v", *outDir, err)
	}

	// Copy Root CA public certificate to deploy_certs root for convenience
	rootCACopyPath := filepath.Join(*outDir, "ca.crt")
	if err := os.WriteFile(rootCACopyPath, ca.CertPEM, 0644); err != nil {
		log.Fatalf("[FATAL] Failed to copy Root CA public certificate: %v", err)
	}

	nodes := strings.Split(*nodeList, ",")
	successCount := 0

	for _, rawNode := range nodes {
		node := strings.TrimSpace(rawNode)
		if node == "" {
			continue
		}

		opts := pki.DefaultNodeCertOptions(node)
		opts.ValidityDays = *days
		opts.KeyBits = *bits

		nodeCert, err := pki.GenerateNodeCert(ca, opts)
		if err != nil {
			log.Fatalf("[FATAL] Failed to generate certificate for %s: %v", node, err)
		}

		nodeDir := filepath.Join(*outDir, node)
		if err := nodeCert.Save(nodeDir); err != nil {
			log.Fatalf("[FATAL] Failed to save certificate bundle for %s: %v", node, err)
		}

		// Also copy public ca.crt into the node bundle directory
		nodeCACopy := filepath.Join(nodeDir, "ca.crt")
		if err := os.WriteFile(nodeCACopy, ca.CertPEM, 0644); err != nil {
			log.Fatalf("[FATAL] Failed to copy ca.crt for %s: %v", node, err)
		}

		log.Printf("  [OK] Generated and verified node '%s':", node)
		log.Printf("       -> %s/server.crt", nodeDir)
		log.Printf("       -> %s/server.key (0600)", nodeDir)
		log.Printf("       -> SANs: DNS=%v, IP=%v", nodeCert.Certificate.DNSNames, nodeCert.Certificate.IPAddresses)
		successCount++
	}

	fmt.Println()
	log.Printf("=================================================================")
	log.Printf("Successfully generated %d verified node certificates in: %s", successCount, *outDir)
	log.Printf("=================================================================")
	log.Printf("DEPLOYMENT INSTRUCTIONS:")
	log.Printf("1. For each node (e.g. dvfs1):")
	log.Printf("   scp -r %s/dvfs1/* <user>@<dvfs1_ip>:/opt/dvfs/certs/", *outDir)
	log.Printf("2. Ensure file permissions on the target node:")
	log.Printf("   chmod 600 /opt/dvfs/certs/server.key")
	log.Printf("   chmod 644 /opt/dvfs/certs/server.crt /opt/dvfs/certs/ca.crt")
	log.Printf("3. NEVER transfer %s (the Root CA private key) to any server!", *caKeyPath)
	log.Printf("=================================================================")
}
