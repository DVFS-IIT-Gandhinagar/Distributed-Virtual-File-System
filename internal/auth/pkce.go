package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// PKCEPair contains the code verifier and code challenge per RFC 7636.
type PKCEPair struct {
	Verifier  string
	Challenge string
	Method    string
}

// GeneratePKCE generates a 32-byte CSPRNG verifier and SHA-256 S256 challenge.
func GeneratePKCE() (*PKCEPair, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(bytes)
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &PKCEPair{
		Verifier:  verifier,
		Challenge: challenge,
		Method:    "S256",
	}, nil
}
