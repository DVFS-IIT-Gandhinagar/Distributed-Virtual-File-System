package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const (
	SSTPrefix     = "sst_"
	TokenByteSize = 32 // 256 bits of CSPRNG entropy
)

// GenerateSST mints a cryptographically secure 256-bit Server Session Token.
// Follows NIST SP 800-63B section 7.1 guidelines for authenticator entropy.
func GenerateSST() (string, error) {
	buf := make([]byte, TokenByteSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("csprng failure: %w", err)
	}
	return SSTPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSST returns the SHA-256 hexadecimal hash of the SST.
// Sessions are stored and indexed by hash to prevent plaintext token recovery from memory inspection.
func HashSST(sst string) string {
	sum := sha256.Sum256([]byte(sst))
	return hex.EncodeToString(sum[:])
}
