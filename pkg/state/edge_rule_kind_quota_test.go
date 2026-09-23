// adr: 201
package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func quotaFixture(t *testing.T, plan api.Plan) (*MemStore, Account, App, api.Limits) {
	t.Helper()
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, string(plan)+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "quota-" + string(plan)})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return m, acct, app, api.MustLimitsFor(plan)
}

func createKindRule(m *MemStore, acct Account, app App, kind EdgeRuleKind, limits api.Limits) error {
	_, err := m.CreateEdgeRuleIfUnderQuota(context.Background(), CreateEdgeRuleParams{
		AccountID: acct.ID,
		AppID:     app.ID,
		MatchHost: "h.example",
		Kind:      kind,
		Enabled:   true,
		Action:    EdgeRuleAction{Kind: kind},
	}, limits)
	return err
}

// The load-bearing case: Free's quota of 0 must DENY, not skip.
func TestEdgeRuleKindQuota_FreeZeroDeniesRatherThanSkips(t *testing.T) {
	for _, kind := range []EdgeRuleKind{EdgeRuleKindCache, EdgeRuleKindRetry, EdgeRuleKindCircuitBreaker} {
		m, acct, app, limits := quotaFixture(t, api.PlanFree)
		err := createKindRule(m, acct, app, kind, limits)
		if err == nil {
			t.Fatalf("kind=%s: Free created a rule with a quota of 0; a zero quota must deny, not skip the check", kind)
		}
		var quotaErr *EdgeRuleQuotaError
		if !errors.As(err, &quotaErr) {
			t.Fatalf("kind=%s: err = %v, want *EdgeRuleQuotaError", kind, err)
		}
		if quotaErr.Limit != 0 || quotaErr.Kind != string(kind) {
			t.Fatalf("kind=%s: quota error = %+v, want Limit 0 and the matching kind", kind, quotaErr)
		}
		if !quotaErr.PerKind || !quotaErr.PerAppOnly {
			t.Fatalf("kind=%s: quota error = %+v, want PerKind and PerAppOnly so apid maps it to the per-kind problem code", kind, quotaErr)
		}
	}
}

func TestEdgeRuleKindQuota_AllowsUpToPlanLimitThenDenies(t *testing.T) {
	cases := []struct {
		plan  api.Plan
		kind  EdgeRuleKind
		limit int
	}{
		{api.PlanHobby, EdgeRuleKindCache, 1},
		{api.PlanPro, EdgeRuleKindCache, 5},
		{api.PlanHobby, EdgeRuleKindRetry, 3},
		{api.PlanPro, EdgeRuleKindRetry, 10},
		{api.PlanHobby, EdgeRuleKindCircuitBreaker, 3},
		{api.PlanScale, EdgeRuleKindCircuitBreaker, 25},
	}
	for _, tc := range cases {
		m, acct, app, limits := quotaFixture(t, tc.plan)
		for i := 0; i < tc.limit; i++ {
			if err := createKindRule(m, acct, app, tc.kind, limits); err != nil {
				t.Fatalf("%s/%s: rule %d of %d rejected: %v", tc.plan, tc.kind, i+1, tc.limit, err)
			}
		}
		err := createKindRule(m, acct, app, tc.kind, limits)
		if err == nil {
			t.Fatalf("%s/%s: rule %d exceeded the quota of %d but was accepted", tc.plan, tc.kind, tc.limit+1, tc.limit)
		}
		var quotaErr *EdgeRuleQuotaError
		if !errors.As(err, &quotaErr) {
			t.Fatalf("%s/%s: err = %v, want *EdgeRuleQuotaError", tc.plan, tc.kind, err)
		}
		if quotaErr.Limit != tc.limit || quotaErr.Observed != tc.limit {
			t.Fatalf("%s/%s: quota error = %+v, want Limit/Observed %d", tc.plan, tc.kind, quotaErr, tc.limit)
		}
	}
}

// The two kinds must have independent budgets — exhausting retry must not
// consume a customer's circuit-breaker allowance.
func TestEdgeRuleKindQuota_KindsAreIndependent(t *testing.T) {
	m, acct, app, limits := quotaFixture(t, api.PlanHobby)
	for i := 0; i < limits.EdgeRulesRetryPerApp; i++ {
		if err := createKindRule(m, acct, app, EdgeRuleKindRetry, limits); err != nil {
			t.Fatalf("retry rule %d rejected: %v", i+1, err)
		}
	}
	if err := createKindRule(m, acct, app, EdgeRuleKindRetry, limits); err == nil {
		t.Fatal("retry quota was not enforced")
	}
	if err := createKindRule(m, acct, app, EdgeRuleKindCircuitBreaker, limits); err != nil {
		t.Fatalf("circuit_breaker rejected after retry was exhausted: %v — the two kinds must have independent budgets", err)
	}
}

// Kinds this helper does not govern must be untouched by it, so the change
// cannot alter behaviour for any pre-existing kind.
func TestEdgeRuleKindQuota_LeavesOtherKindsAlone(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanFree)
	for _, kind := range []EdgeRuleKind{
		EdgeRuleKindRoute, EdgeRuleKindCORSA,
		EdgeRuleKindThrottle, EdgeRuleKindBudget, EdgeRuleKindGeo,
	} {
		if _, governed := edgeRuleKindQuota(kind, limits); governed {
			t.Fatalf("kind=%s is unexpectedly governed by the closed-zero helper", kind)
		}
		if denied := edgeRuleKindQuotaDenied(kind, limits); denied != nil {
			t.Fatalf("kind=%s was denied by the ADR-201 helper: %+v", kind, denied)
		}
		if exceeded := edgeRuleKindQuotaExceeded(kind, limits, 9999); exceeded != nil {
			t.Fatalf("kind=%s was rejected by the ADR-201 helper: %+v", kind, exceeded)
		}
	}
}
