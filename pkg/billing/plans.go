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

// WireQuantityMillicentsPerGBHour is the integer scale factor that converts
// GB-RAM-hours into the wire quantity both providers' metered surfaces
// accept. Stripe's metered subscription_item takes an int64 quantity;
// Paddle's per-line-item Quantity is the same integer axis multiplied by the
// overage price handle. 1000 wire units = 1 GB-RAM-hour = 1 cent at the
// €0.01/GB-h overage rate (CLAUDE.md "Overage €0.01/GB-h").
//
// Defined here (pkg/billing) rather than in pkg/billing/stripe because the
// unit is a financial-model constant — the float-to-int truncation that
// closed the M7 acceptance gate's < 0.1 % delta applies equally to both
// providers, and the value is pinned by TestWireQuantityConstants below.
const WireQuantityMillicentsPerGBHour int64 = api.OverageMillicentsPerGBHour

// SecondsPerGBHour is the integer conversion factor for mb_seconds →
// GB-RAM-hours: 1 MB resident for 1 second = 1/(1024*3600) GB-h. The
// product is exact integer arithmetic — no float — so the wire quantity is
// deterministic across architectures and rounding modes.
//
// Exported so pkg/billing/stripe and any future provider can pin the
// shared value in their own tests without re-declaring it.
const SecondsPerGBHour int64 = api.SecondsPerGBHour

// WireQuantityForMBSeconds converts a summed mb_seconds window into the
// integer wire quantity both providers' metered surfaces accept.
//
//	qty = mbSeconds * WireQuantityMillicentsPerGBHour / secondsPerGBHour
//
// Pure integer math — no float, no per-hour truncation loss. The formula
// was extracted from the SDK-touching push path (pkg/billing/stripe/usage.go)
// so the money-critical conversion is pinned by a hermetic unit test (no
// live Stripe / Paddle). The canonical acceptance case is a 256 MB Hobby
// app (billed at ram+8 = 264 MB) resident for a full 24 h:
// 264 * 60 * 60 * 24 = 22_809_600 mb-s → 6187 wire units.
//
// Truncation is by design — the sub-milliunit remainder is dropped exactly
// the way the spec's integer money model requires (CLAUDE.md: "Floats near
// money fail review"). Range guard: the largest billable window under
// spec §4.7 is a 1 TB instance resident for 24 h = ~2.1e9 mb_seconds, so
// mbSeconds * 1000 ≈ 2.1e12, well below int64 max (~9.2e18).
func WireQuantityForMBSeconds(mbSeconds int64) int64 {
	return mbSeconds * WireQuantityMillicentsPerGBHour / SecondsPerGBHour
}
