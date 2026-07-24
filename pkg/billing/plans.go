// Package billing is the canonical home for billing-provider shared
// helpers. Both pkg/billing/stripe and pkg/billing/paddle import it so
// plan-shape + financial-model values can't drift between providers.
//
// plans.go is the financial-model single source of truth for the
// providers. pkg/api/limits.go owns the Limits table; the helpers
// here are thin wrappers that delegate so the integer money values
// the providers post to Stripe/Paddle come from exactly one place in
// the repo.
package billing

import "github.com/onebox-faas/faas/pkg/api"

// PlanMonthlyMillicents returns the monthly subscription price for p in
// integer millicents. One cent is 1,000 millicents.
//
// pkg/api is the authoritative source for plan limits and financial-model
// values. Unknown plans return zero, matching api.LimitsFor's zero fallback.
// Do not use this as plan validation; that's api.Plan.Valid()'s job.
//
// Callers: pkg/billing/stripe/products.go (EnsurePlanProducts monthly
// Plan creation) and pkg/billing/paddle/products.go (ensureProducts
// monthly planPriceSpec).
func PlanMonthlyMillicents(p api.Plan) int64 {
	l, _ := api.LimitsFor(p)
	return l.PriceMillicents
}

// PlanOverageMillicentsPerGBHour returns the overage price per GB-RAM-hour
// in integer millicents.
//
// The rate is defined by pkg/api because it is part of the financial model
// (CLAUDE.md "Overage €0.01/GB-h" → 1_000 millicents). Stripe represents
// overage through a metered subscription; Paddle uses this value as the
// flat-rate monthly overage line-item price posted at month-rollover (see
// pkg/billing/paddle/usage.go).
func PlanOverageMillicentsPerGBHour() int64 {
	return api.OverageMillicentsPerGBHour
}
