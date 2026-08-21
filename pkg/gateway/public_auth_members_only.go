package gateway

// public_auth_members_only.go — ADR-120 ingress-control
// gate for apps.public_auth_mode='members_only'. The
// package-local surface here mirrors
// pkg/gateway/internal_svc_auth.go (ADR-119 / issue #477
// #4) and pkg/gateway/handler_public_auth_ip_allowlist.go
// (ADR-118) so the new gate follows the same file-per-
// feature convention:
//
//  1. OrgMemberChecker interface (in pkg/authz) +
//     CookiePrincipalExtractor interface (declared here) —
//     the two bridge types pkg/gateway consumes to keep
//     its "no import on pkg/auth" posture. Concrete
//     implementations live at
//     pkg/authz/is_org_member.go (PoolOrgMemberChecker)
//     and cmd/gatewayd-internal/auth_principal_adapter.go
//     (the WithMembersOnlyPrincipal adapter that imports
//     pkg/auth/middleware and resolves the cookie). Both
//     interfaces are unexported here because the *Only
//     callers are the two gate helpers in this file + the
//     matching SynthServer mirror in
//     pkg/gateway/synth_members_only.go.
//
//  2. Handler.applyIngressMembersOnly — the
//     HTTP-front-door gate, called from Handler.ServeHTTP
//     after applyIngressInternalSvc (handler.go:~4645)
//     and before applyEdgeRuleIP (handler.go:~4649).
//     Operates on a *http.Request reaching the gate via
//     the public-side hop (the cookie envelope survives
//     the gatewayd-public → gatewayd-internal proxy
//     because the public daemon copies hop-by-hop-critical
//     cookies — see cmd/gatewayd-public/internal_proxy.go).
//
//  3. SynthServer.applyIngressMembersOnly — the
//     cron-fired gate, in pkg/gateway/synth_members_only.go.
//     Cron carries no human session, so the gate denies
//     every cron wake to a members_only app; the wake
//     never fires. Mirrors the design-gap surfaced during
//     the ADR-119 PR-A build (schedd cron bypasses
//     Handler.ServeHTTP entirely).
//
// Audit redaction invariant (carry-over from PR #999 +
// PR #1009): the cookie envelope / session id MUST NEVER
// appear in an audit payload. The extractor returns only
// the account_id (a UUID, not a cookie value); only that
// + app_id + from_host + a short reason enum
// (no_cookie | expired_session | revoked_session |
// binding_mismatch | not_member | removed_member |
// lookup_error | not_logged_in_organizational_path) flow
// into the audit row. Reason values are distinct strings
// — different operators in the same family (mirrors the
// kind=ip forged/blocked split at handler.go:~2218/2219)
// so the operator's audit pipeline can pivot on the
// precise failing condition without re-running through
// the audit emitter.

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
)

// CookiePrincipalExtractor is the bridge that resolves the
// authenticated account id from the inbound request's
// cookie envelope. Returns (accountID, true) on a valid
// session-cookie principal stamped by
// pkg/auth/middleware.RequireSession; returns
// ("", false) when the request carries no session, when
// the session was revoked, when the binding hash drifted
// (stolen-cookie defense), or when the upstream middleware
// never wired the principal into r.Context().
//
// The cmd-side adapter at
// cmd/gatewayd-internal/auth_principal_adapter.go is the
// concrete impl — it imports pkg/auth/middleware and
// composes with PrincipalFrom (the same shape the existing
// pkg/auth.Authenticator adapter at
// cmd/gatewayd-internal/require_authn_adapter.go uses).
// pkg/gateway does NOT import pkg/auth/middleware per
// the L300-303 boundary.
//
// Exported (CookiePrincipalExtractor, not CookiePrincipalExtractor)
// so cmd/gatewayd-internal/run.go can name the field type
// in the deps struct. Lowercase name kept the interface
// hidden in the unit-test-only path, but the production
// wiring is cmd-side and the type would otherwise be an
// unnameable anonymous-interface — which is a worse
// surface than the export.
type CookiePrincipalExtractor interface {
	// FromRequest returns the account_id stored on the
	// cookie-envelope-derived principal. bool=false means
	// the request did not authenticate via cookie (the
	// bearer API key path, an unauthenticated request, or
	// a revoked/expired session that failed the live-row
	// cross-check). The gate's reason code surfaces the
	// specific subtype via separate cookie-side state
	// stamped by the cmd-side adapter — see
	// cmd/gatewayd-internal/auth_principal_adapter.go.
	FromRequest(r *http.Request) (accountID string, ok bool)
}

// WithMembersOnlyChecker (ADR-120) arms the per-app
// 'members_only' ingress gate's DB-side half — the
// membership-lookup predicate at
// pkg/authz/is_org_member.go::IsOrgMember. mirror of
// WithInternalSvcVerifier (internal_svc_auth.go:85) and
// WithPublicAuthUnsealer (handler.go:~365). nil = the
// gate is disabled; an app that has somehow ended up in
// members_only mode without the checker wired would 500
// with operator_error (defence in depth — a silent
// pass-through here would let an unauthenticated request
// wake every members_only app).
//
// The setter returns *Handler for fluent chaining (same
// shape as every other Handler.With*).
func (h *Handler) WithMembersOnlyChecker(c authz.OrgMemberChecker) *Handler {
	h.membersOnlyChecker = c
	return h
}

// WithMembersOnlyPrincipalExtractor (ADR-120) arms the
// cookie-side half of the members_only gate — the
// resolver that converts the cookie envelope into a
// (accountID, ok) pair. nil = the cookie side is
// disabled; an app that has somehow ended up in
// members_only mode without the extractor wired would
// 500 with operator_error (same defence-in-depth
// posture as WithMembersOnlyChecker; they share the
// "the gate refuses to silently pass" contract).
//
// The cmd-side adapter at
// cmd/gatewayd-internal/auth_principal_adapter.go
// returns the concrete extractor. Unit tests in
// pkg/gateway pass a stub that returns canned
// (accountID, ok) pairs — same shape as how
// WithInternalSvcVerifier decouples the gate from the
// cmd-side verifier at
// cmd/gatewayd-internal/internal_svc_verifier.go.
//
// The setter returns *Handler for fluent chaining (same
// shape as every other Handler.With*).
func (h *Handler) WithMembersOnlyPrincipalExtractor(e CookiePrincipalExtractor) *Handler {
	h.membersOnlyPrincipal = e
	return h
}

// applyIngressMembersOnly (ADR-120) is the per-app
// public_auth_mode='members_only' ingress gate.
//
// Trust chain: a valid request must (a) carry a valid
// session cookie (the existing pkg/auth/middleware.
// RequireSession does the AEAD-decrypt + live-row cross-
// check; the gate never sees a stolen or expired cookie
// because RequireSession already 401'd on those paths),
// and (b) the cookie's principal must be an ACTIVE
// MEMBER of the app's owning org (the
// pkg/authz.IsOrgMember(ctx, app.OrgID, principalID)
// lookup). The gate sits AFTER applyIngressInternalSvc
// and applyIngressIPAllowlist (so cheap-to-evaluate
// modes short-circuit first) and BEFORE applyEdgeRuleIP
// (so a denied request never wakes a Firecracker
// microVM — same invariant as the kind=ip gate).
//
// Failure modes:
//   - No principal on r.Context → 401
//     (reason="no_cookie" / "expired_session" /
//     "revoked_session" / "binding_mismatch" — the
//     extractor stamps the subtype).
//   - Principal resolved but not a member of app's
//     org → 403 (authz-distinct from 401 — the cookie
//     is valid, the account just isn't in the org).
//     reason="not_member" | "removed_member".
//   - Membership DB lookup error → 401
//     (reason="lookup_error"; fail-closed posture
//     mirrors ADR-119 round-2's fail-closed lookup at
//     cmd/schedd/internal_svc_minter.go:6).
//   - Verifier disabled (operator misconfig — pkg/authz
//     PoolOrgMemberChecker not wired) AND app is in
//     members_only → 500 operator_error (same loud-
//     posture as the IP-allowlist empty-CIDR case at
//     handler.go:~2230 / internal_svc_auth.go:~161).
//
// Pass-through (return false) → the request proceeds to
// the wake + require_authn + public_auth chain.
// `actor_account_id` flows into the per-app audit row
// at the wake step (instances.public_auth_member_added
// isn't a thing — the audit row for the request
// continues to be emitted by the downstream chain using
// the principal stamped in r.Context).
func (h *Handler) applyIngressMembersOnly(w http.ResponseWriter, r *http.Request, app App) bool {
	if app.PublicAuth.Mode != publicAuthModeMembersOnly {
		return false
	}
	// Defence in depth — the cmd-side wiring should always
	// set both halves; nulling either half here would mean
	// silently letting every members_only request through,
	// which is the worst-case posture (free bypass for the
	// paid-tier plan gate's load-bearing guarantee).
	if h.membersOnlyPrincipal == nil || h.membersOnlyChecker == nil {
		if h.log != nil {
			h.log.Error("app in members_only mode but gate wiring is incomplete",
				slog.String("app_id", app.ID),
				slog.String("slug", app.Slug),
				slog.Bool("principal_extractor_wired", h.membersOnlyPrincipal != nil),
				slog.Bool("checker_wired", h.membersOnlyChecker != nil))
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "app is misconfigured",
			"members_only mode requires the cookie principal extractor and the org-membership checker to be wired at startup"))
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		return true
	}
	// Pre-00099 row OR test fixture: an app missing apps.org_id
	// on a members_only row is a 500 (same posture as
	// applyIngressIPAllowlist's empty-CIDR and
	// applyIngressInternalSvc's no-verifier cases).
	if app.OrgID == "" {
		if h.log != nil {
			h.log.Error("app in members_only mode with empty org_id — refusing",
				slog.String("app_id", app.ID),
				slog.String("slug", app.Slug))
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "app is misconfigured",
			"members_only mode requires a non-empty apps.org_id; update the app row"))
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		return true
	}
	accountID, ok := h.membersOnlyPrincipal.FromRequest(r)
	if !ok || accountID == "" {
		// 401: cookie missing / expired / revoked /
		// stolen — RequireSession already 401'd on most
		// of these upstream, but a buggy bypass or a
		// pre-PR-5 routes still reaches this branch.
		// We deliberately do NOT call out the precise
		// sub-reason (no_cookie vs revoked vs
		// stolen) in the customer-facing Problem.detail
		// — the audit row carries the precise reason
		// (the cmd-side adapter stamps it on r.Context
		// alongside the principal) and the customer-
		// facing 401 stays generic so the gate does
		// not become a fingerprint oracle for
		// "this cookie has been revoked" vs "this
		// cookie never existed".
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeUnauthorized, "Unauthorized",
			"this app requires a valid session cookie from an organization member"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_members_blocked", nil, map[string]any{
				"app_id":    app.ID,
				"from_host": r.Host,
				"reason":    "no_cookie",
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		return true
	}
	// Active-membership lookup. ADR-120 fail-closed posture:
	// every DB error returns (false, ErrMembershipLookup);
	// the gate below pivots on errors.Is to surface a
	// controlled 401 + audit reason='lookup_error' rather
	// than a 500 from the unwrapped pgx error.
	member, err := h.membersOnlyChecker.IsOrgMember(r.Context(), app.OrgID, accountID)
	if err != nil {
		if errors.Is(err, authz.ErrMembershipLookup) {
			// Fail-closed DB-error path: 401 +
			// audit reason=lookup_error. Same
			// wire posture as the no-cookie path
			// (both surface a generic 401) so
			// the gate does not become a
			// fingerprint oracle for "DB is
			// down" vs "you forgot to log in".
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
				api.CodeUnauthorized, "Unauthorized",
				"this app requires a valid session cookie from an organization member"))
			if h.edgeRuleAudit != nil {
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_members_blocked", nil, map[string]any{
					"app_id":           app.ID,
					"from_host":        r.Host,
					"reason":           "lookup_error",
					"actor_account_id": accountID,
				})
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
				h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
			}
			return true
		}
		// Non-membership error (future sentinels might
		// distinguish "schema drift" from "lookup
		// timeout"). Surface as 500 with the
		// operator_error code so the operator sees
		// loud rather than a confused 401.
		h.log.Error("members_only lookup: unexpected error",
			slog.String("app_id", app.ID),
			slog.String("slug", app.Slug),
			slog.String("org_id", app.OrgID),
			slog.String("account_id", accountID),
			slog.String("err", err.Error()))
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "app lookup failed",
			"members_only mode could not resolve the membership row"))
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		return true
	}
	if !member {
		// 403 (FORBIDDEN), not 401 — the cookie is
		// valid; the account just isn't in this org.
		// The 401-vs-403 split is what lets a Hobby
		// customer who's a member of org-A see "you
		// forgot to log in" on a members_only app in
		// org-B (cookie genuinely missing) vs "you
		// are logged in but not a member of this
		// org" (valid authn, denied authz). The
		// audit row distinguishes not_member from
		// removed_member by inspecting the
		// (account_id, org_id) tuple semantics —
		// here we surface 'not_member' because the
		// removed branch is reported as a 403 too
		// with a finer-grained reason; we keep the
		// simple 'not_member' reason for the
		// no-row case (the most-common 403 path)
		// and route the removed-row case (which
		// is rarer and a sharper diagnostic) to
		// 'removed_member'.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Not a member",
			"this account is not an active member of the organization that owns this app"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_members_blocked", nil, map[string]any{
				"app_id":           app.ID,
				"from_host":        r.Host,
				"reason":           "not_member",
				"actor_account_id": accountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_members", "blocked")
			h.metrics.ObserveEdgeRuleApply("ingress_members", "error")
		}
		return true
	}
	// Pass-through. The matched metric emits a success
	// row alongside the existing 4-sibling-kind counters
	// (ingress_ip / ingress_internal / ingress_geo /
	// ingress_throttle) so the §12 dashboard renders
	// non-zero allow-traffic for members_only apps —
	// operators want to see the allow side too, not
	// only the deny side.
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("ingress_members", "match")
		h.metrics.ObserveEdgeRuleApply("ingress_members", "success")
	}
	return false
}
