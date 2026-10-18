// Issue #606 / SAFE-RELEASES-E.1: per-deployment actor attribution.
// This file is the single source of truth for "given an incoming
// HTTP request that submitted a deployment, who is the actor?" —
// both the server-side column stamps (deployed_by_user_id,
// deployed_via, deployed_from_ip) and the audit-row actor string
// (s.audit.EmitAs(..., resolvedActor, ...)) route through the helpers
// here so the via-classifier never forks across handler files.
//
// MEDIUM review #1 (PR #992): the previous header-sniffing
// classifier (cookie name "faas_session", case-sensitive Bearer
// prefix check, X-API-Key branch) was structurally broken:
//   - cookie name was wrong (the real one is "faas_sid" per
//     handlers_auth_login.go:520 + auth_facade.go:225); the
//     dashboard branch was silently dead code;
//   - the X-API-Key branch was unreachable from any auth-derived
//     request — the SDK uses Authorization: Bearer
//     (pkg/api/client.go:202,308,389,…);
//   - reading raw headers after the auth chain had already
//     validated and classified the credential meant a holder of
//     a stolen bearer key could spoof the dashboard branch by
//     attaching a Cookie: faas_sid=garbage header; the row would
//     stamp deployed_via='dashboard' while authenticated via
//     the bearer key. Provenance was provably wrong.
//
// This file replaces the header-sniff classifier with one
// derived from authmw.AccountFromContext — the authoritative
// (Account, *APIKey) tuple the auth chain stashed on r.Context()
// after credential validation. The auth chain is the source of
// truth; this classifier is downstream of it.
//
// The githubd_bridge path stamps "github" at the bridge itself
// (cmd/apid/githubd_bridge.go::EnqueueBuild) — the bridge
// doesn't carry an HTTP request, so this helper never sees it.
//
// The closed-set vocabulary (api / cli / dashboard / github /
// operator) is enforced at the schema layer by
// migrations/00305_deployments_actor.sql's CHECK constraint — the
// helpers here return only values in that set. "operator" is
// reserved for the (not-yet-implemented) admin path; today the
// helper returns "api" as the fallback when the principal is
// authenticated but the via is otherwise indistinguishable (the
// SDK and the CLI emit byte-identical Authorization: Bearer
// headers — the via split is intent-only, not wire-distinguishable).

package apidserver

import (
	"net/http"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeKindForRequest returns the closed-set classifier for an
// inbound HTTP request. One of "api" / "cli" / "dashboard". The
// githubd_bridge path stamps "github" at the bridge itself
// (cmd/apid/githubd_bridge.go::EnqueueBuild) so this helper never
// has to inspect proto headers — handlers called via the bridge
// are not HTTP-routed.
//
// Classification (MEDIUM review #1, PR #992):
//
//	auth chain classified via API key (AuthKey != nil) → "api"
//	  (machine-to-machine / SDK; the CLI is rare-to-never
//	  API-key authenticated)
//	auth chain classified via session cookie (AuthKey == nil)
//	  → "dashboard"
//	auth chain did NOT classify (r.Context() lacks the tuple —
//	  a request that reached the handler without going through
//	  the auth middleware) → "api" (safe default; the closed-set
//	  CHECK on deployed_via rejects anything outside the set,
//	  so the schema is the safety net for the no-principal
//	  case)
//
// The classifier is downstream of authmw.AccountFromContext — the
// auth chain is the source of truth for "who is authenticated
// and via what credential", not the raw headers. Reading raw
// headers post-authentication is the spoofing surface the
// review flagged.
func routeKindForRequest(r *http.Request) string {
	_, key, ok := authmw.AccountFromContext(r)
	if !ok {
		// No principal stashed — the request reached this
		// handler without going through the auth middleware.
		// Default to "api" rather than failing closed: the
		// closed-set CHECK on deployed_via rejects any
		// out-of-set value, and the dashboard deployment
		// table rows that miss the principal column can be
		// re-stamped via a follow-up migration if
		// observability shows the fallback is hit on any
		// customer surface.
		return "api"
	}
	if key == nil {
		return "dashboard"
	}
	return "api"
}

// stampDeploymentActor stamps the three server-resolved actor
// columns onto a *state.Deployment before INSERT. acct.ID is the
// deploying account's UUID (always set — the handler ran
// loadAppAndPreflight which enforced auth). The IP comes from
// pkg/middleware.ClientIP so the trust contract matches the
// auth-limit bucket.
//
// PusherLogin is NOT stamped here — that path is bridge-only and
// lives in cmd/apid/githubd_bridge.go::EnqueueBuild.
//
// Empty fields are kept as "" on the struct; the pgstore INSERT
// path coalesces them to NULL/” via the migrations/00305 SQL
// shape (nullif() + coalesce() chain).
func stampDeploymentActor(d *state.Deployment, acct state.Account, r *http.Request) {
	d.DeployedByUserID = actorAccount(r, acct).ID
	d.DeployedVia = routeKindForRequest(r)
	if ip := middleware.ClientIP(r); ip != "" {
		d.DeployedFromIP = ip
	}
}

// mergeActorAudit folds the actor context into an existing audit
// payload map. Mirrors the eventual PR #984 mergeAnnotationAudit
// pattern: zero-valued fields drop (no "actor_user_id": "" keys on
// the wire). Callers always pass a non-nil data map; nil handling
// is intentionally omitted (all existing audit Emit call sites
// allocate the map inline at the Emit call site).
//
// Concatenated keys:
//   - actor_user_id (string)
//   - actor_via     (closed-set string)
//   - actor_ip      (string; empty when request had no trusted IP)
//   - actor_pusher  (string; non-empty only on githubd_bridge path)
//
// The "omit when zero" rule matches PR #984's annotation-merge
// helper so the audit row shape stays grep-friendly across PRs.
func mergeActorAudit(data map[string]any, userID, via, ip, pusher string) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	if userID != "" {
		data["actor_user_id"] = userID
	}
	if via != "" {
		data["actor_via"] = via
	}
	if ip != "" {
		data["actor_ip"] = ip
	}
	if pusher != "" {
		data["actor_pusher"] = pusher
	}
	return data
}

// resolvedActorString returns the canonical `<via>:<id>` form for
// s.audit.EmitAs. Used by commit 3 to widen the audit row's actor
// column from the constructor-baked "apid" to the per-call resolved
// actor (PR-E1.3). The shape mirrors the metric-labelling convention
// in pkg/audit (free-form, daemons-as-namespaces).
//
// userID is the deploying account's UUID (or empty for githubd
// path where the pusher isn't a local account). pusher is the raw
// GH login for the githubd path; empty otherwise.
//
// Examples:
//
//	resolvedActorString("dashboard", "8a2...", "", "") = "dashboard:8a2..."
//	resolvedActorString("github",    "",     "", "poyrazK") = "github:poyrazK"
//	resolvedActorString("cli",       "8a2...", "", "") = "cli:8a2..."
func resolvedActorString(via, userID, pusher string) string {
	switch via {
	case "github":
		if pusher != "" {
			return "github:" + pusher
		}
		// Fall through: a githubd call with no pusher (shouldn't
		// happen but the schema permits it) → attribute to the
		// bridge itself.
		return "github:unknown"
	default:
		if userID != "" {
			return via + ":" + userID
		}
		return via + ":unknown"
	}
}
