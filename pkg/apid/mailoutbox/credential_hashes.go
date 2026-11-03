package mailoutbox

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Mailed one-shot tokens are stored as keyed digests once the account
// policy's keyed writes are on (ADR 20261029202403), so a database writer
// cannot insert a reset or sign-in token of their own. Each tag names the
// column its digests are bound to; pkg/state uses the same names.
const (
	tagMagicLink         credhash.Tag = "magic_link_tokens.token_hash"
	tagPasswordReset     credhash.Tag = "password_reset_tokens.token_hash"
	tagMFADisable        credhash.Tag = "mfa_disable_tokens.token_hash"
	tagEmailVerification credhash.Tag = "email_verification_tokens.token_hash"
)

// storedTokenHash is the value a token whose SHA-256 is sum is written as.
// The outbox row keeps the same value, which it shares the token by.
func storedTokenHash(tag credhash.Tag, sum []byte) []byte {
	return credhash.Account().Store(tag, sum)
}

// tokenHashMatches reports whether stored is the value a token whose SHA-256
// is sum may be stored as.
func tokenHashMatches(tag credhash.Tag, sum, stored []byte) bool {
	return credhash.Account().Matches(tag, sum, stored)
}

// lockByTokenHash runs lock with each value a token whose SHA-256 is sum may
// be stored as and returns the first row found and the value that found it;
// pgx.ErrNoRows when none is.
func lockByTokenHash[T any](tag credhash.Tag, sum []byte, lock func([]byte) (T, error)) (T, []byte, error) {
	var zero T
	for _, value := range credhash.Account().Lookups(tag, sum) {
		row, err := lock(value)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return zero, nil, err
		}
		return row, value, nil
	}
	return zero, nil, pgx.ErrNoRows
}
