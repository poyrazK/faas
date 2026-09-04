package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// TestForwardBudgetConfig_StampsBackstopNotPolicy is the regression
// guard for the ADR-093 amendment.
//
// gatewayd-public cannot resolve the app, so it cannot see a
// kind=budget rule. Whatever it stamps becomes the parent of the
// budget gatewayd-internal derives one hop later, and a child can
// only tighten. Stamping api.RequestBudgetDefault (3 s) here made
// every kind=budget rule unable to widen the effective budget — a
// 25 s rule still produced a ~3 s ceiling in production.
//
// If someone re-tightens this default, this test fails and points at
// the amendment rather than letting the regression ship silently.
func TestForwardBudgetConfig_StampsBackstopNotPolicy(t *testing.T) {
	metrics, err := reqbudget.NewMetrics(prometheus.NewRegistry(), "gateway")
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	cfg, err := newForwardBudgetConfig(metrics, slog.Default())
	if err != nil {
		t.Fatalf("newForwardBudgetConfig: %v", err)
	}
	if cfg.Default != api.RequestBudgetMax {
		t.Errorf("Default = %s, want %s (the platform ceiling as a liveness backstop; a tighter value here caps every downstream kind=budget rule — see ADR-093 amendment)",
			cfg.Default, api.RequestBudgetMax)
	}
	if cfg.Default == api.RequestBudgetDefault && api.RequestBudgetDefault != api.RequestBudgetMax {
		t.Errorf("Default is back to RequestBudgetDefault (%s) — this is the exact regression the amendment fixed", api.RequestBudgetDefault)
	}
	if cfg.Route != "forward" {
		t.Errorf("Route = %q, want \"forward\" (the §12 metric label contract)", cfg.Route)
	}
}

// TestChildBudgetCannotExceedParent pins the reqbudget mechanic that
// makes the above matter: a downstream hop deriving a LARGER budget
// than its parent still gets the parent's remaining time. This is
// why the public edge's value is a ceiling on every rule below it,
// and why the fix had to be at the edge rather than in the rule.
func TestChildBudgetCannotExceedParent(t *testing.T) {
	parentTotal := 3 * time.Second
	ctx, cancel, parent := reqbudget.WithRemaining(context.Background(),
		parentTotal, api.RequestBudgetMax, "forward", "GET:/x")
	defer cancel()

	// gatewayd-internal's applyEdgeRuleBudget effect: a 25 s rule.
	childCtx, childCancel, _ := parent.WithCeiling(ctx, 25*time.Second)
	defer childCancel()

	deadline, ok := childCtx.Deadline()
	if !ok {
		t.Fatal("child ctx carries no deadline")
	}
	if remaining := time.Until(deadline); remaining > parentTotal {
		t.Errorf("child deadline %s exceeds the 3s parent — if this ever passes, the clamping model changed and the edge backstop may no longer be load-bearing", remaining)
	}
}
