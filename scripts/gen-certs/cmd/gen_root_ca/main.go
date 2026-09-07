package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/scripts/gen-certs/pki"
)

func main() {
	outDir := flag.String("out", "./certs", "Output directory for ca.crt and ca.key")
	cn := flag.String("cn", pki.DefaultCACommonName, "Common Name for the Root CA")
	org := flag.String("org", pki.DefaultCAOrganization, "Organization for the Root CA")
	days := flag.Int("days", pki.DefaultCAValidityDays, "Validity duration in days (>= 3650 for 10+ years)")
	bits := flag.Int("bits", pki.DefaultCAKeyBits, "RSA key size in bits (e.g. 4096)")
	force := flag.Bool("force", false, "Force overwrite if ca.key/ca.crt already exist in output directory")
	flag.Parse()

	certPath := filepath.Join(*outDir, "ca.crt")
	keyPath := filepath.Join(*outDir, "ca.key")

	if !*force {
		if _, err := os.Stat(certPath); err == nil {
			log.Fatalf("[ERROR] %s already exists. Use -force to overwrite (WARNING: this invalidates all signed certificates!)", certPath)
		}
		if _, err := os.Stat(keyPath); err == nil {
			log.Fatalf("[ERROR] %s already exists. Use -force to overwrite.", keyPath)
		}
	}

	opts := pki.DefaultRootCAOptions()
	opts.CommonName = *cn
	opts.Organization = *org
	opts.ValidityDays = *days
	opts.KeyBits = *bits

	log.Printf("=================================================================")
	log.Printf("                     DVFS ROOT CA GENERATION                     ")
	log.Printf("=================================================================")
	log.Printf("Generating %d-bit RSA Root CA key pair...", opts.KeyBits)
	log.Printf("Validity: %d days (~%.1f years)", opts.ValidityDays, float64(opts.ValidityDays)/365.25)
	log.Printf("Subject: CN=%s, O=%s", opts.CommonName, opts.Organization)

	ca, err := pki.GenerateRootCA(opts)
	if err != nil {
		log.Fatalf("[FATAL] Failed to generate Root CA: %v", err)
	}

	if err := ca.Save(*outDir); err != nil {
		log.Fatalf("[FATAL] Failed to save Root CA to %s: %v", *outDir, err)
	}

	fmt.Println()
	log.Printf("Root CA successfully generated and saved to: %s", *outDir)
	log.Printf("  - Certificate: %s", certPath)
	log.Printf("  - Private Key: %s (Permissions: 0400)", keyPath)
	log.Printf("  - Serial:      %s", ca.Certificate.SerialNumber.Text(16))
	log.Printf("  - Not Before:  %s", ca.Certificate.NotBefore.Format("2006-01-02 15:04:05 UTC"))
	log.Printf("  - Not After:   %s", ca.Certificate.NotAfter.Format("2006-01-02 15:04:05 UTC"))
	fmt.Println()
	log.Printf("IMPORTANT SECURITY NOTICE:")
	log.Printf("1. ca.key MUST be kept strictly offline on encrypted storage.")
	log.Printf("2. NEVER copy ca.key to Metaserver, Fileservers, or client machines.")
	log.Printf("3. ca.crt will be embedded into the client binary as a constant string.")
	log.Printf("=================================================================")
}
