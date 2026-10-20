package state_test

// memstore_app_registry_credentials_test pins the in-memory
// implementation of the per-app private-registry Basic Auth store
// (issue #461 / ADR-062). The MemStore mirror covers the contract
// without Postgres; pgtest round-trip lives in
// pgstore_app_registry_credentials_test.go.
//
// The tests below mirror memstore_coverage_slice3_test.go's posture
// for AppSecret — round-trip, replacement, account isolation,
// not-found delete, quota count, cascade cleanup, and the
// MarkAppRegistryCredentialUsed semantics.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func registryCredFixture(t *testing.T) (*state.MemStore, context.Context, state.Account, state.App, state.App) {
	t.Helper()
	m := state.NewMemStore()
	ctx := context.Background()
	acctA, err := m.CreateAccount(ctx, "registry-auth-a@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	acctB, err := m.CreateAccount(ctx, "registry-auth-b@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	appA, err := m.CreateApp(ctx, state.App{
		AccountID: acctA.ID, Slug: "reg-auth-a", RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	appB, err := m.CreateApp(ctx, state.App{
		AccountID: acctB.ID, Slug: "reg-auth-b", RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	return m, ctx, acctA, appA, appB
}

// fixtureAcctID looks up the account matching app.AccountID from the
// store; tests that need to drive the GDPR pre-delete handshake use
// this because the minimal registryCredFixture intentionally avoids
// leaking the Account through every test signature.
func fixtureAcctID(t *testing.T, m *state.MemStore, app state.App) string {
	t.Helper()
	all, err := m.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAllAccounts: %v", err)
	}
	for _, a := range all {
		if a.ID == app.AccountID {
			return a.ID
		}
	}
	t.Fatalf("account %q not found", app.AccountID)
	return ""
}

// TestMemStore_AppRegistryCredentials_RoundTrip exercises Upsert →
// Get → List → Delete → Count for a single (app, registry) row.
// Ciphertext is opaque bytes; we round-trip the exact bytes to pin
// "MemStore doesn't accidentally copy/skip/re-encode".
func TestMemStore_AppRegistryCredentials_RoundTrip(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	const ciphertext = "sealed-blob-bytes-v1"

	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io", "alice", []byte(ciphertext)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want ghcr.io", got.Registry)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q, want alice", got.Username)
	}
	if string(got.PasswordEncrypted) != ciphertext {
		t.Errorf("PasswordEncrypted round-trip mismatch: got %q want %q", string(got.PasswordEncrypted), ciphertext)
	}
	if got.LastUsedAt != nil {
		t.Errorf("LastUsedAt on fresh row = %v, want nil", got.LastUsedAt)
	}

	list, err := m.ListAppRegistryCredentials(ctx, app.AccountID, app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}
	if list[0].Registry != "ghcr.io" {
		t.Errorf("List[0].Registry = %q, want ghcr.io", list[0].Registry)
	}

	count, err := m.CountAppRegistryCredentials(ctx, app.AccountID, app.ID)
	if err != nil || count != 1 {
		t.Errorf("Count = %d, %v; want 1, nil", count, err)
	}

	if err := m.DeleteAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

// TestMemStore_AppRegistryCredentials_UpdatePreservesCreatedAt pins
// that re-PUTting the same (app, registry) row preserves CreatedAt
// and bumps UpdatedAt — same contract as UpsertAppSecret.
func TestMemStore_AppRegistryCredentials_UpdatePreservesCreatedAt(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)

	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io", "alice", []byte("ct1")); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	first, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	createdAt := first.CreatedAt
	// Force a clock tick so UpdatedAt advances.
	time.Sleep(2 * time.Millisecond)
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io", "alice-rotated", []byte("ct2")); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	second, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if !second.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt advanced across upsert: first=%v second=%v", createdAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(createdAt) {
		t.Errorf("UpdatedAt did not advance: %v (created=%v)", second.UpdatedAt, createdAt)
	}
	if second.Username != "alice-rotated" {
		t.Errorf("Username after rotation = %q, want alice-rotated", second.Username)
	}
	if string(second.PasswordEncrypted) != "ct2" {
		t.Errorf("Ciphertext after rotation = %q, want ct2", string(second.PasswordEncrypted))
	}
}

// TestMemStore_AppRegistryCredentials_AccountIsolation pins the
// cross-account ErrNotFound predicate — defense in depth so a stale
// ID→slug mapping can't leak a credential to the wrong tenant.
func TestMemStore_AppRegistryCredentials_AccountIsolation(t *testing.T) {
	m, ctx, _, appA, appB := registryCredFixture(t)
	if err := m.UpsertAppRegistryCredential(ctx, appA.AccountID, appA.ID, "ghcr.io", "alice", []byte("ct")); err != nil {
		t.Fatalf("Upsert under A: %v", err)
	}
	// Get under the wrong (accountID, appID) returns ErrNotFound.
	if _, err := m.GetAppRegistryCredential(ctx, appB.AccountID, appA.ID, "ghcr.io"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get cross-account = %v, want ErrNotFound", err)
	}
	// Get under the right account but a different app returns ErrNotFound.
	if _, err := m.GetAppRegistryCredential(ctx, appA.AccountID, appB.ID, "ghcr.io"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Get cross-app = %v, want ErrNotFound", err)
	}
	// List under the wrong account returns nothing.
	list, err := m.ListAppRegistryCredentials(ctx, appB.AccountID, appA.ID)
	if err != nil {
		t.Fatalf("List cross-account: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List cross-account len = %d, want 0", len(list))
	}
	// Delete under the wrong account returns ErrNotFound.
	if err := m.DeleteAppRegistryCredential(ctx, appB.AccountID, appA.ID, "ghcr.io"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete cross-account = %v, want ErrNotFound", err)
	}
	// The original row survives.
	if _, err := m.GetAppRegistryCredential(ctx, appA.AccountID, appA.ID, "ghcr.io"); err != nil {
		t.Errorf("Get original after cross-account attempts: %v", err)
	}
}

// TestMemStore_AppRegistryCredentials_DeleteNotFound pins the
// 400-by-design posture: the URL resource IS the registry host, so a
// DELETE for an absent row returns ErrNotFound (not 404 in handler
// land).
func TestMemStore_AppRegistryCredentials_DeleteNotFound(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	if err := m.DeleteAppRegistryCredential(ctx, app.AccountID, app.ID, "never-existed.example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Delete absent = %v, want ErrNotFound", err)
	}
}

// TestMemStore_AppRegistryCredentials_AccountDeletionCascade pins
// that the G6 GDPR DeleteAccount sweep removes every credential row
// for the account. The FK on app_registry_credentials.account_id is
// the production guarantee; MemStore must match (memstore.go:5950).
func TestMemStore_AppRegistryCredentials_AccountDeletionCascade(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io", "alice", []byte("ct")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "registry.gregale.dev", "bob", []byte("ct2")); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	acctID := fixtureAcctID(t, m, app)
	// DeleteAccount requires status='deleted_pending'; that precondition
	// is set by MarkAccountDeletionPending (mirrors the apid
	// delete-pending grace handshake).
	if err := m.MarkAccountDeletionPending(ctx, acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(ctx, acctID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	// Both cred rows gone.
	count, err := m.CountAppRegistryCredentials(context.Background(), acctID, app.ID)
	if err != nil {
		t.Fatalf("Count after cascade: %v", err)
	}
	if count != 0 {
		t.Errorf("Count after account cascade = %d, want 0", count)
	}
}

// TestMemStore_AppRegistryCredentials_QuotaCount pins the
// CountAppRegistryCredentials helper used by the apid handler to
// enforce Limits.RegistryCredentialMax before upsert.
func TestMemStore_AppRegistryCredentials_QuotaCount(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	hosts := []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"}
	for i, h := range hosts {
		if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, h, "u", []byte{byte(i)}); err != nil {
			t.Fatalf("Upsert %s: %v", h, err)
		}
	}
	count, err := m.CountAppRegistryCredentials(ctx, app.AccountID, app.ID)
	if err != nil || count != 4 {
		t.Errorf("Count = %d, %v; want 4, nil", count, err)
	}
}

// TestMemStore_AppRegistryCredentials_MarkUsed_UpdatesLastUsed pins
// that MarkAppRegistryCredentialUsed updates LastUsedAt without
// touching CreatedAt; idempotent on re-mark.
func TestMemStore_AppRegistryCredentials_MarkUsed_UpdatesLastUsed(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io", "alice", []byte("ct")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	before, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if before.LastUsedAt != nil {
		t.Fatalf("LastUsedAt before MarkUsed = %v, want nil", before.LastUsedAt)
	}
	// First mark.
	if err := m.MarkAppRegistryCredentialUsed(ctx, app.AccountID, app.ID, "ghcr.io"); err != nil {
		t.Fatalf("MarkUsed 1: %v", err)
	}
	first, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}
	if first.LastUsedAt == nil {
		t.Fatalf("LastUsedAt after MarkUsed = nil, want non-nil")
	}
	createdAt := first.CreatedAt
	// Re-mark after a sleep — LastUsedAt advances, CreatedAt holds.
	time.Sleep(2 * time.Millisecond)
	if err := m.MarkAppRegistryCredentialUsed(ctx, app.AccountID, app.ID, "ghcr.io"); err != nil {
		t.Fatalf("MarkUsed 2: %v", err)
	}
	second, err := m.GetAppRegistryCredential(ctx, app.AccountID, app.ID, "ghcr.io")
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}
	if !second.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt changed across MarkUsed: %v vs %v", createdAt, second.CreatedAt)
	}
	if second.LastUsedAt == nil || !second.LastUsedAt.After(*first.LastUsedAt) {
		t.Errorf("LastUsedAt did not advance: first=%v second=%v", first.LastUsedAt, second.LastUsedAt)
	}
}

// TestMemStore_AppRegistryCredentials_MarkUsed_NotFound pins the
// "non-fatal" contract: ErrNotFound surfaces (callers MUST treat as
// non-fatal — the deployment already succeeded).
func TestMemStore_AppRegistryCredentials_MarkUsed_NotFound(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	if err := m.MarkAppRegistryCredentialUsed(ctx, app.AccountID, app.ID, "never-existed.example.com"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkUsed absent = %v, want ErrNotFound", err)
	}
}

// TestMemStore_AppRegistryCredentials_MultipleRegistries pins the
// UNIQUE (app_id, registry) constraint: two different registries on
// the same app coexist; listing returns them ordered by registry.
func TestMemStore_AppRegistryCredentials_MultipleRegistries(t *testing.T) {
	m, ctx, _, app, _ := registryCredFixture(t)
	// Insertion order is NOT alphabetical to verify the sort.
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "zzz.example.com", "u1", []byte("ct1")); err != nil {
		t.Fatalf("Upsert zzz: %v", err)
	}
	if err := m.UpsertAppRegistryCredential(ctx, app.AccountID, app.ID, "aaa.example.com", "u2", []byte("ct2")); err != nil {
		t.Fatalf("Upsert aaa: %v", err)
	}
	list, err := m.ListAppRegistryCredentials(ctx, app.AccountID, app.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	if list[0].Registry != "aaa.example.com" || list[1].Registry != "zzz.example.com" {
		t.Errorf("List order = [%s %s], want [aaa zzz]", list[0].Registry, list[1].Registry)
	}
}

// TestMemStore_AppRegistryCredentials_FixtureUUIDsAreValid pins
// that the fixture produces real UUIDs (defense against a refactor
// that swaps in a string type and breaks the SQL round-trip).
func TestMemStore_AppRegistryCredentials_FixtureUUIDsAreValid(t *testing.T) {
	_, _, _, appA, appB := registryCredFixture(t)
	if _, err := uuid.Parse(appA.ID); err != nil {
		t.Errorf("appA.ID %q is not a UUID: %v", appA.ID, err)
	}
	if _, err := uuid.Parse(appB.ID); err != nil {
		t.Errorf("appB.ID %q is not a UUID: %v", appB.ID, err)
	}
	if appA.ID == appB.ID {
		t.Errorf("fixture produced duplicate IDs: %s", appA.ID)
	}
}
