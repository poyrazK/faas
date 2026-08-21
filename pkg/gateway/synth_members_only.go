package gateway

// synth_members_only.go — ADR-123 ingress-control gate for
// the cron-fired wake path. Schedd cron fires /v1/synthesize,
// /v1/invocations:dispatch, and /v1/invocations:dispatch_batch
// (synth.go:301, 373, ???) which dispatch directly to
// s.dispatcher.Wake — bypassing Handler.ServeHTTP and therefore
// bypassing applyIngressMembersOnly (handler.go). Without this
// gate, a /v1/synthesize request for a members_only app would
// wake the underlying Firecracker microVM on cron even though
// no human session is involved at the wake-source.
//
// The Handler-side gate covers the HTTP-front-door path; this
// synth-side gate closes the cron bypass, exactly the same
// design gap ADR-119 surfaced and closed at internal_svc_auth.go
// for the internal_only mode. Cron has no human session, so
// every cron wake against a members_only app is denied by the
// gate (403) BEFORE the wake call reaches the dispatcher —
// customers who want cron-receivable member-gated apps must
// use `open` or `internal_only` (see plan
// docs/adr/120-app-public-auth-members-only.md §3 "Synth
// cron").
//
// The gate reuses the same OrgMemberChecker + CookiePrincipalExtractor
// as the HTTP-front-door gate (cmd/gatewayd-internal/run.go
// constructs one of each and wires into both Handler and
// SynthServer). Single source of truth on the membership
// predicate AND the cookie resolution.
//
// Audit redaction invariant (carry-over from
// public_auth_members_only.go): the cookie envelope / session
// id MUST NEVER appear in an audit payload. The extractor
// returns only the account_id; only that + app_id + from +
// a short reason enum flow into the audit row.

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
)

// WithMembersOnlyChecker wires the OrgMemberChecker into the
// SynthServer. Mirrors the Handler-side WithMembersOnlyChecker
// (public_auth_members_only.go). Called from
// cmd/gatewayd-internal/run.go after the bridge is
// constructed. nil = the gate is disabled; a members_only
// request that reaches handleSynthesize with no checker
// wired would 500 (operator_error), the same loud posture
// as the HTTP-front-door side.
func (s *SynthServer) WithMembersOnlyChecker(c authz.OrgMemberChecker) {
	s.membersOnlyChecker = c
}

// WithMembersOnlyPrincipalExtractor wires the
// cookie-side bridge into the SynthServer. Mirrors the
// Handler-side WithMembersOnlyPrincipalExtractor
// (public_auth_members_only.go). nil = the cookie side
// is disabled; the gate 500s rather than silently
// letting every members_only cron request through.
// same defence-in-depth posture as
// WithMembersOnlyChecker — they share the "the
// gate refuses to silently pass" contract.
func (s *SynthServer) WithMembersOnlyPrincipalExtractor(e CookiePrincipalExtractor) {
	s.membersOnlyPrincipal = e
}

// WithAppOrgIDLookup wires the per-app OrgID resolution into
// the SynthServer. nil = every members_only cron request
// 500s with the operator_error "misconfig" posture (same
// shape as the HTTP-front-door side's empty-orgid branch).
// Production wires the same per-app cache the Handler
// consults (cmd/gatewayd-internal/run.go) so a cache miss
// returns "" which the gate treats as "misconfig" for
// members_only mode only — public-mode apps retain their
// pre-existing wake behaviour bit-for-bit. The lookup
// takes a context so the request's ctx (with timeout /
// cancel chain) flows into the per-app store call.
func (s *SynthServer) WithAppOrgIDLookup(lookup func(ctx context.Context, appID string) string) *SynthServer {
	s.appOrgID = lookup
	return s
}

// applyIngressMembersOnly is the synth-side analogue of
// Handler.applyIngressMembersOnly. Reads the inbound
// request's cookie-stamped principal (cron has none —
// every cron-fired wake fails the cookie check at the
// extractor and surfaces a 403), looks up the app's
// org_id via the per-app cache, and verifies the cookie
// principal is an active member of the org.
//
// Cron is the dominant case (no cookie), so the
// cookie-fail path returns 403 with
// reason="no_cookie_principal" (cron fires from the
// scheduler daemon, not a human client). A future
// human-fired /v1/invocations:dispatch /
// /v1/synthesize (e.g. a curl from the dashboard's
// "trigger wake" button) WOULD carry a cookie — and
// that case defers to the membership predicate.
//
// The mode lookup reads from the per-app cache
// (populated by the same hydration path that feeds
// Handler.applyIngressMembersOnly). The `from`
// argument tags the audit payload so dashboards can
// split the three call surfaces. The three current
// values:
//   - "synth"           — handleSynthesize (legacy wake-only path)
//   - "synth_dispatch"  — handleInvocationDispatch (Move 1 single
//     invocation envelope)
//   - "synth_batch"     — handleInvocationDispatchBatch (Move 1 batch)
//
// Failure modes are identical to the HTTP-side gate:
//   - Mode != 'members_only' → no-op (return false)
//   - No checker wired → 500 operator_error (misconfig)
//   - No org_id for app_id → 500 operator_error (misconfig)
//   - No cookie principal → 403 (cron denied, no audit exception)
//   - Principal not a member → 403 (rare; the human-fired
//     dashboard curl hits this if the user is signed in
//     as a non-member of the org)
//   - DB lookup error → 403 (fail-closed posture, reason=lookup_error)
//
// Pass-through (return false) → the dispatcher.Wake
// proceeds; member-gated apps only wake for human-fired
// /v1/synthesize calls (a future /v1/invocations:dispatch
// from a dashboard's "trigger wake" button is the only
// legitimate path).
func (s *SynthServer) applyIngressMembersOnly(w http.ResponseWriter, r *http.Request, appID, mode, from string) bool {
	if mode != publicAuthModeMembersOnly {
		return false
	}
	if s.membersOnlyChecker == nil || s.membersOnlyPrincipal == nil {
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"app is misconfigured",
			"members_only mode requires the cookie principal extractor and the org-membership checker to be wired at startup"))
		return true
	}
	if s.appOrgID == nil {
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"app is misconfigured",
			"members_only mode requires the per-app OrgID lookup"))
		return true
	}
	orgID := s.appOrgID(r.Context(), appID)
	if orgID == "" {
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"app is misconfigured",
			"members_only mode requires a non-empty apps.org_id"))
		return true
	}
	accountID, ok := s.membersOnlyPrincipal.FromRequest(r)
	if !ok || accountID == "" {
		// Cron has no cookie; this is the dominant path
		// (every /v1/synthesize from the schedd cron
		// driver carries no faas_sid). 403 is the
		// correct surface; the audit row carries
		// from="synth" / "synth_dispatch" /
		// "synth_batch" so the operator's dashboard
		// distinguishes a cron-fired denial from a
		// human-fired one.
		if s.synthAuditEmit != nil {
			s.synthAuditEmit(r.Context(), "instances.public_auth_members_blocked", nil, map[string]any{
				"app_id": appID,
				"from":   from,
				"reason": "no_cookie_principal",
			})
		}
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden,
			"members-only mode requires a valid session cookie",
			"this app is reachable only by organization members via a logged-in Gregale session; cron cannot trigger member-gated wakes"))
		return true
	}
	member, err := s.membersOnlyChecker.IsOrgMember(r.Context(), orgID, accountID)
	if err != nil {
		if errors.Is(err, authz.ErrMembershipLookup) {
			if s.synthAuditEmit != nil {
				s.synthAuditEmit(r.Context(), "instances.public_auth_members_blocked", nil, map[string]any{
					"app_id":           appID,
					"from":             from,
					"reason":           "lookup_error",
					"actor_account_id": accountID,
				})
			}
			if s.metrics != nil {
				s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
				s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
			}
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
				api.CodeForbidden,
				"members-only org membership lookup failed",
				"could not resolve the org membership row; try again"))
			return true
		}
		// Unexpected non-lookup error — 500; the
		// HTTP-side gate handles this branch
		// identically.
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"members lookup failed",
			"members_only mode could not resolve the membership row"))
		return true
	}
	if !member {
		if s.synthAuditEmit != nil {
			s.synthAuditEmit(r.Context(), "instances.public_auth_members_blocked", nil, map[string]any{
				"app_id":           appID,
				"from":             from,
				"reason":           "not_member",
				"actor_account_id": accountID,
			})
		}
		if s.metrics != nil {
			s.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			s.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden,
			"Not a member",
			"this account is not an active member of the organization that owns this app"))
		return true
	}
	if s.metrics != nil {
		s.metrics.ObserveEdgeRuleMatch("ingress_members", "match")
		s.metrics.ObserveEdgeRuleApply("ingress_members", "success")
	}
	return false
}
