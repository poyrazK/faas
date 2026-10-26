// Package writegate implements the request classification + cross-box
// leader resolution helpers that the Tier A9 / ADR-084 standby
// write-redirect layer inserts in front of cmd/gatewayd-internal's
// existing apidProxy (cmd/gatewayd-internal/proxy.go).
//
// # Why this package exists
//
// ADR-083 (active-passive HA, PR #738) ships lex-min leader election
// + DNS handoff, but defers the customer-intent write path:
// standbys currently accept writes and route them to local apid,
// which writes to local Postgres — invisible to the leader and
// creating data divergence. ADR-084 closes the deferral.
//
// The redirect logic is **pure with respect to its inputs**: given
// the same `*http.Request` + the same `LeaderResolver.Current()`
// answer, the gate picks one of the documented outcomes. PR-A ships
// the classification helpers and the LeaderResolver interface;
// PR-B wires them into the proxy with the cross-box mTLS hop.
// This file holds **no state of its own** — every state transition
// (cache TTL, dial retry, pg_notify subscriber) lives in PR-B.
//
// # Boundaries
//
// The package MUST NOT import cmd/gatewayd-internal (the proxy is
// downstream of this gate) or pkg/state (the gate sees a
// LeaderResolver, not the raw store). It MAY import pkg/gateway/leader
// for the existing LeaderStore — but only as an interface
// satisfaction hint; the concrete store plumbing lives in PR-B.
//
// # Failure modes (enumerated, addressed by PR-B)
//
//   - Cross-box dial fails → 307 fallback (see package-level docs
//     in the parent plan file noble-swimming-balloon.md §"Terminology")
//   - Election returns no leader → 503 Retry-After: 60
//   - Cookie write on standby → 503 Retry-After: 5 (cookie cross-box
//     requires shared PG, deferred per ADR-025 Tier 2)
//   - Inbound X-Faas-Forwarded-Leader → 400 redirect_loop
//     (loop-storm DoS guard)
//   - mTLS handshake fails → 503 Retry-After: 5 with distinct log
//   - mTLS_failure metric
//
// All five map to a single WithLabelValues(outcome, authKind) call
// against pkg/wire's `<prefix>_write_redirect_total` counter.
package writegate

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
)

// LoopGuardSentinel is the header name the writeGate sets on the
// outbound cross-box mTLS hop. It identifies the proxying node so
// the receiving leader can refuse a second redirect (an attacker
// who sets this header on an inbound request will be rejected with
// `400 redirect_loop` before any classification runs).
//
// The value MUST be the proxying node's `compute_nodes.name` — a
// stable, operator-readable identifier (NOT a UUID). PR-B's
// leader_url_publisher reads FAAS_NODE_NAME once at boot and reuses
// it for every outbound hop.
//
// The header is server-set ONLY on outbound hops. Clients cannot
// influence it. This is the load-bearing property: an attacker
// who can craft arbitrary headers but cannot defeat mTLS cannot
// trigger a redirect loop.
const LoopGuardSentinel = "X-Faas-Forwarded-Leader"

// WriteOutcome is the closed label set for the
// `<prefix>_write_redirect_total{outcome=...}` counter. Values
// outside this set must NOT be emitted at runtime — writeGate
// classifies at request entry (the value is bound at compile time
// by the branching below).
//
// "same_box" and "redirect_307" are NOT errors; the rest are. The
// PromQL `rate(gatewayd_internal_write_redirect_total{outcome=~"cookie_blocked|leader_unreachable|loop_prevented|mTLS_failure|error"}[5m])`
// is the §12 panel that fires when the redirect layer is unhealthy.
type WriteOutcome string

const (
	// OutcomeRelayed — the standby opened an outbound mTLS hop to
	// the leader's apid and copied the response verbatim. This is
	// the steady-state path; a healthy two-box fleet sees a
	// sustained non-zero rate proportional to write QPS.
	OutcomeRelayed WriteOutcome = "relayed"
	// OutcomeRedirect307 — either (a) the old leader detected it
	// is no longer the leader via LeaderResolver and returned a
	// 307 to the new leader URL, or (b) the cross-box dial failed
	// and the standby degraded to 307. Both are graceful recovery;
	// stdlib http.Client follows 30x with method+body preserved.
	OutcomeRedirect307 WriteOutcome = "redirect_307"
	// OutcomeSameBox — the request resolved to the local node (the
	// box IS the leader, OR the classification short-circuited to
	// local apidProxy). Always emitted; never an error.
	OutcomeSameBox WriteOutcome = "same_box"
	// OutcomeCookieBlocked — the request carried a `Cookie: faas_sid=`
	// but no bearer token, AND the receiving box is not the leader.
	// Sessions are local per ADR-039; cross-box cookie validation
	// requires shared PG (deferred per ADR-025 Tier 2). Emits 503
	// + Retry-After: 5 with an RFC 7807 body.
	OutcomeCookieBlocked WriteOutcome = "cookie_blocked"
	// OutcomeLeaderUnreachable — `LeaderResolver.Current` returned
	// `name == ""` (all boxes drained or pg outage). Emits 503 +
	// Retry-After: 60 (`pkg/api/limits.go::StandbyWriteNoLeaderRetryAfterSeconds`).
	OutcomeLeaderUnreachable WriteOutcome = "leader_unreachable"
	// OutcomeLoopPrevented — the inbound request already carries
	// LoopGuardSentinel (any value). Emits 400 with RFC 7807
	// `code=redirect_loop`. The counter is the redirect-storm DoS
	// alarm: a sustained non-zero rate means an attacker is
	// probing the cluster.
	OutcomeLoopPrevented WriteOutcome = "loop_prevented"
	// OutcomeMTLSFailure — the cross-box handshake failed (cert
	// expiry, clock skew beyond TLS tolerance). Emits 503 +
	// Retry-After: 5; the writeGate ALSO increments
	// `gateway_active_passive_failovers_total{outcome="mTLS_failure"}`
	// (the existing Tier A8 counter) so the §12 failover panel
	// surfaces this.
	OutcomeMTLSFailure WriteOutcome = "mTLS_failure"
	// OutcomeError — catch-all for unexpected writeGate state
	// (resolver returned (name="", isMe=true, err=nil) — a state
	// the algorithm does not produce by construction but the
	// counter exists for defensive programming). Never expected
	// in healthy operation.
	OutcomeError WriteOutcome = "error"
)

// AllWriteOutcomes is the closed slice of every WriteOutcome
// value. The pkg/wire pre-instantiation loops over this slice
// to seed every (outcome, auth_kind) label combination at boot
// (review finding #5 of PR #761: the label vocabulary MUST be
// imported from this package, not re-declared as raw strings
// in metrics.go — a drift here silently breaks the §12 PromQL
// `outcome=~"..."` regex matcher).
//
// Order matches the Outcome* constants above. Adding a new
// value requires:
//  1. Appending the constant to the block above.
//  2. Appending it here.
//  3. Documenting it in docs/faas_implementation_spec.md §12.
//
// The compile will fail in pkg/wire if step 1 ≠ step 2.
var AllWriteOutcomes = []WriteOutcome{
	OutcomeRelayed,
	OutcomeRedirect307,
	OutcomeSameBox,
	OutcomeCookieBlocked,
	OutcomeLeaderUnreachable,
	OutcomeLoopPrevented,
	OutcomeMTLSFailure,
	OutcomeError,
}

// AuthKind is the closed label set for the
// `<prefix>_write_redirect_total{auth_kind=...}` counter.
//
// "anonymous" covers the unauthenticatedCarveOuts paths
// (webhooks, OAuth callbacks, cli-auth) when they are reached on
// a non-leader — they're not redirected, but the metric still
// classifies them as anonymous so the dashboard can split bearer
// vs cookie vs anonymous traffic.
type AuthKind string

const (
	AuthBearer    AuthKind = "bearer"
	AuthCookie    AuthKind = "cookie"
	AuthAnonymous AuthKind = "anonymous"
)

// AllAuthKinds is the closed slice of every AuthKind value.
// See AllWriteOutcomes for the rationale on closed-slice
// pre-instantiation; the same drift-prevention rule applies.
var AllAuthKinds = []AuthKind{
	AuthBearer,
	AuthCookie,
	AuthAnonymous,
}

// unauthenticatedCarveOuts is the closed allowlist of paths that
// are mutations (POST/PUT/PATCH/DELETE) but MUST run locally even
// on a standby. The reasons are protocol-level, not policy:
//
//   - Webhook paths (/v1/webhooks/{stripe,paddle}) carry HMAC
//     headers verified against the receiving box's stored secret.
//     Cross-box forwarding would require either (a) the standby
//     replaying the HMAC verification OR (b) the standby trusting
//     the leader's HMAC verdict — both are strictly weaker than
//     the existing local verification.
//
//   - OAuth callbacks (/v1/auth/{google,github}/callback) bind
//     the OAuth `state` cookie to the box that started the flow.
//     Cross-box forwarding breaks the state binding.
//
//   - cli-auth paths (/v1/cli-auth/*, /cli-auth) are an anonymous
//     device-code flow where the customer machine polls for a
//     token. Redirecting mid-flow from standby → leader would
//     change the polling origin and break the device-code
//     semantics (the device code is per-host).
//
// Carve-out path constants. Each is referenced in the
// `unauthenticatedCarveOuts` map AND in the test suite —
// promoting them to named constants (a) gives the protocol
// invariant a stable identity in logs/metrics, (b) prevents
// drift between the allowlist and the test fixture (goconst
// detects when a string appears 3+ times, which would
// otherwise signal "this should be a constant"), and (c) lets
// the runbook link to a path by its identifier rather than
// its string form.
//
// Adding a new carve-out requires updating BOTH the constant
// block below AND the `unauthenticatedCarveOuts` map (the
// goconst lint will catch a missed map entry). The order
// matches the allowlist ordering in writegate_test.go.
const (
	CarveOutWebhookStripe   = "/v1/webhooks/stripe"
	CarveOutWebhookPaddle   = "/v1/webhooks/paddle"
	CarveOutCLIAuthCode     = "/v1/cli-auth/code"
	CarveOutCLIAuthExchange = "/v1/cli-auth/exchange"
	CarveOutCLIAuthRoot     = "/cli-auth"
	CarveOutOAuthGoogleCB   = "/v1/auth/google/callback"
	CarveOutOAuthGitHubCB   = "/v1/auth/github/callback"
)

// The allowlist is intentionally small (7 entries) and stable;
// every entry corresponds to a documented protocol invariant.
// Adding a new entry requires updating this doc + the table-driven
// tests in writegate_test.go.
var unauthenticatedCarveOuts = map[string]bool{
	CarveOutWebhookStripe:   true,
	CarveOutWebhookPaddle:   true,
	CarveOutCLIAuthCode:     true,
	CarveOutCLIAuthExchange: true,
	CarveOutCLIAuthRoot:     true,
	CarveOutOAuthGoogleCB:   true,
	CarveOutOAuthGitHubCB:   true,
}

// IsCarveOutPath reports whether the request path is an
// unauthenticated mutation that must NOT be cross-box forwarded.
// Returns false for reads (the classifier skips them entirely
// before consulting this map).
func IsCarveOutPath(path string) bool {
	return unauthenticatedCarveOuts[path]
}

// writeMethods is the closed set of HTTP methods whose
// apid-bound requests the writeGate treats as mutations. Reads
// (GET/HEAD/OPTIONS) fall through to local apidProxy untouched.
// CONNECT/TRACE are not handled by apid; the proxy's Host-based
// routing drops them before they reach this gate.
var writeMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// IsWriteMethod reports whether the request's method is one
// the writeGate treats as a mutation. Returns false for reads
// (GET/HEAD/OPTIONS) and for any method apid doesn't bind.
func IsWriteMethod(method string) bool {
	return writeMethods[strings.ToUpper(method)]
}

// AuthKindOf classifies the inbound request's auth posture.
// Pure function — derives only from header inspection.
//
// Order of precedence:
//
//  1. `Authorization: Bearer ...` → AuthBearer.
//  2. `Cookie: __Host-faas_sid=...` or the legacy `faas_sid=` (the ADR-039
//     session cookie, ADR-20261026112500)
//     → AuthCookie.
//  3. else → AuthAnonymous (the unauthenticatedCarveOuts paths).
//
// The function does NOT validate the bearer key or the session
// cookie; that work lives in apid's auth chain
// (cmd/apid/server.go::authLimited). The gate only needs to know
// which auth kind is in play so the cross-box hop carries the
// right headers (Authorization) and the cookie-only path emits
// the right error response.
//
// Scheme matching accepts both `Bearer <token>` and bare `Bearer`
// (no trailing space, no token) so a malformed `Authorization: Bearer`
// header — which some HTTP clients emit when the token is empty —
// is still classified as AuthBearer. Classifying it as anonymous
// would route an otherwise-bearer request through the
// unauthenticated cross-box path, which is a security-relevant
// regression (review finding #4 of PR #761). The bare-Bearer
// request will still 401 at apid's auth chain because the empty
// token is rejected downstream — the gate's job is only to
// classify, not authenticate.
func AuthKindOf(r *http.Request) AuthKind {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(h), "bearer") {
		return AuthBearer
	}
	if c, err := sessioncookie.Get(r); err == nil && c != nil {
		return AuthCookie
	}
	return AuthAnonymous
}

// IsLoopAttempt reports whether the inbound request already
// carries the LoopGuardSentinel header — meaning either a
// legitimate cross-box hop has completed (and a re-entry would
// loop) or an attacker is probing with a crafted header. The
// function does NOT distinguish; writeGate rejects both with 400.
//
// The sentinel value is intentionally ignored — even an empty
// value triggers the rejection. The presence of the header is
// the loop signal.
//
// Implementation note: net/http's Header.Get returns "" for
// both "header absent" AND "header present with empty value",
// so we look up the canonical key directly. This honors the
// documented invariant (any inbound sentinel → 400) at the
// cost of one canonicalization call per request.
func IsLoopAttempt(r *http.Request) bool {
	_, present := r.Header[http.CanonicalHeaderKey(LoopGuardSentinel)]
	return present
}

// IsWriteRequest is the top-level classifier the proxy calls
// before deciding whether to enter the writeGate flow. It is
// a coarse pre-filter — the actual writeGate logic (auth-kind
// branching, leader resolution, cross-box dial) lives in PR-B.
//
// `r.URL.Path` is matched against the same anchored-path set
// `isApidPath` uses in cmd/gatewayd-internal/proxy.go (the
// apid-public surface: /v1, /dashboard, /oauth/*, /login/*,
// /auth/*, /status/*, /healthz, /cli-auth). The PR-A refactor
// moves the predicate into this package but the *exact* set of
// routes is unchanged — see writegate_test.go for the regression
// table. PR-A explicitly does NOT modify the existing
// isApidPath call sites in cmd/gatewayd-internal/proxy.go; PR-B
// re-points the proxy to this helper.
//
// Returns true ONLY when both the method is a write AND the
// path is apid-bound. Reads always return false; carve-outs
// (webhooks, OAuth callbacks, cli-auth) return false here
// because they're handled by local apidProxy — the per-path
// carve-out allowlist is checked at the proxy call site, not
// in this predicate.
//
// NOTE: the path predicate was lifted from a writegate-local
// duplicate (`apidPathMatch`) to the shared `pkg/apid.IsApidPath`
// in PR-B / Tier A9 / ADR-084. The two implementations share
// the same anchored-root discipline and were verified
// equivalent via the regression table at
// `pkg/apid/router_test.go::TestIsApidPath` and the parallel
// table at `writegate_test.go::TestIsWriteRequest_Mutations`.
//
// The handler constructor (`newWriteGate` in
// cmd/gatewayd-internal/write_gate.go, PR-B sub-task B6)
// receives the path predicate as an injected `func(string)
// bool` so any future caller can route the gate through a
// different matcher without re-touching `pkg/gateway/writegate`.
func IsWriteRequest(r *http.Request) bool {
	if !IsWriteMethod(r.Method) {
		return false
	}
	return apid.IsApidPath(r.URL.Path)
}
