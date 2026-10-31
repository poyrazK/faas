package mailoutbox

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261029202403 — the outbox and pkg/state bind digests to the same
// column names, so a token one issues the other redeems.
func TestMailedTokenTagsMatchState(t *testing.T) {
	for _, pair := range [][2]credhash.Tag{
		{tagMagicLink, state.CredentialColumnMagicLink},
		{tagPasswordReset, state.CredentialColumnPasswordReset},
		{tagMFADisable, state.CredentialColumnMFADisable},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("outbox tag %q, state tag %q", pair[0], pair[1])
		}
	}
	sum := sha256.Sum256([]byte("verify"))
	if !state.CredentialMatches("email_verification_tokens.token_hash", sum[:], storedTokenHash(tagEmailVerification, sum[:])) {
		t.Fatal("state does not redeem the outbox's verification digest")
	}
}

// adr: 20261029202403 — a mailed token is redeemed whether its row holds
// the keyed digest or, during the rollout, the plain hash, and the row is
// then locked and consumed by the value it holds.
func TestLockByTokenHashTriesEveryStoredForm(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	credhash.SetAccount(&credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: true})
	t.Cleanup(func() { credhash.SetAccount(nil) })
	sum := sha256.Sum256([]byte("reset"))
	keyed := storedTokenHash(tagPasswordReset, sum[:])
	if !credhash.Keyed(keyed) {
		t.Fatalf("keyed writes stored %x", keyed)
	}
	for _, stored := range [][]byte{keyed, sum[:]} {
		row, value, err := lockByTokenHash(tagPasswordReset, sum[:], func(v []byte) (string, error) {
			if bytes.Equal(v, stored) {
				return "account-1", nil
			}
			return "", pgx.ErrNoRows
		})
		if err != nil || row != "account-1" || !bytes.Equal(value, stored) {
			t.Fatalf("stored %x: row %q value %x err %v", stored, row, value, err)
		}
	}
	if _, _, err := lockByTokenHash(tagPasswordReset, sum[:], func([]byte) (string, error) { return "", pgx.ErrNoRows }); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing token = %v", err)
	}
	boom := errors.New("db down")
	if _, _, err := lockByTokenHash(tagPasswordReset, sum[:], func([]byte) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("store failure = %v", err)
	}
}
