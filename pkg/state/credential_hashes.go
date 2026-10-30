package state

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Account-family credential columns (ADR 20261029202403). Each names the
// column its keyed digests are bound to.
const (
	credTagAPIKey    credhash.Tag = "api_keys.key_sha256"
	credTagOIDCToken credhash.Tag = "oidc_exchanged_tokens.token_hash"

	// CredentialColumnConsumerKey and CredentialColumnForkToken are checked
	// by gatewayd-internal, which loads the row itself and asks apid whether
	// the presented token matches (MatchCredential).
	CredentialColumnConsumerKey credhash.Tag = "consumer_keys.hashed_secret"
	CredentialColumnForkToken   credhash.Tag = "app_forks.access_token_hash"
)

// GatewayCheckedCredential reports whether a gateway may ask apid to match
// a value of column.
func GatewayCheckedCredential(column string) (credhash.Tag, bool) {
	switch tag := credhash.Tag(column); tag {
	case CredentialColumnConsumerKey, CredentialColumnForkToken:
		return tag, true
	}
	return "", false
}

// CredentialMatches reports whether stored, read from column tag, is the
// value a credential whose token hashes to sum may hold under this
// process's account policy.
func CredentialMatches(tag credhash.Tag, sum, stored []byte) bool {
	return credhash.Account().Matches(tag, sum, stored)
}

// StoredCredential is the value to write in column tag for a credential
// whose token hashes to sum.
func StoredCredential(tag credhash.Tag, sum []byte) []byte {
	return storedCredential(tag, sum)
}

// credentialLookups are the stored values to try for a credential: a stored
// digest (credhash.DigestSize bytes, as a caller re-checking a stored key
// passes) is looked up as it is; a token's SHA-256 gives the account
// policy's candidates, the current key's digest first.
func credentialLookups(tag credhash.Tag, sum []byte) [][]byte {
	if credhash.Keyed(sum) {
		return [][]byte{sum}
	}
	return credhash.Account().Lookups(tag, sum)
}

// storedCredential is the value to write for a credential whose token
// hashes to sum: a keyed digest once keyed writes are on, the plain hash
// before. A stored digest is written as it is.
func storedCredential(tag credhash.Tag, sum []byte) []byte {
	if credhash.Keyed(sum) {
		return sum
	}
	return credhash.Account().Store(tag, sum)
}

// staleCredential reports whether a credential found by its token's SHA-256
// should be rewritten under the current key, and the value to write.
func staleCredential(tag credhash.Tag, sum, stored []byte) ([]byte, bool) {
	p := credhash.Account()
	if len(sum) != credhash.PlainSize || !p.Stale(stored) {
		return nil, false
	}
	return p.Store(tag, sum), true
}

// firstCredential runs find for each lookup in turn and returns the first
// result that is not ErrNotFound.
func firstCredential[T any](ctx context.Context, lookups [][]byte, find func(context.Context, []byte) (T, error)) (T, error) {
	var zero T
	for _, value := range lookups {
		out, err := find(ctx, value)
		if err == nil || !errors.Is(err, ErrNotFound) {
			return out, err
		}
	}
	return zero, ErrNotFound
}

// credentialEntry finds a MemStore row keyed by the hex of its stored
// value, trying each lookup in turn, and returns the map key it found.
func credentialEntry[T any](rows map[string]T, tag credhash.Tag, sum []byte) (T, string, bool) {
	for _, value := range credentialLookups(tag, sum) {
		key := hex.EncodeToString(value)
		if row, ok := rows[key]; ok {
			return row, key, true
		}
	}
	var zero T
	return zero, "", false
}
