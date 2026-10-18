// handlers_obs_on_behalf_of.go — operator-as-tenant view helper (P1).
//
// When an admin (caller) passes ?on_behalf_of=<uuid-or-slug> on a
// per-app observability endpoint, the handler reads the target's
// data using the target's plan (Free→402 still applies even when
// the caller is an admin) and emits an operator.action.view audit
// row keyed on the target's account id with the caller captured as
// the actor. The caller MUST be in the admin allowlist — same
// precedent as s.adminAllows(acct) at handlers_admin_billing.go:117
// and compute_nodes.go:147. There is no per-target gate: an admin
// in the allowlist can read any account's data.
//
// The helper is called at the TOP of every per-app handler that
// participates in P1; the handler substitutes `target` for `acct`
// in its body so the plan gate, loadApp, and downstream reads all
// flow through the target's identity. The caller is preserved in
// the closure (the audit emit captures both).
//
// Idempotency: an absent `on_behalf_of=` returns target=nil; the
// handler proceeds unchanged (current behaviour).
//
// Edge cases:
//
//   - empty on_behalf_of  → target=nil (no-op)
//   - slug not found      → 404 (not 400; 400 leaks slug existence)
//   - UUID not parseable  → 404 (same posture)
//   - caller not in admin allowlist → 403 admin_required
//   - target is the caller → target=nil (treat as no-op; admin
//     reading their own data is the normal customer path)
//   - target's plan is Free → 402 propagates from handler gate
package apidserver

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolveOnBehalfOf parses ?on_behalf_of= from the URL, resolves
// the target account (UUID first, slug fallback), gates through
// s.adminAllows(target), and writes the appropriate RFC 7807
// problem on any error. Returns (nil, nil) when on_behalf_of is
// absent or resolved to the caller.
//
// The endpoint parameter is the route name stamped on the
// operator.action.view audit row (e.g. "metrics", "usage",
// "wake-timeline", "errors-summary"). When target is nil the
// endpoint string is unused.
//
// The caller is captured for the audit emit so the post-response
// emit captures both the caller id (actor) and the target id
// (account_id on the audit row). The caller param is unused when
// on_behalf_of is absent.
func (s *server) resolveOnBehalfOf(w http.ResponseWriter, r *http.Request, caller state.Account, endpoint string) (*state.Account, bool) {
	raw := r.URL.Query().Get("on_behalf_of")
	if raw == "" {
		return nil, true
	}
	var target state.Account
	var err error
	if uid, perr := uuid.Parse(raw); perr == nil {
		// Fast path: caller passed a UUID. One round-trip.
		target, err = s.store.AccountByID(r.Context(), uid.String())
	} else {
		// Slug path: resolve via the apps table (the slug is the
		// per-app observability endpoint's URL slug too, so the
		// app's AccountID gives us the target). Two round-trips
		// (AppBySlug → AccountByID) is acceptable here because
		// the on_behalf_of=slug branch is the slow human-friendly
		// path; an operator scripting the API uses UUIDs.
		app, aerr := s.store.AppBySlug(r.Context(), raw)
		if aerr != nil || app.ID == "" {
			err = aerr
			if err == nil {
				err = state.ErrNotFound
			}
		} else {
			target, err = state.AppResourceAccount(r.Context(), s.store, app)
		}
	}
	if err != nil || target.ID == "" {
		// 404 — slug-leak guard. An admin probing a non-existent
		// slug gets the same 404 as a customer probing the same
		// slug (the byte-identical posture enforced by loadApp).
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"target not found", "no account matches the on_behalf_of value"))
		return nil, false
	}
	if target.ID == caller.ID {
		// Admin reading their own data — same path as a regular
		// customer. Don't stamp an audit row (the customer-facing
		// handler will not emit pii.accessed either).
		return nil, true
	}
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return nil, false
	}
	return &target, true
}
