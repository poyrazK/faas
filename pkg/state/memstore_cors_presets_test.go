package state_test

// Coverage for the MemStore side of the cors_presets read path
// (issue #975 item #4 / Mega-Foundation #979-b, slot 00294). The
// write surface lands in PR-B (#979-c, slot 00295); these tests
// seed m.corsPresets directly to exercise the read path that the
// handler tests (cmd/gatewayd-internal/edge_rules_test.go) will
// depend on. Mirrors the pattern at
// memstore_app_webhooks_test.go (in-memory seed via the internal
// map; no sqlc involvement).

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// memSampleCorsPreset returns the simplest valid seed row. The
// caller is expected to set the row's ID, CreatedAt, and
// UpdatedAt to deterministic values.
func memSampleCorsPreset(accountID, appID, name string) state.CorsPreset {
	return state.CorsPreset{
		ID:               uuid.NewString(),
		AccountID:        accountID,
		AppID:            appID,
		Name:             name,
		Description:      "shared allowlist",
		AllowOrigins:     []string{"https://app.example.com"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Authorization"},
		ExposeHeaders:    []string{"X-Request-Id"},
		AllowCredentials: false,
		MaxAgeSeconds:    600,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
}

// memSeedCorsPreset inserts a preset into the memstore map. Used
// by the read-path tests to bypass the (not-yet-existing) write
// surface — same pattern as memstore_app_webhooks_test.go.
func memSeedCorsPreset(m *state.MemStore, p state.CorsPreset) state.CorsPreset {
	(*m.Mu()).Lock()
	defer (*m.Mu()).Unlock()
	(*m.CorsPresets())[p.ID] = p
	return p
}

func corsFixture(t *testing.T) (m *state.MemStore, ctx context.Context, accountID, appID string) {
	t.Helper()
	ctx = context.Background()
	m = state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "cors-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "cors-" + strconv.Itoa(int(time.Now().UnixNano())),
		RAMMB:     256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct.ID, app.ID
}

// TestMemStore_CorsPreset_AccountWideRead pins the account-wide
// read path. AppID is empty in the seed row.
func TestMemStore_CorsPreset_AccountWideRead(t *testing.T) {
	m, ctx, acct, _ := corsFixture(t)

	seed := memSampleCorsPreset(acct, "", "shared")
	memSeedCorsPreset(m, seed)

	got, err := m.GetCorsPresetByID(ctx, acct, seed.ID)
	if err != nil {
		t.Fatalf("GetCorsPresetByID: %v", err)
	}
	if got.AccountID != acct {
		t.Errorf("AccountID = %q, want %q", got.AccountID, acct)
	}
	if got.AppID != "" {
		t.Errorf("AppID = %q, want empty", got.AppID)
	}
	if got.Name != "shared" {
		t.Errorf("Name = %q, want %q", got.Name, "shared")
	}
}

// TestMemStore_CorsPreset_AppScopedRead pins the app-scoped read
// path.
func TestMemStore_CorsPreset_AppScopedRead(t *testing.T) {
	m, ctx, acct, app := corsFixture(t)

	seed := memSampleCorsPreset(acct, app, "scoped")
	memSeedCorsPreset(m, seed)

	got, err := m.GetCorsPresetByID(ctx, acct, seed.ID)
	if err != nil {
		t.Fatalf("GetCorsPresetByID: %v", err)
	}
	if got.AppID != app {
		t.Errorf("AppID = %q, want %q", got.AppID, app)
	}
}

// TestMemStore_CorsPreset_GetByID_NotFound pins the ErrNotFound
// path. The apid boundary maps this to 422 ("preset has been
// deleted; re-save the rule").
func TestMemStore_CorsPreset_GetByID_NotFound(t *testing.T) {
	m, ctx, acct, _ := corsFixture(t)
	_, err := m.GetCorsPresetByID(ctx, acct, uuid.NewString())
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetCorsPresetByID(unknown) = %v, want ErrNotFound", err)
	}
}

// TestMemStore_CorsPreset_GetByID_CrossTenantRejects pins the
// tenancy-at-the-boundary guard. A preset created under account
// A must not be readable by account B even with the right id.
func TestMemStore_CorsPreset_GetByID_CrossTenantRejects(t *testing.T) {
	m, ctx, acctA, _ := corsFixture(t)
	_, _, acctB, _ := corsFixture(t)

	seed := memSampleCorsPreset(acctA, "", "isolated")
	memSeedCorsPreset(m, seed)

	_, err := m.GetCorsPresetByID(ctx, acctB, seed.ID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetCorsPresetByID(cross-tenant) = %v, want ErrNotFound", err)
	}
}

// TestMemStore_CorsPreset_ListForAccount_OrdersByAppIDNullsFirst
// mirrors the pgstore test of the same name. Account-wide rows
// (AppID == "") must come first; within each group, alphabetical
// by name. The compile-side merge in
// cmd/gatewayd-internal/edge_rules.go::compileCORSRules depends
// on this ordering for the deterministic cache key.
func TestMemStore_CorsPreset_ListForAccount_OrdersByAppIDNullsFirst(t *testing.T) {
	m, ctx, acct, app := corsFixture(t)

	// Insert in deliberately scrambled order to prove the
	// in-memory sort is the source of truth, not insertion order.
	memSeedCorsPreset(m, memSampleCorsPreset(acct, app, "z-app"))
	memSeedCorsPreset(m, memSampleCorsPreset(acct, "", "a-wide"))
	memSeedCorsPreset(m, memSampleCorsPreset(acct, app, "a-app"))

	got, err := m.ListCorsPresetsForAccount(ctx, acct)
	if err != nil {
		t.Fatalf("ListCorsPresetsForAccount: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].AppID != "" {
		t.Errorf("got[0].AppID = %q, want empty (account-wide first)", got[0].AppID)
	}
	if got[1].AppID != app || got[2].AppID != app {
		t.Errorf("got[1].AppID=%q got[2].AppID=%q, want both = %q", got[1].AppID, got[2].AppID, app)
	}
	if got[1].Name > got[2].Name {
		t.Errorf("app-scoped rows not sorted by name: %q > %q", got[1].Name, got[2].Name)
	}
}

// TestMemStore_CorsPreset_ListForAccount_ScopesByAccount pins
// the cross-account guard — the IDOR regression net for the
// in-process path.
func TestMemStore_CorsPreset_ListForAccount_ScopesByAccount(t *testing.T) {
	m, ctx, acctA, _ := corsFixture(t)
	// Create a second account by reusing the fixture helper.
	_, _, acctB, _ := corsFixture(t)

	memSeedCorsPreset(m, memSampleCorsPreset(acctA, "", "a-only"))
	memSeedCorsPreset(m, memSampleCorsPreset(acctB, "", "b-only"))

	listA, err := m.ListCorsPresetsForAccount(ctx, acctA)
	if err != nil {
		t.Fatalf("ListCorsPresetsForAccount(A): %v", err)
	}
	if len(listA) != 1 {
		t.Errorf("len = %d, want 1", len(listA))
	}
	if len(listA) > 0 && listA[0].AccountID != acctA {
		t.Errorf("account A leaked: %q", listA[0].AccountID)
	}
}

// TestMemStore_CorsPreset_ListForApp_ExcludesAccountWide pins
// the strict-scope contract of ListCorsPresetsForApp. The compile
// path unions this with the result of ListCorsPresetsForAccount
// for the full overlay.
func TestMemStore_CorsPreset_ListForApp_ExcludesAccountWide(t *testing.T) {
	m, ctx, acct, app := corsFixture(t)

	memSeedCorsPreset(m, memSampleCorsPreset(acct, "", "wide"))
	memSeedCorsPreset(m, memSampleCorsPreset(acct, app, "scoped"))

	list, err := m.ListCorsPresetsForApp(ctx, acct, app)
	if err != nil {
		t.Fatalf("ListCorsPresetsForApp: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].AppID != app {
		t.Errorf("AppID = %q, want %q", list[0].AppID, app)
	}
}

// TestMemStore_CorsPreset_ListForApp_RejectsCrossAccount pins
// the account filter added after the medium code review. The
// memstore path previously matched p.AppID == appID, which leaked
// account-wide presets when appID was empty AND leaked rows
// across accounts when a caller probed by appID without an
// account filter. The Store boundary now requires accountID so
// both stores are behaviorally aligned.
func TestMemStore_CorsPreset_ListForApp_RejectsCrossAccount(t *testing.T) {
	m, ctx, acctA, appA := corsFixture(t)
	_, _, acctB, _ := corsFixture(t)

	memSeedCorsPreset(m, memSampleCorsPreset(acctA, appA, "isolated"))

	list, err := m.ListCorsPresetsForApp(ctx, acctB, appA)
	if err != nil {
		t.Fatalf("ListCorsPresetsForApp(cross-tenant): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0 (cross-tenant probe rejected)", len(list))
	}
}

// TestMemStore_CorsPreset_ListForApp_RejectsEmptyAppID pins the
// complement: a caller probing with appID == "" must get an empty
// list, not every account-wide preset of every account. The pg
// path errors on the empty uuid; the memstore path silently
// returned matches. The Store boundary now rejects on both
// sides — empty appID is a programming error at the apid
// boundary, not a meaningful query.
func TestMemStore_CorsPreset_ListForApp_RejectsEmptyAppID(t *testing.T) {
	m, ctx, acct, _ := corsFixture(t)

	memSeedCorsPreset(m, memSampleCorsPreset(acct, "", "wide"))

	list, err := m.ListCorsPresetsForApp(ctx, acct, "")
	if err != nil {
		t.Fatalf("ListCorsPresetsForApp(empty appID): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0 (account-wide presets not surfaced by the app-scoped query)", len(list))
	}
}
