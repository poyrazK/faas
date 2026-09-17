// Package oauthpkce implements the RFC 7636 Proof Key for Code Exchange
// helpers used by Gregale's browser-based OAuth authorization-code flows.
package oauthpkce

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// MethodS256 is the RFC 7636 challenge method supported by Gregale.
const MethodS256 = "S256"

// Generate creates an RFC 7636 code verifier and its S256 code challenge.
// A 32-byte random verifier encodes to 43 unpadded base64url characters,
// satisfying the RFC's 43-128 character verifier requirement.
func Generate() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

// Verify reports whether verifier produces the supplied RFC 7636 S256
// challenge. Constant-time comparison avoids turning this helper into a
// challenge oracle if it is reused at a security boundary.
func Verify(verifier, challenge string) bool {
	if !ValidVerifier(verifier) || challenge == "" {
		return false
	}
	hash := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) == 1
}

// ValidVerifier reports whether verifier satisfies RFC 7636's code_verifier
// length and unreserved-character requirements.
func ValidVerifier(verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for i := 0; i < len(verifier); i++ {
		c := verifier[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			continue
		}
		return false
	}
	return true
}
