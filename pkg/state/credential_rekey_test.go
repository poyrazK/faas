package state_test

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func credentialRows(t *testing.T, s *state.PgStore, column string) map[int]int64 {
	t.Helper()
	rows, err := s.CredentialHashStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]int64{}
	for _, r := range rows {
		if r.Column == column {
			out[r.KeyID] = r.Rows
		}
	}
	return out
}

// adr: 20261029202403 decision 4.2 — once keyed writes are on, apid
// rewrites plain long-lived credentials without their plaintext, the
// status counts what is left by key, and the rewritten key still
// authenticates after plain lookup is turned off.
func TestRekeyCredentialsBackfillsPlainRows(t *testing.T) {
	expand, keyed, contract := credentialPolicies(t)
	ctx := context.Background()
	s := state.NewPgStore(pgtest.OpenMigrated(t))
	acct, err := s.CreateAccount(ctx, "rekey@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("fp_backfill"))
	credhash.SetAccount(nil)
	if _, err := s.CreateAPIKey(ctx, acct.ID, sum[:], "plain", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}
	if got := credentialRows(t, s, "api_keys.key_sha256"); got[-1] != 1 || len(got) != 1 {
		t.Fatalf("status before = %v", got)
	}

	credhash.SetAccount(expand)
	if n, err := s.RekeyCredentials(ctx, 10); n != 0 || err != nil {
		t.Fatalf("expand rekeyed %d, %v; keyed writes are off", n, err)
	}

	credhash.SetAccount(keyed)
	if n, err := s.RekeyCredentials(ctx, 10); n < 1 || err != nil {
		t.Fatalf("keyed writes rekeyed %d, %v", n, err)
	}
	if n, err := s.RekeyCredentials(ctx, 10); n != 0 || err != nil {
		t.Fatalf("second pass rekeyed %d, %v", n, err)
	}
	if got := credentialRows(t, s, "api_keys.key_sha256"); got[1] != 1 || got[-1] != 0 {
		t.Fatalf("status after = %v", got)
	}

	credhash.SetAccount(contract)
	if _, k, err := s.AuthenticateKey(ctx, sum[:]); err != nil || !credhash.Keyed(k.Hash) || k.Hash[0] != 1 {
		t.Fatalf("contract: backfilled key = %x, %v", k.Hash, err)
	}
}
