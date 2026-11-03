package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/state"
)

// credentialPolicies are the rollout steps of ADR 20261029202403 over one
// ring. Tests that install one must not run in parallel: the policy is
// process-wide.
func credentialPolicies(t *testing.T) (expand, keyed, contract *credhash.Policy) {
	t.Helper()
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { credhash.SetAccount(nil) })
	return &credhash.Policy{Ring: ring, PlainLookup: true},
		&credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: true},
		&credhash.Policy{Ring: ring, KeyedWrites: true}
}

// adr: 20261029202403 — a key written plain by the release before still
// authenticates through the rollout, a keyed write stores a digest, and
// once plain lookup is off a plain row a database writer inserted no
// longer authenticates.
func TestAPIKeyLookupsFollowTheRollout(t *testing.T) {
	expand, keyed, contract := credentialPolicies(t)
	ctx := context.Background()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(ctx, "credhash@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	plainSum := sha256.Sum256([]byte("fp_plain"))
	credhash.SetAccount(nil)
	if _, err := st.CreateAPIKey(ctx, acct.ID, plainSum[:], "plain", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}

	credhash.SetAccount(expand)
	if _, k, err := st.AuthenticateKey(ctx, plainSum[:]); err != nil || len(k.Hash) != credhash.PlainSize {
		t.Fatalf("expand: plain key = %x, %v", k.Hash, err)
	}

	credhash.SetAccount(keyed)
	keyedSum := sha256.Sum256([]byte("fp_keyed"))
	created, err := st.CreateAPIKey(ctx, acct.ID, keyedSum[:], "keyed", []string{api.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if !credhash.Keyed(created.Hash) {
		t.Fatalf("keyed write stored %x", created.Hash)
	}
	if _, k, err := st.AuthenticateKey(ctx, keyedSum[:]); err != nil || k.ID != created.ID {
		t.Fatalf("keyed key by its token's SHA-256 = %+v, %v", k, err)
	}
	if _, k, err := st.AuthenticateKey(ctx, created.Hash); err != nil || k.ID != created.ID {
		t.Fatalf("keyed key by its stored digest = %+v, %v", k, err)
	}
	if _, _, err := st.AuthenticateKey(ctx, plainSum[:]); err != nil {
		t.Fatalf("keyed writes still find a plain key: %v", err)
	}

	credhash.SetAccount(contract)
	if _, _, err := st.AuthenticateKey(ctx, keyedSum[:]); err != nil {
		t.Fatalf("contract: keyed key = %v", err)
	}
	forged := sha256.Sum256([]byte("fp_forged"))
	credhash.SetAccount(nil)
	if _, err := st.CreateAPIKey(ctx, acct.ID, forged[:], "forged", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}
	credhash.SetAccount(contract)
	if _, _, err := st.AuthenticateKey(ctx, forged[:]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("contract: a plain row authenticated: %v", err)
	}
}

// adr: 20261029202403 — one-shot tokens follow the same lookups: a token
// issued plain is redeemed during the rollout, one issued keyed is stored
// as a digest, and a digest is bound to its column.
func TestEmailVerificationTokensAreKeyed(t *testing.T) {
	expand, keyed, _ := credentialPolicies(t)
	ctx := context.Background()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(ctx, "verify@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)

	plain := sha256.Sum256([]byte("verify-plain"))
	credhash.SetAccount(nil)
	if err := st.IssueEmailVerificationToken(ctx, plain[:], acct.ID, expires); err != nil {
		t.Fatal(err)
	}
	credhash.SetAccount(expand)
	if got, err := st.ConsumeEmailVerificationToken(ctx, plain[:]); err != nil || got != acct.ID {
		t.Fatalf("plain token = %q, %v", got, err)
	}

	credhash.SetAccount(keyed)
	sum := sha256.Sum256([]byte("verify-keyed"))
	if err := st.IssueEmailVerificationToken(ctx, sum[:], acct.ID, expires); err != nil {
		t.Fatal(err)
	}
	if !state.CredentialMatches("email_verification_tokens.token_hash", sum[:], state.StoredCredential("email_verification_tokens.token_hash", sum[:])) {
		t.Fatal("a stored digest does not match its token")
	}
	if state.CredentialMatches("magic_link_tokens.token_hash", sum[:], state.StoredCredential("email_verification_tokens.token_hash", sum[:])) {
		t.Fatal("a digest matched in another column")
	}
	if got, err := st.ConsumeEmailVerificationToken(ctx, sum[:]); err != nil || got != acct.ID {
		t.Fatalf("keyed token = %q, %v", got, err)
	}
	if _, err := st.ConsumeEmailVerificationToken(ctx, sum[:]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replayed token = %v", err)
	}
}

// adr: 20261029202403 — hex columns hold a keyed digest's 66-character hex
// and are found by the hex SHA-256 the caller presents.
func TestHexCredentialLookupsTryEveryStoredForm(t *testing.T) {
	expand, keyed, contract := credentialPolicies(t)
	const tag = "issue_slack_oauth_flows.state_hash"
	sum := sha256.Sum256([]byte("nonce"))
	sumHex := hex.EncodeToString(sum[:])
	find := func(stored string) func(context.Context, string) (string, error) {
		return func(_ context.Context, value string) (string, error) {
			if value == stored {
				return value, nil
			}
			return "", state.ErrNotFound
		}
	}

	credhash.SetAccount(expand)
	if got := state.StoredCredentialHex(tag, sumHex); got != sumHex {
		t.Fatalf("expand stores %q", got)
	}
	credhash.SetAccount(keyed)
	stored := state.StoredCredentialHex(tag, sumHex)
	if len(stored) != 2*credhash.DigestSize {
		t.Fatalf("keyed stores %q", stored)
	}
	for _, tc := range []struct {
		policy *credhash.Policy
		stored string
		found  bool
	}{
		{keyed, stored, true},
		{keyed, sumHex, true},
		{contract, stored, true},
		{contract, sumHex, false},
	} {
		credhash.SetAccount(tc.policy)
		got, err := state.FirstCredentialHex(context.Background(), tag, sumHex, find(tc.stored))
		if tc.found != (err == nil) || tc.found && got != tc.stored {
			t.Fatalf("stored %q under %+v: got %q, %v", tc.stored, tc.policy, got, err)
		}
	}
}
