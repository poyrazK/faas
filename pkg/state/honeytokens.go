package state

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"time"
)

// HoneytokenKindAPIKey is a decoy Gregale API key (ADR 20261024014500).
const HoneytokenKindAPIKey = "api_key"

// Honeytoken is a registered decoy credential. Only its digest is stored.
type Honeytoken struct {
	ID, Kind, Label string
	TokenDigest     string // hex HMAC, see HoneytokenDigest
	PlantedAt       time.Time
	RetiredAt       *time.Time
}

// HoneytokenStore keeps the honeytoken registry. It is not part of Store:
// apid's auth middleware and gregalectl reach it through a type assertion.
type HoneytokenStore interface {
	// CreateHoneytoken registers a decoy by its HoneytokenDigest.
	CreateHoneytoken(ctx context.Context, kind, digest, label string) (Honeytoken, error)
	// MatchHoneytoken returns the honeytoken with this digest, retired or
	// not, or ErrNotFound.
	MatchHoneytoken(ctx context.Context, digest string) (Honeytoken, error)
	// ListHoneytokens returns every honeytoken, oldest first.
	ListHoneytokens(ctx context.Context) ([]Honeytoken, error)
	// RetireHoneytoken stamps retired_at once; a retired token still matches.
	RetireHoneytoken(ctx context.Context, id string) (Honeytoken, error)
}

// honeytokenDigestDomain separates honeytoken digests from every other HMAC
// under the same key: app_secrets.value_hash is an HMAC of the same
// plaintext under the same host key, and a shared derivation would let a
// copy of the database match decoy rows to registry rows.
const honeytokenDigestDomain = "gregale-honeytoken-v1\x00"

// HoneytokenDigest is the registry key for a decoy credential: the hex
// HMAC-SHA256 of token under apid's host HMAC key. The key is not in the
// database, so the registry gives a copy of the database no way to test
// whether a key it decrypted is a decoy.
func HoneytokenDigest(hmacKey []byte, token string) (string, error) {
	if len(hmacKey) < 32 || token == "" {
		return "", ErrInvalidArgument
	}
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write([]byte(honeytokenDigestDomain))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

var honeytokenDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validHoneytoken(kind, digest, label string) bool {
	return kind == HoneytokenKindAPIKey && honeytokenDigestPattern.MatchString(digest) && len(label) >= 1 && len(label) <= 200
}
