package state_test

// MemStore parity for the top-level validate_mode column
// (ADR-128 §D1). The pgstore companion tests at
// pgstore_edge_rules_test.go cover the SQL boundary; this file
// pins the in-memory mirror so the gateway handler can rely on
// the same value across both stores without an integration test.
//
// The memstore preserves ValidateMode verbatim on Create
// (memstore.go:CreateEdgeRule); the SQL coalesce to 'block'
// lives only in pgstore. The gateway-side loader
// (cmd/gatewayd-internal/edge_rules.go:1552) handles the
// "empty in both columns" sentinel by coercing to 'block' at
// handler.go:2694 — same end-state as the SQL coalesce.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// memSampleValidateRuleParams is the kind=validate mirror of
// pgSampleValidateRuleParams. The ValidateMode field exercises
// the top-level column path.
func memSampleValidateRuleParams(accountID, appID, host, mode string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:    accountID,
		AppID:        appID,
		MatchHost:    host,
		MatchPath:    "/",
		MatchMethods: []string{"POST"},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindValidate,
		ValidateMode: mode,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindValidate,
			Validate: &state.EdgeRuleValidateAction{
				Schema: []byte(`{"type":"object","required":["x"]}`),
			},
		},
	}
}

// memEdgeRuleSeedAccount stands up an account + app for the
// memstore tests. Mirrors pgEdgeRuleSeedAccount but uses the
// in-memory API surface.
func memEdgeRuleSeedAccount(t *testing.T, m *state.MemStore, ctx context.Context, plan api.Plan, suffix string) (acctID, appID string) {
	t.Helper()
	acct, err := m.CreateAccount(ctx, "edge-rules-mem-"+suffix+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", suffix, err)
	}
	app, err := m.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "edge-rules-mem-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", suffix, err)
	}
	return acct.ID, app.ID
}

// TestMemStore_EdgeRule_ValidateModeTopLevelRoundTrip mirrors
// the pgstore test of the same name. Create with 'warn' →
// GetByID returns 'warn' → Update to 'observe' → GetByID
// returns 'observe'.
func TestMemStore_EdgeRule_ValidateModeTopLevelRoundTrip(t *testing.T) {
	m, ctx := state.NewMemStore(), context.Background()
	acct, app := memEdgeRuleSeedAccount(t, m, ctx, api.PlanPro, "vt-rt")

	created, err := m.CreateEdgeRule(ctx, memSampleValidateRuleParams(acct, app, "memrt.example.com", "warn"))
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ValidateMode != "warn" {
		t.Errorf("after create ValidateMode = %q, want %q", created.ValidateMode, "warn")
	}

	got, err := m.GetEdgeRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEdgeRuleByID: %v", err)
	}
	if got.ValidateMode != "warn" {
		t.Errorf("GetByID ValidateMode = %q, want %q", got.ValidateMode, "warn")
	}

	observe := "observe"
	updated, err := m.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{ValidateMode: &observe})
	if err != nil {
		t.Fatalf("UpdateEdgeRule: %v", err)
	}
	if updated.ValidateMode != "observe" {
		t.Errorf("after Update ValidateMode = %q, want %q", updated.ValidateMode, "observe")
	}
}

// TestMemStore_EdgeRule_ValidateModeEmptyStaysEmpty pins the
// memstore's verbatim-stash behaviour: the SQL coalesce lives
// at pgstore only. Empty ValidateMode round-trips as empty
// here; the gateway handler's fall-through coerce at
// handler.go:2694 normalises to 'block' on the apply side.
// This divergence is intentional — the memstore is a hot-path
// mirror for tests, not a SQL substitute.
func TestMemStore_EdgeRule_ValidateModeEmptyStaysEmpty(t *testing.T) {
	m, ctx := state.NewMemStore(), context.Background()
	acct, app := memEdgeRuleSeedAccount(t, m, ctx, api.PlanPro, "vt-empty")

	created, err := m.CreateEdgeRule(ctx, memSampleValidateRuleParams(acct, app, "memempty.example.com", ""))
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ValidateMode != "" {
		t.Errorf("empty ValidateMode stored as %q, want \"\" (memstore preserves verbatim; pgstore would coalesce to 'block')", created.ValidateMode)
	}
}

func TestMemStore_EdgeRule_ManifestKeyUniquePerApp(t *testing.T) {
	m, ctx := state.NewMemStore(), context.Background()
	acct, app := memEdgeRuleSeedAccount(t, m, ctx, api.PlanPro, "manifest-key")
	params := memSampleValidateRuleParams(acct, app, "manifest-key.example.com", "block")
	params.ManifestKey = "async-route:create-report"
	created, err := m.CreateEdgeRule(ctx, params)
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ManifestKey != params.ManifestKey {
		t.Fatalf("ManifestKey = %q, want %q", created.ManifestKey, params.ManifestKey)
	}
	if _, err := m.CreateEdgeRule(ctx, params); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate manifest key error = %v, want ErrConflict", err)
	}
}
