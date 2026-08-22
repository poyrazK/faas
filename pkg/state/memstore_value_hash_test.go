// memstore_value_hash_test.go — ADR-117 PR-C widening.
//
// Pins the memstore sibling of
// UpsertAppSecretWithKidAndValueHashInScope + the AppSecret
// struct's ValueHash field round-trip through every read path.
// Mirrors pgstore_value_hash_test.go's surface but uses
// MemStore-only paths (no pgx pool / migrations).

package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// memValueHashFixture seeds a Pro account + app for the
// memstore value_hash tests.
func memValueHashFixture(t *testing.T) (*state.MemStore, context.Context, state.Account, state.App) {
	t.Helper()
	m := state.NewMemStore()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "mem-vh-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "mem-vh-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return m, ctx, account, app
}

// TestMemStore_UpsertAppSecretWithKidAndValueHashInScope_Happy pins
// the load-bearing write side: a row stamped with a 16-hex
// value_hash round-trips through GetAppSecretInScope with the
// same value.
func TestMemStore_UpsertAppSecretWithKidAndValueHashInScope_Happy(t *testing.T) {
	m, ctx, account, app := memValueHashFixture(t)
	const fp = "abcdef0123456789"
	if err := m.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "API_KEY", "age1abc", fp, []byte("cipher")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := m.GetAppSecretInScope(ctx, account.ID, app.ID, "prod", "API_KEY")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ValueHash != fp {
		t.Errorf("ValueHash round-trip: got %q, want %q", got.ValueHash, fp)
	}
	if got.Kid != "age1abc" {
		t.Errorf("Kid round-trip: got %q, want %q", got.Kid, "age1abc")
	}
}

// TestMemStore_UpsertAppSecretWithKidAndValueHashInScope_Overwrites pins
// the on-conflict semantics: a second upsert at the same tuple
// replaces ciphertext + kid + value_hash. The map key is
// (app_id, scope, key) so the replacement is the standard path.
func TestMemStore_UpsertAppSecretWithKidAndValueHashInScope_Overwrites(t *testing.T) {
	m, ctx, account, app := memValueHashFixture(t)
	if err := m.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "API_KEY", "k1", "1111111111111111", []byte("c1")); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := m.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "API_KEY", "k2", "2222222222222222", []byte("c2")); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := m.GetAppSecretInScope(ctx, account.ID, app.ID, "prod", "API_KEY")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ValueHash != "2222222222222222" {
		t.Errorf("ValueHash overwrite: got %q, want %q", got.ValueHash, "2222222222222222")
	}
	if got.Kid != "k2" {
		t.Errorf("Kid overwrite: got %q, want %q", got.Kid, "k2")
	}
	if string(got.Ciphertext) != "c2" {
		t.Errorf("Ciphertext overwrite: got %q, want %q", got.Ciphertext, "c2")
	}
}

// TestMemStore_ListAppSecretReads_ValueHash_Surfaced pins every
// memstore read path that the env-diff endpoint will rely on.
// The PR-C handler uses ListAllAppSecrets; the legacy
// default-scope path uses ListAppSecretsInScope.
func TestMemStore_ListAppSecretReads_ValueHash_Surfaced(t *testing.T) {
	m, ctx, account, app := memValueHashFixture(t)

	if err := m.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "WITH_HASH", "k1", "abcdef0123456789", []byte("c1")); err != nil {
		t.Fatalf("seed with-hash: %v", err)
	}
	if err := m.UpsertAppSecretWithKidInScope(ctx, account.ID, app.ID, "staging", "WITHOUT_HASH", "k2", []byte("c2")); err != nil {
		t.Fatalf("seed without-hash: %v", err)
	}

	// ListAllAppSecrets (env-diff path).
	all, err := m.ListAllAppSecrets(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatalf("ListAllAppSecrets: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllAppSecrets: want 2 rows, got %d", len(all))
	}
	for _, r := range all {
		switch r.Key {
		case "WITH_HASH":
			if r.ValueHash != "abcdef0123456789" {
				t.Errorf("WITH_HASH.ValueHash = %q, want %q", r.ValueHash, "abcdef0123456789")
			}
		case "WITHOUT_HASH":
			if r.ValueHash != "" {
				t.Errorf("WITHOUT_HASH.ValueHash = %q, want \"\" (legacy kid-only upsert stores zero-value)", r.ValueHash)
			}
		}
	}

	// ListAppSecretsInScope (default-scope PR-A path).
	inScope, err := m.ListAppSecretsInScope(ctx, account.ID, app.ID, "staging")
	if err != nil {
		t.Fatalf("ListAppSecretsInScope: %v", err)
	}
	if len(inScope) != 1 || inScope[0].Key != "WITHOUT_HASH" || inScope[0].ValueHash != "" {
		t.Errorf("ListAppSecretsInScope(staging) surfaced %+v; want [{WITHOUT_HASH,ValueHash:''}]", inScope)
	}

	// ListAppSecretsForRekey.
	rekeyRows, err := m.ListAppSecretsForRekey(ctx, 10, "")
	if err != nil {
		t.Fatalf("ListAppSecretsForRekey: %v", err)
	}
	if len(rekeyRows) != 2 {
		t.Fatalf("ListAppSecretsForRekey: want 2 rows, got %d", len(rekeyRows))
	}
	for _, r := range rekeyRows {
		switch r.Key {
		case "WITH_HASH":
			if r.ValueHash != "abcdef0123456789" {
				t.Errorf("Rekey WITH_HASH.ValueHash = %q, want %q", r.ValueHash, "abcdef0123456789")
			}
		case "WITHOUT_HASH":
			if r.ValueHash != "" {
				t.Errorf("Rekey WITHOUT_HASH.ValueHash = %q, want \"\"", r.ValueHash)
			}
		}
	}
}

// TestMemStore_ListAppSecretsForAccount_ValueHash_Surfaced pins the
// per-account cross-app enumeration (issue #393) — the
// admin-style GET /secrets path that walks every app on the
// account.
func TestMemStore_ListAppSecretsForAccount_ValueHash_Surfaced(t *testing.T) {
	m, ctx, account, app := memValueHashFixture(t)
	if err := m.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "API_KEY", "k1", "deadbeefcafebabe", []byte("cipher")); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err := m.ListAppSecretsForAccount(ctx, account.ID, 25, "")
	if err != nil {
		t.Fatalf("ListAppSecretsForAccount: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].ValueHash != "deadbeefcafebabe" {
		t.Errorf("ValueHash surfaced: got %q, want %q", rows[0].ValueHash, "deadbeefcafebabe")
	}
	if rows[0].AppSlug != app.Slug {
		t.Errorf("AppSlug surfaced: got %q, want %q", rows[0].AppSlug, app.Slug)
	}
}
