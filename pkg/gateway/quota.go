// QuotaSnapshot — Finding 6 (issue #314).
//
// Dashboard-facing projection of one app's per-app rate-limit bucket.
// Backed by Limiter.Peek so the value reflects "what the next caller
// would see", not "what the last caller saw". Returns ok=false on
// the same conditions as Peek: nil/noop limiter, unknown plan, or a
// bucket that has never been Allow'd for this app.
//
// The control-listener endpoint (cmd/gatewayd/quota_handler.go)
// serialises this as JSON; the dashboard polls it via HTMX every 30 s.
// The same shape is also used by the gateway's response-header writer
// (Handler.writeRateLimitHeaders) so the JSON snapshot and the
// in-band headers agree on the same numbers within one request.
package gateway

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// QuotaSnapshot is one app's bucket state at the gateway edge. All
// integer fields are zero-valued when ok=false; the dashboard's "—"
// badge distinguishes this from a real "exhausted" reading
// (ok=true, remaining=0).
//
// The OK flag is always present in the JSON shape — the handler in
// cmd/gatewayd/quota_handler.go renders "ok":true / "ok":false
// literally on both the 200 response and the noop path so browser
// JS can do a single `body.ok` check without a fallback to a missing
// field. Distinct from omitting the field — which Go's encoding/json
// does NOT do for a value-typed bool — so the contract is "always
// present, callers ignore integer fields when ok is false".
type QuotaSnapshot struct {
	AppID        string `json:"app_id"`
	Plan         string `json:"plan"`
	Limit        int    `json:"limit"`
	Remaining    int    `json:"remaining"`
	ResetSeconds int    `json:"reset_seconds"`
	// OK is the contract flag: false on noop/unknown-plan/missing-bucket.
	// Always emitted in the JSON response (true OR false).
	OK bool `json:"ok"`
}

// NewQuotaSnapshot reads the per-app bucket for appID on plan and
// returns the snapshot. Plan is rendered as the plan name (api.Plan
// is a typed string with the closed vocabulary) so the dashboard can
// show the price tier without re-deriving from the app record (which
// the gateway hot path doesn't have without an extra Backend.Lookup).
// nil-safe on h.
func (h *Handler) QuotaSnapshot(appID string, plan api.Plan) QuotaSnapshot {
	snap := QuotaSnapshot{AppID: appID, Plan: string(plan)}
	if h == nil || h.limiter == nil {
		return snap
	}
	limit, remaining, resetSeconds, ok := h.limiter.Peek(appID, plan)
	if !ok {
		return snap
	}
	snap.Limit = limit
	snap.Remaining = remaining
	snap.ResetSeconds = resetSeconds
	snap.OK = true
	return snap
}
