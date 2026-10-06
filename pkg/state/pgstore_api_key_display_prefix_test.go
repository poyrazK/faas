//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreAPIKeyDisplayPrefix(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "key-prefix-pg@example.test", "pro")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(ctx, acct.ID, hash, "pg", []string{api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	_, otherHash, _ := api.GenerateAPIKey()
	other, err := s.CreateAPIKey(ctx, acct.ID, otherHash, "no-prefix", []string{api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetAPIKeyDisplayPrefix(ctx, key.ID, plaintext[:16]); err != nil {
		t.Fatalf("SetAPIKeyDisplayPrefix: %v", err)
	}
	got, err := s.APIKeyDisplayPrefixes(ctx, []string{key.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got[key.ID] != plaintext[:16] {
		t.Fatalf("prefix = %q, want %q", got[key.ID], plaintext[:16])
	}
	if _, ok := got[other.ID]; ok {
		t.Fatalf("key without a recorded prefix returned %q", got[other.ID])
	}
	// The CHECK constraint keeps secret material or junk out of the column.
	if err := s.SetAPIKeyDisplayPrefix(ctx, other.ID, plaintext); err == nil {
		t.Fatal("full plaintext accepted as a display prefix")
	}
	if err := s.SetAPIKeyDisplayPrefix(ctx, "00000000-0000-0000-0000-000000000000", plaintext[:16]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown key = %v, want ErrNotFound", err)
	}
}
