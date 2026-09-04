package main

import (
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// newForwardBudgetConfig builds the reqbudget middleware config for
// the public forward route.
//
// ADR-093 amendment. gatewayd-public stamps a BACKSTOP, not a policy.
// It does not resolve the app, so it cannot see a kind=budget rule,
// and reqbudget derives a CHILD budget from whatever is already on
// the context — so whatever this daemon stamps becomes the parent of
// every downstream budget and can only be tightened, never widened,
// by the hop that does resolve the app.
//
// The original PR-B wiring used api.RequestBudgetDefault (3 s) next
// to a comment saying budgets "come from the edge-rule kind=budget
// match (resolved deeper in the chain)". The deeper match runs in
// gatewayd-internal's applyEdgeRuleBudget, one hop later, so it
// inherited a 3 s parent. Measured on the europe-west3 deployment
// with a 25 s rule confirmed applied (budget_ms:25000, source:rule on
// 713/1000 requests): upstream latency still capped at p50 2948 /
// p90 3000 / p99 3013 / max 3121 ms. No customer could raise their
// request budget by any means.
//
// Every request this daemon forwards lands on a hop that stamps its
// own authoritative budget — controlPlaneProxy routes apid paths to
// apid (api.RequestBudgetApidDefault) and everything else to
// gatewayd-internal, whose applyEdgeRuleBudget always stamps (rule
// match, else plan default) and owns the 504 +
// request_budget_exceeded envelope. So the edge defers to the owner
// and keeps only the platform ceiling as a liveness guard: a wedged
// downstream is still cut at RequestBudgetMax, so a public
// connection can never be pinned indefinitely.
//
// This also subsumes the previous sync-invoke DefaultFor carve-out,
// which returned exactly this value for the same reason (apid owns
// its own plan-aware wait), so no DefaultFor is needed.
func newForwardBudgetConfig(metrics *reqbudget.M, log *slog.Logger) (reqbudget.MiddlewareConfig, error) {
	return reqbudget.NewMiddlewareConfig(reqbudget.MiddlewareConfig{
		Default: api.RequestBudgetMax,
		Max:     api.RequestBudgetMax,
		Route:   "forward",
		Metrics: metrics,
		Log:     log,
	})
}
