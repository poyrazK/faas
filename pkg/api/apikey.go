package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// API keys (spec §4.2, §11). The plaintext key is shown to the user exactly once
// (at creation); only its SHA-256 is stored. Keys are prefixed so they are
// greppable in incident response and detectable in leaked-secret scanners.
const (
	// APIKeyPrefix marks live keys. A test/sandbox prefix can be added later.
	APIKeyPrefix = "fp_live_"
	// apiKeyRandomBytes is the entropy behind each key.
	apiKeyRandomBytes = 24
)

// GenerateAPIKey mints a new key, returning the plaintext (to show the user once)
// and its SHA-256 hash (to store). The plaintext is never persisted.
func GenerateAPIKey() (plaintext string, hash []byte, err error) {
	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("api: generate key: %w", err)
	}
	plaintext = APIKeyPrefix + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:], nil
}

// HashAPIKey returns the SHA-256 of a plaintext key for lookup/comparison.
func HashAPIKey(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// HashToken returns the SHA-256 of arbitrary raw bytes. Login tokens
// (M7.5 magic link) are random 32-byte values — no API-key prefix —
// so the storage key is the SHA-256 of the raw token, hex-decoded.
func HashToken(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

// ValidAPIKeyFormat reports whether s looks like one of our keys (cheap pre-check
// before hitting the database).
func ValidAPIKeyFormat(s string) bool {
	if !strings.HasPrefix(s, APIKeyPrefix) {
		return false
	}
	body := strings.TrimPrefix(s, APIKeyPrefix)
	if len(body) != apiKeyRandomBytes*2 {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

// ConstantTimeEqualHash compares two key hashes without leaking timing.
func ConstantTimeEqualHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
