package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
	"github.com/onebox-faas/faas/pkg/geoip"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ResolveSlugFn (ADR-093) is the (slug → appID) resolver the
// control-listener /v1/internal/apps/{slug}/routes handler uses
// to translate the path segment into the appID the in-process
// route-set map keys on. Production wires a closure that consults
// the same apps table apid used to register the app — the
// control listener is loopback-only so it does NOT open its own
// Postgres connection (ADR-070 single-purpose control mux). The
// first request after a freshly-started gatewayd sees an empty
// Routes array until the in-process cache hydrates; the dashboard
// treats that as "no traffic yet" (matches the existing
// /v1/internal/quota empty-state contract).
//
// ok=false is a clean "slug not registered"; the handler renders
// an empty Routes array rather than 404 so the dashboard doesn't
// distinguish "unknown slug" from "no traffic yet" (avoiding an
// enumeration oracle on the loopback surface).
type ResolveSlugFn func(slug string) (appID string, ok bool)

// App is the routing target for a hostname.
type App struct {
	ID        string
	AccountID string // joined in pgRouter.toApp; empty only in fakeBackend unit tests (ADR-040)
	Plan      api.Plan
	// MaxConcurrency is the app instance ceiling; zero uses the plan ceiling.
	MaxConcurrency int
	// Slug is the customer-facing app slug (lowercased at apid
	// write time). Surfaced on the 503 Problem.detail for
	// apps.maintenance_mode so monitoring / curl users can
	// identify which app is in maintenance. Default-empty in
	// fakeBackend unit tests; production path always populates.
	Slug string
	// MaintenanceMode (ADR-091 amendment / §4.1.2.0) is the
	// coarse-grained per-app maintenance flag mirrored from
	// apps.maintenance_mode. When true, every inbound request is
	// short-circuited with 503 + Retry-After BEFORE auth, BEFORE
	// wake, BEFORE any kind=maintenance rule (coarse gate beats
	// fine-grained per D4 ordering). Plumbed through pgRouter.toApp
	// from the apps row so ServeHTTP can short-circuit WITHOUT
	// re-reading the database. Default-false in fakeBackend unit
	// tests; production path always populates from
	// apps.maintenance_mode.
	MaintenanceMode bool
	// StreamingEnabled is the per-app streaming flag (issue #471 /
	// ADR-047). Plumbed through pgRouter.toApp from the apps row so
	// ServeHTTP can decide between the buffered and streamed
	// response path WITHOUT re-reading the database. PR-A uses the
	// flag only for the once-per-process buffered-fallback
	// deprecation log; PR-B activates the actual Flusher path.
	// Default-false in fakeBackend unit tests (the in-memory
	// backend doesn't populate the column).
	StreamingEnabled bool
	// WebSocketEnabled is the per-app raw-bytes Upgrade bridge flag
	// (issue #676 / ADR-080). Plumbed through pgRouter.toApp from
	// apps.websocket_enabled so ServeHTTP can route inbound
	// Connection: Upgrade + Upgrade: <token> requests to the
	// rawStreamReverseProxy without re-reading the database. apid
	// applies Plan.WebSocketEnabled() at CreateApp time and gates
	// PATCH writes through Plan.WebSocketResponseAllowed() (Free
	// returns false → 403 plan_websocket_not_allowed). Default-false
	// in fakeBackend unit tests; tests that want to exercise the
	// raw path set this to true alongside an app.Plan != PlanFree.
	WebSocketEnabled bool
	// RouteMetricsEnabled (ADR-093) opts the app into the per-route
	// observability surface. When true, Handler.observe emits three
	// additional Prometheus series keyed by an enumerated `route`
	// label (method + raw path, bounded per-app at 50 distinct
	// entries with __route_other__ as the non-evicting overflow
	// bucket) and the bounded in-memory reader at
	// GET /v1/internal/apps/{slug}/routes returns the per-route
	// detail. apid applies Plan.RouteMetricsEnabled() at CreateApp
	// time and gates PATCH writes through Plan.RouteMetricsResponseAllowed().
	// Default-false in fakeBackend unit tests; tests that want to
	// exercise the per-route path set this to true alongside an
	// app.Plan != PlanFree.
	RouteMetricsEnabled bool
	// AppProtocol (ADR-124) is the per-app wire-protocol selector
	// that the customer picks at the public edge. Closed-set
	// {api.AppProtocolHTTP1 ("http1"), api.AppProtocolHTTP2 ("http2"),
	// api.AppProtocolGRPC ("grpc")}. The default ("") is treated as
	// "http1" by decideProtocol (preserves pre-ADR-124 behaviour for
	// every fakeBackend unit test that doesn't populate the column).
	// `grpc` is plan-gated at write time by apid (Plan.AppProtocolAllowed);
	// the gateway enforces no plan gate here, so the read-side
	// hydration must populate this from apps.app_protocol verbatim.
	// Plumbed through pgRouter.toApp / the AppResolver closure from
	// the apps row so ServeHTTP can stamp x-faas-protocol on the
	// request at the site x-faas-stream is stamped today without
	// re-reading the database. Empty on legacy unit-test fixtures.
	AppProtocol string
	// NodeID is the durable shard key the owning schedd
	// resolves at startup (Phase 2 / Gate A). Populated by
	// pgRouter.toApp / the AppResolver closure from apps.node_id;
	// empty on the pre-migration single-box install where
	// apps.node_id was added by migration 00090. Tests that
	// don't exercise the per-schedd routing path leave it zero.
	NodeID string
	// Sidecars (issue #463 / ADR-069 / ADR-071 / PR-C §5) is
	// the per-deployment sidecar roster mirrored on the app
	// for the public listener's routing-key split
	// (<host>--<sidecar>.<suffix>). Empty for a deployment
	// with no sidecars (the typical pre-PR-B install).
	// Sourced from the apps row's deployments join at
	// ResolveHost time; stale at most for the route-cache
	// TTL (60s, downstream). The per-port forwarder
	// (Target.Port) uses these to resolve the sidecar port
	// that the public listener forwards to. Mirroring on
	// the App struct keeps the hot path allocation-free
	// after first sight.
	Sidecars []AppSidecar
	// RequireAuthn (issue #560) is the per-deployment
	// token-gate opt-in. When true, ServeHTTP demands a
	// valid `Authorization: Bearer <token>` header on every
	// request to this app; cross-account tokens (the bearer
	// resolves to a different account than App.AccountID)
	// receive 403 insufficient_scope. Plumbed through
	// pgRouter.toApp from the apps row, mirroring
	// StreamingEnabled above, so ServeHTTP can make the
	// decision without re-reading the database. Default
	// false in fakeBackend unit tests (the in-memory
	// backend doesn't populate the column).
	RequireAuthn bool
	// PublicAuth (issue #477 / ADR-079) is the per-app
	// public-URL auth mode (open|bearer|basic). When
	// mode='open' (the pre-#477 default), ServeHTTP
	// pass-throughs anonymous traffic. When mode='bearer',
	// ServeHTTP demands an Authorization: Bearer header
	// (re-using the require_authn key chain, same
	// apps:read scope on the app's owning account).
	// When mode='basic', ServeHTTP demands an
	// Authorization: Basic header and constant-time-
	// compares the unsealed creds (BasicSealed from the
	// apps row, sealed under the APP_BASIC_AUTH
	// secretbox namespace). Plumbed through pgRouter.toApp
	// from the apps row; default zero value (Mode="" →
	// treated as "open" by enforcePublicAuth) preserves
	// the pre-#477 customer behaviour in fakeBackend unit
	// tests.
	PublicAuth PublicAuthConfig
	// Scope (issue #272 / ADR-095 PR-B) is the per-lookup scope
	// label that the gateway forwards to schedd on the wake /
	// admit path. Empty = production (the legacy single-deployment
	// behaviour every pre-PR-B caller exercises). Non-empty =
	// preview scope, e.g. "pr-42" — the preview app row already
	// carries its own apps.id, so the scheduler resolves the
	// wake against the scope's live deployment (ADR-091 surfaced
	// LiveDeploymentForScope for this).
	//
	// The field is populated by pgRouter.toApp from the resolved
	// apps row's preview_pr_state + preview_pr_number
	// (preview apps row) or stays empty (prod row). It is NOT
	// plumbed via the apps table — it's a per-lookup token, not a
	// row property — so cache invalidation is identical to the
	// existing route cache (apps_update / domain_changed wipes
	// the route cache, and the next Lookup re-derives).
	Scope string
	// CORS improvements D1: per-app default CORS
	// opt-in. Plumbed from apps.cors_default_enabled
	// through pgRouter.toApp so applyEdgeRuleCORS
	// can stamp a soft CORS header set on the
	// response when no kind=cors rule matches the
	// Pointer (not plain bool) for the same
	// reason state.App uses a pointer: legacy
	// rows (never PATCHed) hydrate as *false;
	// an explicit opt-out hydrates as *false;
	// an opt-in hydrates as *true. The
	// nil-vs-*false distinction only matters on
	// the WRITE path; the hot path collapses
	// both to false by checking for non-nil
	// before deref (see applyEdgeRuleCORS).
	// fakeBackend unit tests use *bool(true)
	// to exercise the default-fallback stamp.
	// See spec §4.1.2.6a CORS defaults.
	CORSDefaultEnabled *bool
	// CORS improvements D1: per-app default CORS
	// allowlist. Plumbed from apps.cors_default_origins
	// (text[]) through pgRouter.toApp so the gateway
	// reuses the same matchOrigin matcher against
	// this list as against edge_rules_cors.allow_origins.
	// nil and len==0 are both treated as "deny all"
	// by the gateway; the apid handler validates that
	// CORSDefaultEnabled=true ⇒ a non-nil value is
	// provided.
	CORSDefaultOrigins []string
}

// PublicAuthConfig (issue #477 / ADR-079 + ADR-118) is the
// per-app public-URL auth mode bundle plumbed onto App. Mode is
// the canonical text from apps.public_auth_mode CHECK enum
// ('open'|'bearer'|'basic'|'ip_allowlist'); empty Mode is
// treated as 'open' by enforcePublicAuth so a fakeBackend unit
// test that doesn't populate the column keeps working.
// BasicSealed is the secretbox-sealed bytea from
// apps.public_auth_basic, only set when Mode='basic' (nil for
// open/bearer/ip_allowlist). The unsealed shape is
// {username_env, password_env} env-var reference names; the
// plaintext credentials live in app_secrets (ADR-045) and are
// loopback-mounted at boot.
//
// IPAllowlist (ADR-118) is the per-app ingress CIDR allowlist
// hydrated from apps.public_auth_ip_allowlist cidr[]. Only
// consulted when Mode=publicAuthModeIPAllowlist; for other
// modes the slice is left nil and ignored. The handler caches
// this slice once per app per process — the NotifyAppChanged
// arm in cmd/gatewayd-internal/backend.go already invalidates
// the per-app cache on every PATCH, so live-drift fan-out is
// handled without an extra RPC.
//
// The mode check is done with a direct string compare on the
// constants below — no separate enum type, mirroring the
// eviction_priority / streaming / require_authn column
// shape.
type PublicAuthConfig struct {
	Mode        string
	BasicSealed []byte
	IPAllowlist []netip.Prefix
}

// Canonical public-auth mode strings (issue #477). Values
// must stay in sync with the apps_public_auth_mode_chk
// CHECK constraint in migrations/00153_apps_public_auth.sql
// (widened in 00326 for ip_allowlist + 00333 for
// internal_only). Companion drift-guard tests pin the three
// surfaces equal: pkg/api/public_auth_constants_test.go (api
// vs state) and pkg/gateway/handler_public_auth_constants_test.go
// (handler package-local lowercase).
const (
	publicAuthModeOpen         = "open"
	publicAuthModeBearer       = "bearer"
	publicAuthModeBasic        = "basic"
	publicAuthModeIPAllowlist  = "ip_allowlist"
	publicAuthModeInternalOnly = "internal_only"
)

// docsTypeBase is the canonical docs path prefix for problem
// `type:` URLs emitted from the gateway (RFC 7807 §3.1). Sourced
// from wire.DocsHost so a rotation only edits pkg/wire/docs.go,
// not this file. Distinct from pkg/api's `docsBase` because the
// gateway emits problem types (full URN-shaped slugs) rather
// than topic-path docs URLs.
var docsTypeBase = "https://" + wire.DocsHost + "/errors"

// Authn-failure reason taxonomy (issue #560 + issue #477).
// The strings land on the `reason` field of the audit row
// emitted by enforceRequireAuthn and enforcePublicAuth,
// and feed the dashboard handler-edge severity cap. Two
// callers (the require_authn gate and the public_auth gate)
// share the same set so a pivot by reason in the audit
// log works across both gates. Extracted as constants so
// goconst's "3+ occurrences" lint rule is satisfied AND
// so a drift between the two call sites surfaces at
// compile time.
const (
	authnReasonInvalidBearer = "invalid_bearer"
	authnReasonExpired       = "expired"
	authnReasonRevoked       = "revoked"
)

// Rate-limit closed vocabulary (goconst: literal duplicates that
// crossed the package-wide goconst threshold once Phase 4 added
// the per-rule consult + the X-RouteRateLimit-Policy header).
// These mirror the constants in pkg/gateway/ratelimit.go
// (centralKey triple) and the KindEdgeRule constants in
// pkg/api/dto.go — keep them in sync when adding new values.
const (
	rateLimitScopeApp     = "app"
	rateLimitScopeAccount = "account"
	rateLimitScopeRule    = "rule"
	rateLimitScopeRoute   = "route"
)

// AppSidecar (issue #463 / ADR-069 / ADR-071 / PR-C §5) is
// the narrow subset of pkg/api.SidecarSpec the public
// listener's routing-key split uses. The full spec lives
// in pkg/api (the wire-shape that imaged writes at build
// time); the gateway only needs Name + Port to drive the
// `<host>--<name>` → `port` resolution. Keeping the type
// gateway-local avoids a pkg/api ↔ pkg/gateway cycle.
type AppSidecar struct {
	Name string
	Port int
}

// RequireAuthnAuthenticator (issue #560) is the narrow slice of
// pkg/auth.Middleware.Authenticator the per-deployment authz
// branch consumes — AuthenticateKey alone, returning
// (account, key, error). Declaring it locally keeps pkg/gateway
// free of any import dependency on pkg/auth or pkg/state
// (cmd/gatewayd-internal/wires the *authmw.Middleware, which satisfies
// this interface through its exported Authn field; the
// compile-time assertion at the call site pins the contract).
type RequireAuthnAuthenticator interface {
	AuthenticateKey(ctx context.Context, hash []byte) (RequireAuthnAccount, RequireAuthnKey, error)
}

// RequireAuthnAccount is the read-only slice of state.Account the
// authz branch needs — just the ID, so it can compare against
// App.AccountID and emit 403 on mismatch. Field name mirrors
// state.Account.ID so a future drift surfaces as a compile error
// at the wiring site.
type RequireAuthnAccount struct {
	ID string
}

// RequireAuthnKey is the read-only slice of state.APIKey the
// authz branch carries for forensic visibility on denied
// requests (the audit payload's `key_id` field). Field name
// mirrors state.APIKey.ID so the wiring site catches drift.
type RequireAuthnKey struct {
	ID string
}

// RequireAuthnAuditor is the narrow slice of cmd/gatewayd-internal/audit.go's
// gatewaydAuditor the per-deployment authz branch uses to emit
// instances.authn_missing / instances.authn_invalid /
// instances.authn_scope. Declared locally so pkg/gateway doesn't
// import cmd/* (avoid a reverse dep) and so tests can inject a
// counting fake. Best-effort semantics — the authz branch never
// blocks a deny response on a failed emit (matches the apid
// auditor's contract at cmd/apid/audit.go:79).
type RequireAuthnAuditor interface {
	Emit(ctx context.Context, kind string, subject *string, data map[string]any)
}

// PublicAuthUnsealer (issue #477 / ADR-079) turns a
// secretbox-sealed APP_BASIC_AUTH blob into the {username,
// password} pair the request Authorization header carries.
// Declared locally (mirroring RequireAuthnAuthenticator's
// shape) so pkg/gateway stays free of any import dependency on
// pkg/secretbox or pkg/auth; the production wiring in
// cmd/gatewayd-internal closes over secretbox.OpenMulti on the
// loaded host identities. Returns an error when the blob is
// tampered or the namespace tag doesn't match — the caller
// treats both as a credential mismatch (401) so a brute-forcer
// can't tell the difference.
//
// **ctx contract (load-bearing):** the ctx parameter is the
// per-request inbound ctx (the basic-auth branch passes
// r.Context()). The implementation MUST be ctx-clean:
// no I/O, no blocking syscalls, no host.age reload on
// disk. The hot path is a single in-memory secretbox
// OpenMulti call on the loaded identities slice (the
// identities are loaded once at boot and held via the
// closure; the rotation-overlap window is bounded by the
// 30-day rotated-pair eviction). A future contributor
// adding an I/O step (e.g. fetching host.age from disk
// on a slow drive) MUST NOT block on a client-cancelled
// ctx — they should use a fresh, long-lived ctx for any
// internal I/O. The ctx parameter is reserved for
// future-proofing the contract shape, not for
// transactionalism.
type PublicAuthUnsealer interface {
	UnsealBasicAuth(ctx context.Context, sealed []byte) (username, password string, err error)
}

// Target is one routable instance in the gateway's per-app cache (issue
// #168). Multiple Targets per app = real fan-out across max_concurrency.
// The NodeID is the compute_node.id the instance lives on (ADR-028); the
// forwarder dereferences it via the per-node vmmd client cache. The
// InstanceID is the instances.id row schedd owns — used to attribute
// last_request_at touches (spec §4.1) and to stamp x-faas-instance on
// the request before proxying.
type Target struct {
	NodeID     string
	InstanceID string
	WakeID     string
	AddedAt    time.Time
	// Port (issue #460 / ADR-053, PR-C) is the per-deployment
	// override port copied from AdmitInstanceResponse.port. 0 =
	// legacy 8080 (vmmd wire-boundary default). The forwarder
	// reads this to populate ForwardHTTPRequestInit.Port; vmmd's
	// buildStreamingBridgeScript resolves 0 to netns.AppPort (8080)
	// for legacy cached targets that pre-date the field.
	Port int
	// DeploymentID (issue #556 / PR-B) is the live deployment id the
	// target was admitted for. The per-deployment weighted picker
	// (PGBackend.Pick) routes subsequent requests to the right
	// deployment bucket based on each bucket's traffic_percent.
	// Empty on legacy / pre-PR-B targets — the picker collapses to
	// today's single-targetSet behaviour when DeploymentID is empty
	// (one app, one targetSet, round-robin within it).
	DeploymentID string
}

// Backend is the seam between the edge and the rest of the platform (in
// production: the routing cache over Postgres, and schedd over gRPC). Splitting
// it out keeps the hot request path testable end-to-end without a real cluster.
//
// Issue #168 widened this interface to support per-app fan-out:
//   - Pick returns one routable instance for the app (round-robin across
//     max_concurrency), used on every request (cold or warm).
//   - HealthyCount returns the number of routable instances currently cached
//     for the app. Drives the WakeGate's shouldWake predicate: stop admitting
//     once we're at the plan's effective max_concurrency.
//   - Admit asks schedd to admit ONE additional instance for the app, gated
//     by maxConcurrency so concurrent callers cannot collectively over-admit
//     past the cap (issue #168 trust model). Returns the new Target's
//     WakeID on the admitted path, atCapacity=true when the cache is
//     already at maxConcurrency (the gateway treats this as a benign
//     no-op when it has ≥1 cached target), or an *api.Problem on real
//     failure (RAM headroom, chooser, store).
type Backend interface {
	// Lookup resolves a hostname to its app (cache-first, spec §4.1).
	// The scope is plumbed on the returned App (issue #272 /
	// ADR-095 PR-B) — empty for prod, non-empty for preview
	// hosts (e.g. "pr-42"). This avoids widening the Lookup
	// signature itself; the App handle is the only thing the
	// handler caches, so the scope must ride on it.
	Lookup(ctx context.Context, host string) (App, bool)
	// Pick returns a PickResult (issue #556 / PR-C): the routable
	// Target plus the signals the handler needs to drive
	// wake-fan-out (Picked deploymentID, ColdBucket signal).
	// OK=false when the cache is empty (caller should ensure
	// capacity first).
	Pick(appID string) PickResult
	// HealthyCount returns the number of routable Targets currently cached
	// for appID. Drives the WakeGate's shouldWake predicate.
	HealthyCount(appID string) int
	// Admit asks schedd to admit ONE additional instance for appID, only
	// when HealthyCount(appID) < maxConcurrency at the moment the call
	// commits. Implementations MUST serialize the HealthyCount check and
	// the cache update so a burst of concurrent Admit calls can never
	// collectively exceed maxConcurrency (issue #168 fan-out invariant).
	// On the admitted path wakeID is non-empty, the new Target is cached,
	// and method reflects what schedd actually did (restore or cold boot).
	// On the at-capacity path wakeID is empty, method is
	// WakeMethodUnspecified, and err is nil. On real failure err is a
	// non-nil *api.Problem and method is WakeMethodUnspecified.
	// Admit asks schedd to admit ONE additional instance for
	// appID, only when HealthyCount(appID) < maxConcurrency at
	// the moment the call commits. Implementations MUST
	// serialize the HealthyCount check and the cache update so
	// a burst of concurrent Admit calls can never collectively
	// exceed maxConcurrency (issue #168 fan-out invariant).
	//
	// deploymentID (issue #556 / PR-C): the live deployment
	// the new instance should be admitted for. Empty falls
	// through to schedd's default (newest live deployment) —
	// the legacy behaviour every pre-PR-B caller exercises.
	// Non-empty is the wake-fan-out path: the picker landed
	// on a cold bucket and the handler asks schedd to wake
	// that specific deployment so the retry Pick has a
	// routable Target.
	//
	// scope (issue #272 / ADR-095 PR-B): the per-lookup scope
	// label forwarding the preview-vs-prod routing decision
	// to schedd. Empty = prod (legacy). Non-empty = preview
	// (e.g. "pr-42"). schedd threads scope through Engine.Wake
	// / Engine.AdmitInstance / LiveDeploymentForScope so the
	// preview app's deployment row is the one resolved, not
	// the parent prod app's. The picker is keyed by appID
	// (the preview app already has its own apps.id), so
	// scope is orthogonal to the per-app picker buckets.
	//
	// On the admitted path wakeID is non-empty, the new
	// Target is cached, and method reflects what schedd
	// actually did (restore or cold boot). On the at-capacity
	// path wakeID is empty, method is WakeMethodUnspecified,
	// and err is nil. On real failure err is a non-nil
	// *api.Problem and method is WakeMethodUnspecified.
	// trigger (ADR-127): wake-boot trigger enum value forwarded to
	// schedd so the emitted wake.boot_started / wake.boot_completed
	// events stamp "gateway". Other triggers (cron, floor, etc.)
	// have their own Backend wrappers or wire RPCs; this surface
	// is the gateway-driven admit path so the trigger is always
	// "gateway". Kept as a parameter (rather than hard-coded) so
	// future caller surfaces (synth handler, replay worker) can
	// pass a distinct closed-enum value without breaking the wire.
	Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (wakeID string, method WakeMethod, atCapacity bool, err error)
	// LookupMirrorRules (issue #72 / ADR-125 PR-A3) returns the
	// enabled mirror rules cached for appID, or (nil, false) on a
	// cache miss. The handler treats a miss as "no mirror" — the
	// mirror never blocks the customer response; the next
	// pg_notify for kind="mirror" will populate the cache. Returns
	// the gateway-local projection (MirrorRuleRow), not the state
	// type, so the gateway package keeps its zero-pkg/state import
	// discipline (mirrors deploymentWeightsStore's posture).
	LookupMirrorRules(ctx context.Context, appID string) ([]MirrorRuleRow, bool)
	// ScheduleMirror (issue #72 / ADR-124 / ADR-125 PR-A3) is the
	// mirror-VM admission sibling to Admit. Stamps mode='mirror'
	// on the new instances row (PR-A1's 00385) and is gated by
	// the per-rule concurrent-mirror-VM cap (default 5,
	// sched.MirrorMaxConcurrentPerRule). Returns the mirror
	// wakeID on success, or an error wrapping
	// sched.ErrMirrorSlotAtCapacity on cap-at-max (the dispatch
	// goroutine maps that to ledger result=cap_at_max +
	// gateway_mirror_dispatched_total{result="cap_at_max"}).
	// Real failures (RAM headroom, chooser, store) come back as
	// *api.Problem-shaped errors — the dispatch goroutine logs +
	// drops those without writing a misleading ledger row.
	ScheduleMirror(ctx context.Context, appID, mirrorDeploymentID, mirrorRuleID string) (instanceID, wakeID string, err error)
}

// liveTargetReconciler is an optional capability implemented by the
// PostgreSQL-backed production backend. It lets the handler repair an empty
// process-local target cache before deciding that an app is cold. Keeping it
// optional preserves the small Backend test seam and legacy backends.
type liveTargetReconciler interface {
	ReconcileLiveTargets(ctx context.Context, appID string) error
}

// warmPathPicker is implemented by the production PGBackend. It lets the
// handler probe the already-hydrated picker before entering ensureCapacity,
// while legacy/test backends retain the original ensure-then-pick ordering.
// Keeping this additive avoids widening Backend and preserves custom adapters
// that intentionally model a pick failure after admission.
type warmPathPicker interface {
	PickWarm(appID string) PickResult
}

// staleTargetRecovery is an optional production capability. When the
// forwarder proves that the selected target is stale, the backend removes it
// from the picker and asynchronously admits a replacement if that eviction
// left the app with no routable capacity. Keeping this optional preserves the
// small Backend test seam and avoids widening the hot-path interface.
type staleTargetRecovery interface {
	RecoverStaleTarget(ctx context.Context, appID, scope string, maxConcurrency int)
}

// warmEnsurer is the optional production cold-start capability. Its scheduler
// implementation is cross-producer single-flight; preview/deployment-scoped
// paths fall back to Backend.Admit because the legacy EnsureWake RPC has no
// scope selector yet.
type warmEnsurer interface {
	EnsureWarm(ctx context.Context, appID, scope, trigger string) (wakeID string, method WakeMethod, atCapacity bool, err error)
}

// Handler is gatewayd-internal's HTTP entrypoint: route → rate-limit → (wake-block if
// parked) → proxy (spec §4.1, §2). It is the only public listener on the box.
type Handler struct {
	backend Backend
	limiter *Limiter
	// routeLimiter is the per-rule token-bucket throttle (ADR-091
	// D20.5 amendment, issue #881). Same underlying *Limiter type as
	// limiter + accountLimiter but constructed with NewLimiterWithLRU
	// (PR #887) so the per-rule bucket map — keyed by
	// `appID + "\x00" + ruleID` — cannot grow unboundedly. Distinct
	// from limiter (per-app) and accountLimiter (per-account) so the
	// customer-configured rule scope shares no bucket slots with
	// platform-shared throttles.
	routeLimiter *Limiter
	// routeConsumerLimiter (ADR-104, issue #881 Phase 3) is the
	// per-rule per-consumer bucket map, separate from routeLimiter
	// so the per-rule scope (`appID+"\x00"+ruleID`, 10k entries) and
	// the per-consumer scope (one bucket per authenticated identity
	// plus a pinned __other__ collapse bucket per rule) do not
	// fight for slots. Constructed with NewLimiterWithLRU so
	// the per-rule consumer map is bounded by
	// EdgeRuleConsumerCacheCap. nil when WithRouteConsumerLimiter
	// wasn't wired — applyEdgeRuleThrottle treats nil as
	// "per-consumer throttling disabled", matching the back-compat
	// posture for tests that don't exercise Phase 3.
	routeConsumerLimiter *Limiter
	// accountLimiter is the per-account token-bucket throttle (ADR-040 /
	// issue #292). Runs BEFORE limiter (per-app) in ServeHTTP so a botnet
	// rotating across many apps is rejected before any per-app bucket
	// drains and before the wake gate (a schedd gRPC RPC) is touched.
	accountLimiter *Limiter
	gate           *WakeGate
	// admissionQueue protects the control plane from a simultaneous cold
	// burst across many apps. It is intentionally separate from gate:
	// gate coalesces waiters for one app, while admissionQueue orders the
	// resulting app leaders across plan priorities.
	admissionQueue *wakeAdmissionQueue
	// burstPressure is the immediate request-pressure signal used to
	// trigger bounded scale-out during a public burst. It is local to
	// this gateway process and deliberately separate from scraped
	// metrics, which arrive too late to protect a cold burst.
	burstPressure *burstPressure
	// metrics may be nil; nil-guarded everywhere it is read.
	metrics *Metrics
	// mirrorRoundTripper (issue #72 / ADR-124 PR-A3) is the
	// per-request HTTP forwarder the dispatch goroutine uses
	// to reach the mirror VM. Defaults to
	// NewDefaultMirrorRoundTripper(nil) inside dispatchMirror so
	// a nil field is safe; tests inject a stub via
	// WithMirrorRoundTripper.
	mirrorRoundTripper MirrorRoundTripper
	// mirrorSlots (issue #72 / ADR-133 / ADR-125 PR-A3
	// code-review fix #3) is the per-rule concurrent mirror-VM
	// cost circuit. Keyed on the mirror-rule UUID (NOT the
	// deployment — multiple rules can target the same mirror
	// deployment). Each value is an *atomic.Int64 the dispatch
	// goroutine increments via tryAcquireMirrorSlot and
	// decrements via releaseMirrorSlot when the goroutine
	// completes (the slot reflects "VMs in flight" through
	// round-trip complete, NOT "admit attempts"). sync.Map's
	// LoadOrStore handles the first-write-under-contention race —
	// whichever goroutine lands first allocates the *atomic.Int64;
	// concurrent callers reuse the winner's pointer. The slot
	// lives on the gateway (not schedd) so the cap covers the
	// full lifecycle from admit to round-trip complete; the
	// schedd's AdmitMirrorInstance just stamps mode='mirror'
	// on the new row.
	mirrorSlots sync.Map
	// MirrorMaxConcurrentPerRule (issue #72 / ADR-133 / ADR-125
	// PR-A3) is the per-rule concurrent-mirror-VM cap. Loaded
	// from api.MirrorMaxConcurrentPerRule in NewHandlerWith so
	// the gate defaults match the schedd-engine removal of
	// mirrorSlots. Operators can lift the cap via a constant
	// edit + redeploy; ADR-127-style alert covers anomalous
	// sustained cap-at-max saturation.
	MirrorMaxConcurrentPerRule int64
	// log may be nil (defaults to slog.Default()).
	log *slog.Logger
	// appsSuffix is the configured public suffix (e.g. ".gregale.dev").
	// Non-empty enables a pre-Lookup host suffix check that 404s anything
	// outside it (spec §4.1 noise filter). Custom domains (Pro+) bypass this
	// constraint implicitly by being keys in the routing cache — see
	// WithAppsSuffix docs.
	appsSuffix string
	// egressSink records per-instance HTTP response body bytes for
	// ADR-046 (per-instance egress metering, telemetry only).
	// Set via WithEgressSink from cmd/gatewayd-internal/main.go; nil in
	// unit tests that don't exercise the egress counter.
	// Recording happens in observe() after the proxy returns —
	// see the proxyByNode call site in ServeHTTP.
	egressSink *egresssink.EgressSink
	// proxyFor builds the reverse proxy for an upstream address; overridable in
	// tests. The cap parameter is the plan-derived response body cap
	// (Plan.MaxResponseBodyBytes()); the factory installs an
	// io.LimitReader(resp.Body, cap+1) ModifyResponse hook so the
	// guest EOFs at cap+1 instead of streaming past the cap.
	// Issue #995 Phase 2 / ADR-121.
	proxyFor func(addr string, cap int64) http.Handler
	// proxyByNode builds the reverse proxy for a compute_node.id (issue
	// #98 / ADR-028). When non-nil, the handler dispatches every
	// request through it instead of proxyFor — the string returned by
	// Backend.Pick is interpreted as a node id and dereferenced via
	// the per-node vmmd client cache. nil = legacy addr-based path
	// (default for tests and the e2e harness; production wires
	// ForwardingReverseProxy in cmd/gatewayd-internal/main.go).
	//
	// PR-C (issue #460 / ADR-053): the callback receives the full
	// Target (not just the node id) so the forwarder can stamp
	// ForwardHTTPRequestInit.port with the per-deployment override
	// port the gateway cached at admit time. Legacy callers that
	// still expect the node-id-only shape must update to
	// func(Target) http.Handler — the wire defaulting at vmmd
	// keeps pre-PR-C targets (Port=0) reaching 8080.
	proxyByNode func(t Target) http.Handler
	// rawByNode (issue #676 / ADR-080) is the Upgrade-traffic
	// counterpart of proxyByNode. When non-nil, the handler
	// dispatches inbound Connection: Upgrade + Upgrade: <token>
	// requests through it instead of proxyByNode — the raw
	// forwarder opens ForwardRawStream and pumps bytes verbatim
	// into the guest's netns TCP socket. nil = the raw path is
	// disabled (default for tests and the e2e harness without a
	// vmmd overlay; production wires ForwardingRawReverseProxy in
	// cmd/gatewayd-internal/main.go alongside proxyByNode).
	//
	// Detection happens BEFORE proxyByNode is invoked: see the
	// isUpgradeRequest branch at the proxyByNode call site in
	// ServeHTTP. The App.WebSocketEnabled flag is the per-app
	// gate (Free defaults off; Hobby/Pro/Scale default on via
	// Plan.WebSocketEnabled / WebSocketResponseAllowed).
	rawByNode func(t Target) http.Handler
	// topNSample is the per-request bump for the gateway-side
	// top-N sampler (cmd/gatewayd-internal/topn.go, issue #300). Set via
	// SetTopNSample from cmd/gatewayd-internal/main.go. nil in unit
	// tests; observe() nil-checks before invoking. The function
	// takes the resolved app_id and increments the sampler's
	// rolling-window count — the gauge write itself happens in
	// the sampler's once-per-5s tick.
	topNSample func(appID string)
	// lastSeen records per-instance last_request_at (spec §4.1). nil-safe.
	lastSeen LastSeenSink
	// requestTelemetry (ADR-127) is the in-process recorder that
	// captures one row per gateway-served request at Handler.observe
	// (line 5456). Set via WithRequestTelemetryRecorder from
	// cmd/gatewayd-internal/main.go; nil in unit tests + older
	// call paths. observe nil-checks before enqueueing. The recorder
	// never opens a Postgres connection itself — the publisher
	// (request_telemetry_publisher.go) ships drained rows to apid.
	requestTelemetry *requestTelemetryRecorder

	// streamingEnabled gates the per-app streaming response path
	// (issue #471 / ADR-047). When false (the default), every app is
	// on the legacy buffered path regardless of the per-app
	// apps.streaming_enabled column; an app that emits
	// text/event-stream is buffered end-to-end with a once-per-process
	// deprecation log so a noisy Free-tier app doesn't spam logs.
	// PR-B activates the Flusher path when this is true; PR-A only
	// tests the buffered-fallback AC (#streaming_not_available on
	// Free + a logged deprecation). Set via WithStreamingEnabled from
	// cmd/gatewayd-internal/main.go so production defaults to off and operators
	// opt in per-cluster after PR-B ships.
	streamingEnabled bool
	// streamingWarned is the once-per-process log dedup for the
	// buffered-fallback deprecation. Keyed on (appID, content-type) so
	// the first instance of an SSE-emitting app under the flag-off
	// path emits one warn line; subsequent requests are silent.
	streamingWarned sync.Map // map[string]struct{} (key = appID)
	// bodyCapWarned is the once-per-process log dedup for the
	// envelope-cap warn-on-approach signal (issue #995 Phase 4 /
	// ADR-121). Keyed on (appID, bucket) so the first instance of
	// an app hitting the 80% / 95% / 100% threshold for the body
	// cap emits one slog.Warn; subsequent requests only bump the
	// Prometheus counter. The dedup is in addition to the
	// per-request atomic.Bool guards in capWriter (which prevent
	// double-increment per request); this sync.Map prevents log
	// floods across many requests.
	bodyCapWarned sync.Map // map[string]struct{} (key = appID + "\x00" + bucket)
	// piApps deduplicates Metrics.PreInstantiateApp calls per appID
	// (issue #273 / ADR-042). A value-typed sync.Map wrapper; the
	// zero value is valid so NewHandlerWith doesn't have to
	// initialise it explicitly. Value semantics avoid the
	// data-race that lazy init would create under -race.
	piApps preInstantiateApps
	// routeSets (ADR-093) is the per-app routeLabelSet map keyed
	// by appID. Lazily created on the first request for an opt-in
	// app (App.RouteMetricsEnabled=true AND the operator kill-
	// switch is enabled) so the cold path is allocation-free for
	// apps that don't opt in. The map is never deleted — the
	// underlying routeLabelSet is non-evicting (the daemon
	// restart is the only path that resets it, same contract as
	// accountLabelSet / hostnameLabelSet). sync.Map because the
	// hot path is the "already created" lookup. nil until
	// SetRouteMetricsEnabled is called from the App→routeSet
	// resolution path.
	routeSets sync.Map // appID(string) → *routeLabelSet
	// routeSetsPi (ADR-093) deduplicates Metrics.PreInstantiateAppRoute
	// calls keyed by (appID, routeLabel). The closed `class` set is
	// written once per app per route; the dedupe map is never
	// deleted (the underlying routeLabelSet is non-evicting). Same
	// shape as preInstantiateApps, separate sync.Map so the per-app
	// and per-route keys don't collide.
	routeSetsPi sync.Map // appID+"\x00"+routeLabel(string) → struct{}
	// routeMetricsEnabled (ADR-093) is the operator kill-switch
	// mirroring the per-app opt-in. When false, every per-app
	// routeSetFor lookup returns nil regardless of
	// app.RouteMetricsEnabled — the customer's flag is inert.
	// Wired from cmd/gatewayd-internal/config.go's `[route_metrics]
	// enabled` field via WithRouteMetricsEnabled. Default false
	// so existing deployments are unaffected.
	routeMetricsEnabled bool
	// requireAuthnAuthn is the bearer-key verifier the
	// per-deployment authz branch uses (issue #560). nil =
	// authz branch disabled (default; matches the pre-issue
	// behaviour where every app is public-by-default). Production
	// wires it from pkg/auth.Middleware.Authn so the
	// authenticate-key path is shared with cmd/apid + the
	// AppLogsHandler (ADR-046). nil-safe at the call site —
	// ServeHTTP skips the authz branch when nil, so unit tests
	// that don't exercise require_authn don't have to wire it.
	requireAuthnAuthn RequireAuthnAuthenticator
	// requireAuthnAudit emits the instances.authn_missing /
	// instances.authn_invalid / instances.authn_scope audit
	// rows when a gated-app request is denied. nil =
	// audit-disabled (tests). Production wires thegatewayd-internal
	// audit emitter so the rows land in the same events table
	// every other gatewayd-scope row uses (cmd/gatewayd-internal/audit.go).
	requireAuthnAudit RequireAuthnAuditor
	// publicAuthCache is the unsealed basic-auth credential
	// cache (issue #477 / ADR-079). Nil = no caching; the
	// basic-auth path falls back to per-request unsealing
	// (slower but safe; used by unit tests that don't want to
	// thread a cache through the constructor). Production
	// wires it from cmd/gatewayd-internal/main.go so the
	// 60s TTL + per-key invalidation through
	// db.NotifyKeyChanged both apply. The cache itself lives
	// in pkg/gateway/public_auth_cache.go.
	publicAuthCache *PublicAuthCache
	// responseCache (ADR-122 §Decision) is the in-process cache
	// the kind=cache applier consults. nil = cache disabled
	// (every request is a cache miss; the runtime never serves
	// from store). Production wires this from
	// cmd/gatewayd-internal/run.go via WithResponseCache; unit
	// tests omit it. The cache's invalidation surface lives on
	// the cache itself (InvalidateByApp
	// / InvalidateAll) and is wired by the db.Notify handler in
	// commit 14.
	responseCache *ResponseCache
	// publicAuthUnsealer turns an APP_BASIC_AUTH secretbox
	// sealed blob into the username/password pair the
	// request header carries. nil = unseal disabled (unit
	// tests + dev boxes that don't have the host.age loaded);
	// mode='basic' returns 500 if hit. Production wires the
	// same secretbox.MultiOpen path the apid uses
	// (cmd/gatewayd-internal/main.go → secretbox.MultiOpen
	// → publicAuthUnsealer closure).
	publicAuthUnsealer PublicAuthUnsealer

	// edgeRules (ADR-089 / issue #561 PR 3) is the
	// per-host LRU-backed matcher consulted between the
	// apps-suffix gate and Backend.Lookup in ServeHTTP.
	// nil = matcher disabled (default; pre-PR-3 behaviour).
	// Production wires it from cmd/gatewayd-internal/edge_rules.go
	// via WithEdgeRules so unit tests + the e2e harness can
	// opt out without touching the gate. The matcher
	// substitutes the gateway.App end-to-end on a `kind=route`
	// hit — downstream RequireAuthn / PublicAuth / wake gate /
	// proxy all see the *target* app's context, not the
	// inbound host's (auth remains per-app).
	edgeRules EdgeRuleMatcher
	// geoReader is the pkg/geoip.Reader used by applyEdgeRuleGeo
	// (ADR-091 D21). A nil reader means the gate is disabled —
	// applyEdgeRuleGeo short-circuits to fall-through so the
	// daemon boots cleanly without a DB-IP file (§11 fail-open
	// spirit). Set via WithGeoReader.
	geoReader *geoip.Reader
	// resolveTargetApp is the closure the matcher uses to
	// swap the gateway.App when a `kind=route` rule fires.
	// It returns (App{}, false) when the slug is not found
	// or the target is cross-account — the matcher emits
	// `edge_rule.route_blocked` audit + `outcome=blocked`
	// metric in that case. nil = same as edgeRules==nil
	// (matcher disabled; pre-PR-3 behaviour preserved).
	resolveTargetApp ResolveTargetApp
	// edgeRuleAudit emits the `edge_rule.route_matched` /
	// `edge_rule.route_blocked` audit rows when a kind=route
	// rule fires (PR 3 only; PR 4-7 extend the kind set).
	// nil = audit-disabled (unit tests). Production wires
	// the gatewaydAuditor thin wrapper from
	// cmd/gatewayd-internal/edge_rules.go so the rows land
	// in the same events table every other gatewayd-scope
	// row uses.
	edgeRuleAudit EdgeRuleAuditor

	// jwtVerifier (ADR-091 / issue #561 PR 5) is the
	// per-rule JWT verify handle consulted by applyEdgeRuleJWT.
	// It is wired by cmd/gatewayd-internal/main.go from the
	// pkg/edgejwks.Verifier constructed against the per-URL JWKS
	// cache; nil = JWT kind disabled (unit tests + pre-PR-5 builds).
	jwtVerifier JWTVerifier

	// internalSvcVerifier (ADR-119 / issue #477 #4) is the
	// per-service public-key allowlist consulted by
	// applyIngressInternalSvc and the parallel
	// SynthServer.applyIngressInternalSvc (synth.go). It is
	// wired by cmd/gatewayd-internal/internal_svc_verifier.go
	// from the env-loaded FAAS_INTERNAL_SVC_PUBKEYS map (a JSON
	// document mapping svcName→PEM-encoded Ed25519 public key).
	// nil = internal_only mode disabled — the gate short-circuits
	// to a 500 "operator_error" if an app is somehow in
	// internal_only mode without the verifier wired (defence in
	// depth: a misconfiguration that lets every internal-only
	// request through is worse than a 500).
	internalSvcVerifier InternalSvcVerifier

	// validator (PR-B) is the per-rule JSON-Schema validate
	// handle consulted by applyEdgeRuleValidate. Wired via
	// WithValidator from cmd/gatewayd-internal/edge_validate.go
	// (which adapts the pkg/edgevalidate.Manager to the
	// gateway.Validator interface). nil = validate kind
	// disabled (unit tests + pre-PR-B builds). The applier
	// short-circuits to fall-through when nil — same posture
	// as jwtVerifier above.
	validator Validator

	// drain (issue #587 / PR-A) is the per-request WaitGroup-backed
	// tracker the gateway's shutdown drain waits on. nil = drain
	// disabled (default for unit tests + the e2e harness that
	// doesn't exercise graceful shutdown). Wired from
	// cmd/gatewayd-internal/main.go via WithInFlightTracker so
	// production has ONE tracker per daemon, shared with the
	// control mux + InternalReverseProxy + TraceHandler via the
	// same setter on each. ServeHTTP does
	// `defer h.drain.Begin("http")()` so every return path
	// (including the early-out problem writes) is bounded by
	// the drain budget. The Begin closure is no-op-safe when
	// h.drain == nil — see the wrapper in serveHTTPWithDrain
	// for the nil guard.
	drain *drain.Tracker
}

// emptyAccountWarned is the process-wide trip flag for
// warnEmptyAccountOnce (ADR-040). Empty AccountID only appears in
// fakeBackend unit tests; production joins always populate it. Logging
// every test invocation would flood logs in `-count=10` runs.
var emptyAccountWarned atomic.Bool

// warnEmptyAccountOnce logs a debug-level warning the first time ServeHTTP
// sees an App with an empty AccountID (ADR-040). After that, subsequent
// occurrences are silent. The atomic flag is process-scoped, not per-Handler,
// because the warning is genuinely a code-path bug and one log line is
// enough to surface it.
func (h *Handler) warnEmptyAccountOnce() {
	if emptyAccountWarned.CompareAndSwap(false, true) {
		if h.log != nil {
			h.log.Debug("gateway: app.AccountID empty at rate-limit check; passing through unmetered (ADR-040)",
				"note", "production joins always populate AccountID; empty only in fakeBackend tests")
		}
	}
}

// NewHandler wires the edge with the spec's defaults (wake queue 512/30 s, spec
// §4.1) and the new Metrics + slog bundle. The host→app routing cache lives
// inside the Backend (it fronts Postgres).
func NewHandler(backend Backend) *Handler {
	return NewHandlerWith(backend, NewMetrics(), slog.Default())
}

// NewHandlerWith lets tests inject a custom Metrics bundle (to assert on the
// registry) and a custom slog logger.
func NewHandlerWith(backend Backend, m *Metrics, log *slog.Logger) *Handler {
	h := &Handler{
		backend: backend,
		limiter: NewLimiter(),
		// routeLimiter is built with NewLimiterWithLRU (#887) so
		// the per-rule bucket map — keyed by appID+"\x00"+ruleID —
		// cannot grow unboundedly; full-bucket-only eviction
		// invariant means an attacker cannot weaponise eviction to
		// bypass a partially-drained rule.
		routeLimiter: NewLimiterWithLRU(EdgeRuleCacheCap),
		// routeConsumerLimiter (ADR-104, issue #881 Phase 3) is
		// the per-rule per-consumer bucket map, separate scope so
		// per-rule buckets and per-consumer buckets never fight
		// for slots. Sized 10x the per-rule cap because the
		// per-consumer scope multiplies by the per-rule
		// MaxKeysPerRule ceiling (default 1000) — see plan
		// §"Eviction interaction". The full-bucket-only invariant
		// is inherited from routeLimiter's discipline; the
		// __other__ collapse bucket is additionally pinned (see
		// ratelimit.go::bucket.pinned).
		routeConsumerLimiter: NewLimiterWithLRU(EdgeRuleConsumerCacheCap),
		accountLimiter:       NewLimiter(),
		gate:                 NewWakeGate(api.WakeQueueCap, time.Duration(api.WakeQueueTTLSeconds)*time.Second),
		admissionQueue: newWakeAdmissionQueue(
			api.GatewayWakeAdmissionParallelism,
			api.GatewayWakeAdmissionQueueCap,
			func(plan string, depth int) {
				if m != nil {
					m.SetWakeAdmissionQueueDepth(plan, depth)
				}
			},
		),
		burstPressure: &burstPressure{},
		metrics:       m,
		log:           log,
		// mirrorSlots is sync.Map (zero value ready); the cap is
		// loaded from api.MirrorMaxConcurrentPerRule (default 5)
		// so the per-rule VM cost circuit matches the MirrorMaxLifetimeSeconds
		// envelope. PR-A3 code-review fix #3 moved ownership from
		// schedd's Engine to here so the cap reflects "VMs in
		// flight" (admit → round-trip complete), not "admit attempts".
		MirrorMaxConcurrentPerRule: api.MirrorMaxConcurrentPerRule,
	}
	// piApps is a value-typed sync.Map wrapper; its zero value is valid
	// and no init is required (avoiding a lazy-init write that would
	// race with parallel ServeHTTP readers — the load test under -race
	// caught that pattern; see the field doc).
	h.proxyFor = defaultProxy
	return h
}

// WithAppsSuffix sets the configured wildcard suffix filter (call before serving).
// When set, every request whose Host doesn't end in this suffix is rejected
// with 404 BEFORE consulting the cache. Custom domains on a different suffix
// are intended to be reached via the Lookup table directly (M5); this PR only
// adds the wildcard-apps-domain guard.
func (h *Handler) WithAppsSuffix(suffix string) *Handler {
	// Leading dot normalization so callers can pass either form.
	if suffix != "" && suffix[0] != '.' {
		suffix = "." + suffix
	}
	h.appsSuffix = strings.ToLower(suffix)
	return h
}

// WithLastSeenSink installs the LastSeenSink that records per-instance
// last_request_at (spec §4.1). Production wires a PG-flushing impl from
// schedd; tests use the in-memory implementation (idle.go).
func (h *Handler) WithLastSeenSink(sink LastSeenSink) *Handler {
	h.lastSeen = sink
	return h
}

// WithLimiter installs the per-app rate limiter. Production wires the
// token-bucket Limiter; load tests install an unlimitedLimiter so they
// aren't constrained by the plan rps/burst from newTestHandler's
// PlanPro default (which would 429 ~half of a 1k rps test). Treat this
// as a test-only seam; do NOT expose it as a config knob.
func (h *Handler) WithLimiter(l *Limiter) *Handler {
	h.limiter = l
	return h
}

// WithCentralBackend (ADR-104 amendment 5, issue #881 Phase 4 C3)
// installs the production CentralBackend on every per-process
// Limiter the Handler owns (per-app, per-account, per-rule,
// per-consumer). nil is accepted — the call sites fall back to
// the noopCentralBackend default. Production wiring lives in
// cmd/gatewayd-internal/run.go's centralBackendFromConfig helper
// and is conditioned on cfg.RateLimit.Mode == "central".
//
// Single-threaded wire-time invariant: this MUST be called
// before ServeHTTP starts accepting requests. Mutating the
// backend mid-flight would race with the boundary-case consult
// (Limiter.central is read without holding the limiter mutex).
func (h *Handler) WithCentralBackend(central CentralBackend) *Handler {
	if central == nil {
		return h
	}
	if h.limiter != nil {
		h.limiter.central = central
	}
	if h.accountLimiter != nil {
		h.accountLimiter.central = central
	}
	if h.routeLimiter != nil {
		h.routeLimiter.central = central
	}
	if h.routeConsumerLimiter != nil {
		h.routeConsumerLimiter.central = central
	}
	return h
}

// InvalidateRateLimit (ADR-104 amendment 5, issue #881 Phase 4
// C4) drops the in-process bucket for (scope, subjectID, plan)
// on every Limiter the Handler owns. Called by the
// LISTEN-side invalidator (pkg/wire/pgratelimit_invalidator.go)
// on every 'rate_limit_changed' pg_notify tick. nil subjectID
// is rejected (defence-in-depth — a malformed payload from an
// adversarial daemon cannot wipe every bucket for the scope).
//
// Backward-compat note: the Limiter's existing Forget /
// ForgetAccount API is keyed by appID / accountID, not by the
// (scope, subject_id, plan) triple. We translate the triple
// back into the matching key shape before calling Forget.
// This is intentionally minimal — a future PR that gives the
// Limiter a more granular Forget(scope, subjectID, plan)
// signature can replace this shim without changing the wire.
func (h *Handler) InvalidateRateLimit(scope, subjectID, plan string) {
	if h == nil || subjectID == "" {
		return
	}
	switch scope {
	case rateLimitScopeApp:
		for _, l := range h.limiters() {
			l.Forget(subjectID)
		}
	case rateLimitScopeAccount:
		for _, l := range h.limiters() {
			l.ForgetAccount(subjectID)
		}
	case rateLimitScopeRule:
		// Rule scope: per-rule bucket key is
		// appID+"\x00"+ruleID. We don't have appID in the
		// payload (subject_id IS the rule UUID), so we walk
		// every bucket looking for a key that ends with the
		// subject_id suffix. This is O(n) over the LRU map
		// per notification — bounded by EdgeRuleCacheCap (10k)
		// so acceptable as a degraded-mode invalidation.
		// A future PR can add a per-rule Forget(scope, ruleID)
		// to skip the scan.
		h.invalidateRuleBySuffix(subjectID)
	default:
		// Unknown scope: drop the notification silently. The
		// SQL CHECK would have rejected it before the trigger
		// fired, so this branch only catches a future scope
		// addition without a handler update.
	}
}

// limiters returns the slice of *Limiter the Handler owns;
// helper for InvalidateRateLimit so every scope walks the same
// set without copy-pasting the four fields.
func (h *Handler) limiters() []*Limiter {
	out := make([]*Limiter, 0, 4)
	if h.limiter != nil {
		out = append(out, h.limiter)
	}
	if h.accountLimiter != nil {
		out = append(out, h.accountLimiter)
	}
	if h.routeLimiter != nil {
		out = append(out, h.routeLimiter)
	}
	if h.routeConsumerLimiter != nil {
		out = append(out, h.routeConsumerLimiter)
	}
	return out
}

// invalidateRuleBySuffix scans the per-rule + per-consumer LRU
// maps for any bucket whose key ends with "\x00"+ruleID and
// drops it. O(n) per notification but bounded by
// EdgeRuleCacheCap (10k) + EdgeRuleConsumerCacheCap (100k).
func (h *Handler) invalidateRuleBySuffix(ruleID string) {
	suffix := "\x00" + ruleID
	for _, l := range h.limiters() {
		for _, k := range l.bucketKeys() {
			if strings.HasSuffix(k, suffix) {
				l.Forget(k)
			}
		}
	}
}

// WithRouteConsumerLimiter (ADR-104, issue #881 Phase 3) installs
// the per-rule per-consumer token-bucket throttle. Production
// wires NewLimiterWithLRU(EdgeRuleConsumerCacheCap) in NewHandlerWith
// alongside WithLimiter; load tests install a noop limiter so
// per-consumer accounting doesn't constrain the test's own rate
// generator. nil disables per-consumer throttling — the throttle
// applier falls back to the per-rule AllowWithParams path (PR #887
// back-compat shape), so a test that doesn't wire this surface
// exercises the Phase 1+2 behaviour bit-for-bit.
func (h *Handler) WithRouteConsumerLimiter(l *Limiter) *Handler {
	h.routeConsumerLimiter = l
	return h
}

// WithAccountLimiter installs the per-account rate limiter (ADR-040).
// Production wires the token-bucket Limiter constructed in NewHandlerWith;
// load tests install an unlimitedAccountLimiter so they aren't constrained
// by the plan RPM from newTestHandler's PlanPro default. Same test-only
// contract as WithLimiter — do NOT expose as a config knob.
func (h *Handler) WithAccountLimiter(l *Limiter) *Handler {
	h.accountLimiter = l
	return h
}

// WithForwarding installs the per-node HTTP→gRPC forwarder built by
// pkg/gateway/forwardproxy.go (issue #98 / ADR-028). When set, every
// request dispatches through fn(nodeID) where nodeID is the value
// Backend.Pick returned. nil-safe: pass nil to revert to the legacy
// addr-based proxy path (used by tests and the e2e harness).
func (h *Handler) WithForwarding(fn func(t Target) http.Handler) *Handler {
	h.proxyByNode = fn
	return h
}

// WithRawForwarding installs the per-node raw-bytes Upgrade
// forwarder (issue #676 / ADR-080) built by
// pkg/gateway/forwardproxy.go's ForwardingRawReverseProxy. When
// set, every inbound Connection: Upgrade + Upgrade: <token>
// request whose App.WebSocketEnabled is true dispatches through
// fn(nodeID). Reuses the same NodeClientLookup that
// WithForwarding installs (the raw RPC runs on the same per-node
// gRPC channel as the plain-HTTP RPC). nil = the raw path is
// disabled (default for tests without the vmmd overlay; production
// wires this in cmd/gatewayd-internal/main.go alongside WithForwarding).
func (h *Handler) WithRawForwarding(fn func(t Target) http.Handler) *Handler {
	h.rawByNode = fn
	return h
}

// WithStreamingEnabled flips the per-app streaming response path on
// or off (issue #471 / ADR-047). Production default is false; the
// e2e harness + metal tests opt in per-process so PR-B's Flusher
// path can be exercised end-to-end without a fleet-wide rollout.
// The flag is a single global gate — the per-app apps.streaming_enabled
// column is read in ServeHTTP and combined with this flag to decide
// whether to actually stream. Mutates the receiver in place; the
// returned *Handler is the same pointer (fluent-chaining convention
// matching WithEgressSink / WithLimiter / etc).
func (h *Handler) WithStreamingEnabled(enabled bool) *Handler {
	h.streamingEnabled = enabled
	return h
}

// WithRouteMetricsEnabled (ADR-093) arms the operator kill-switch
// for the per-route observability surface. When false, every
// per-app routeSetFor lookup returns nil regardless of
// app.RouteMetricsEnabled — the customer's flag is inert. The
// setter is fluent for chaining (same shape as the rest of the
// Handler.With* family). Wired from
// cmd/gatewayd-internal/config.go's `[route_metrics] enabled`
// field via the runtime config init.
func (h *Handler) WithRouteMetricsEnabled(enabled bool) *Handler {
	h.routeMetricsEnabled = enabled
	return h
}

// WithRequireAuthn (issue #560) arms the per-deployment token
// gate. authn must satisfy RequireAuthnAuthenticator —
// production passes the *pkg/auth.Middleware from cmd/gatewayd-internal/
// (which exposes its Authn field). audit may be nil (audit-
// disabled mode); the authz branch still fires but the
// instances.authn_* rows are dropped. nil authn = the authz
// branch is silently disabled (matches the pre-issue behaviour
// where every app is public-by-default) so unit tests that
// don't exercise require_authn don't have to wire the chain.
//
// The setter returns *Handler for fluent chaining (same shape as
// every other Handler.With*).
func (h *Handler) WithRequireAuthn(authn RequireAuthnAuthenticator, audit RequireAuthnAuditor) *Handler {
	h.requireAuthnAuthn = authn
	h.requireAuthnAudit = audit
	return h
}

// WithEdgeRules (ADR-089 / issue #561 PR 3) arms the
// per-host edge-rule matcher. matcher may be nil (matcher
// disabled; pre-PR-3 behaviour preserved for unit tests +
// the e2e harness). resolve may be nil when matcher is
// nil; when matcher is non-nil resolve MUST be non-nil —
// production wires the AppBySlug closure from
// cmd/gatewayd-internal/run.go so the same-account check
// at matchAndSubstituteRoute sees a real AccountID on
// every target App. audit may be nil (audit-disabled
// mode); the matcher still substitutes the App but the
// `edge_rule.route_matched` / `edge_rule.route_blocked`
// rows are dropped (mirrors WithRequireAuthn's
// `audit may be nil` shape).
//
// Returns *Handler for fluent chaining (same shape as
// every other Handler.With*).
func (h *Handler) WithEdgeRules(matcher EdgeRuleMatcher, resolve ResolveTargetApp, audit EdgeRuleAuditor) *Handler {
	h.edgeRules = matcher
	h.resolveTargetApp = resolve
	h.edgeRuleAudit = audit
	return h
}

// WithGeoReader (ADR-091 D21 / §4.1.2.8b) arms the per-rule
// geographic lookup consulted by applyEdgeRuleGeo. r may be nil
// (geo kind disabled; pre-PR-7 + file-missing posture). The
// nil-receiver-safe pattern in pkg/geoip.Reader.Lookup means a
// nil reader keeps the gate fail-open — the matcher can still
// surface a geo rule, but the lookup returns ("", false, nil)
// and the rule does not fire.
func (h *Handler) WithGeoReader(r *geoip.Reader) *Handler {
	h.geoReader = r
	return h
}

// WithJWTVerifier (ADR-091 / issue #561 PR 5) arms the per-rule
// JWT verify handle consulted by applyEdgeRuleJWT. v may be nil
// (JWT kind disabled; pre-PR-5 + unit-test posture). The verifier
// is a pkg/edgejwks.Verifier wrapped behind the narrow JWTVerifier
// interface so pkg/gateway doesn't import pkg/edgejwks (the
// cmd-side adapter builds the wrapper in
// cmd/gatewayd-internal/edge_rules_jwks.go).
func (h *Handler) WithJWTVerifier(v JWTVerifier) *Handler {
	h.jwtVerifier = v
	return h
}

// WithValidator (PR-B) arms the per-rule JSON-Schema validate
// handle consulted by applyEdgeRuleValidate. v may be nil
// (validate kind disabled; unit tests + pre-PR-B posture).
// Production wires the cmd-side adapter
// (cmd/gatewayd-internal/edge_validate.go) which adapts
// pkg/edgevalidate.Manager to the narrow Validator interface
// so pkg/gateway never imports pkg/edgevalidate.
func (h *Handler) WithValidator(v Validator) *Handler {
	h.validator = v
	return h
}

// WithPublicAuth (issue #477 / ADR-079) arms the per-app
// public-URL auth gate. cache may be nil (no caching; the
// basic-auth path unseals per-request, slower but correct).
// unsealer may be nil (the basic-auth path returns 500 on
// miss; open/bearer modes are unaffected). Both fields are
// nil-safe at the call site — enforcePublicAuth short-
// circuits when either is nil AND mode='basic', and the
// open/bearer branches don't touch either. The setter
// returns *Handler for fluent chaining (same shape as every
// other Handler.With*).
func (h *Handler) WithPublicAuth(cache *PublicAuthCache, unsealer PublicAuthUnsealer) *Handler {
	h.publicAuthCache = cache
	h.publicAuthUnsealer = unsealer
	return h
}

// WithResponseCache (ADR-122 §Decision) wires the in-process
// ResponseCache consulted by applyEdgeRuleCache. The setter
// returns *Handler for fluent chaining (same shape as every
// other Handler.With*).
func (h *Handler) WithResponseCache(cache *ResponseCache) *Handler {
	h.responseCache = cache
	return h
}

// WithMirrorRoundTripper (issue #72 / ADR-124 PR-A3) wires the
// HTTP forwarder the dispatch goroutine uses to reach the mirror
// VM. nil is safe (dispatchMirror defaults to
// NewDefaultMirrorRoundTripper(nil) on a nil field); tests inject
// a stub to assert on the request shape without standing up a
// real upstream. Fluent setter, mirrors WithResponseCache.
func (h *Handler) WithMirrorRoundTripper(rt MirrorRoundTripper) *Handler {
	h.mirrorRoundTripper = rt
	return h
}

// enforceRequireAuthn is the per-deployment token-gate
// (issue #560). Returns true when the request is authorised to
// proceed (either the routed app has RequireAuthn=false OR the
// caller presented a valid bearer token belonging to the app's
// owning account); returns false after writing the deny
// response and the metrics observation. The boolean keeps the
// call-site in ServeHTTP at one line (already-extracted from
// the hot path to stay under the 50-line handler cap, per
// golangci-lint v2.4.0 handler checks).
//
// Deny paths:
//
//	401 unauthorized     no Authorization header, or the
//	                     bearer token doesn't match
//	                     api.ValidAPIKeyFormat (mirrors the
//	                     shape pkg/auth.Middleware uses).
//	401 unauthorized     bearer key is unknown / revoked /
//	                     expired (the audit row distinguishes
//	                     the cause — see instances.authn_*
//	                     below).
//	403 insufficient_scope  bearer resolves to a valid key,
//	                     but the key's account_id != the
//	                     app's account_id. Distinct from
//	                     401 so the SDK can pivot on the
//	                     response code instead of having to
//	                     inspect the body.
//
// Audit emission:
//
//	instances.authn_missing  401 path, no/garbled header
//	instances.authn_invalid  401 path, key unknown /
//	                         revoked / expired
//	instances.authn_scope    403 path, account mismatch
//
// All three audit rows carry app_id + slug + key_id (when
// known) so an operator dashboard can pivot by app and by
// caller. The subject pointer is the account ID (the key's
// owner) for scope, nil for missing/invalid (no principal to
// stamp). Best-effort — a failed emit never blocks the deny
// response (matches the gatewaydAuditor.Emit contract).
func (h *Handler) enforceRequireAuthn(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	// Disabled path: not gated, or no auth chain wired.
	// Both branches preserve the pre-issue "public by default"
	// behaviour so unit tests + dev boxes don't need to set
	// up a fake authenticator.
	if !app.RequireAuthn || h.requireAuthnAuthn == nil {
		return true
	}
	// (1) Bearer extraction — fail-fast at 401 if no token.
	tok := bearerTokenFromHeader(r.Header.Get("Authorization"))
	if tok == "" || !api.ValidAPIKeyFormat(tok) {
		h.emitAuthnAudit(r, app, nil, "instances.authn_missing", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"reason": "missing_or_malformed_bearer",
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "this app requires an API key; present it as `Authorization: Bearer <token>`"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (2) Verify the key via the shared pkg/auth.Authenticator.
	// The hash call uses the same api.HashAPIKey helper the
	// apid middleware uses (defense in depth — keys never
	// reach the store unhashed).
	acct, key, err := h.requireAuthnAuthn.AuthenticateKey(r.Context(), api.HashAPIKey(tok))
	if err != nil {
		// Distinguish expired/revoked (the store's typed
		// sentinels) from "unknown key" so the audit row
		// carries forensic detail. The customer-facing
		// response stays 401 in both cases — leaking the
		// distinction would tell an attacker whether a
		// given key prefix exists.
		reason := authnReasonInvalidBearer
		if errors.Is(err, ErrAPIKeyExpired) {
			reason = authnReasonExpired
		} else if errors.Is(err, ErrAPIKeyRevoked) {
			reason = authnReasonRevoked
		}
		h.emitAuthnAudit(r, app, nil, "instances.authn_invalid", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"reason": reason,
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "the presented API key is not valid for this app"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (3) Cross-account check — a valid key on the wrong
	// account is 403, not 401. The audit subject is the key's
	// owning account so the operator can see "who tried".
	if acct.ID != app.AccountID {
		h.emitAuthnAudit(r, app, &acct.ID, "instances.authn_scope", map[string]any{
			"app_id":            app.ID,
			"slug":              r.Host,
			"caller_account_id": acct.ID,
			"app_account_id":    app.AccountID,
			"key_id":            key.ID,
		})
		rec.status = http.StatusForbidden
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Insufficient scope", "this API key does not belong to the account that owns the app"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// Phase 3 (ADR-104, issue #881): stamp the resolved API key
	// id on the request context so applyEdgeRuleThrottle can key
	// a per-consumer bucket when the matched rule opts into
	// key_by="api_key". Pre-Phase-3 code dropped key.ID at the
	// audit boundary; Phase 3 keeps it via withAuthenticated. The
	// context.Value setter is a single map insertion — no
	// measurable cost on the authn hot path.
	*r = *r.WithContext(withAuthenticated(r.Context(), Authenticated{
		APIKeyID: key.ID,
	}))
	return true
}

// emitAuthnAudit is a tiny wrapper around the optional auditor
// so the three deny branches in enforceRequireAuthn read
// symmetrically. Nil-safe: tests + dev boxes wire a nil
// auditor and the row is silently dropped.
func (h *Handler) emitAuthnAudit(r *http.Request, app App, subject *string, kind string, data map[string]any) {
	if h.requireAuthnAudit == nil {
		return
	}
	h.requireAuthnAudit.Emit(r.Context(), kind, subject, data)
}

// matchAndSubstituteRoute (ADR-089 / issue #561 PR 3) consults
// the per-host edge-rule matcher BEFORE Backend.Lookup. On a
// `kind=route` hit, *app is overwritten with the target App
// (already populated with the same-account check) and returns
// true. The caller (`ServeHTTP`) skips Backend.Lookup and
// jumps straight to `haveApp` so downstream RequireAuthn /
// PublicAuth / wake gate / proxy all see the *target* app's
// context (auth remains per-app).
//
// Returns false when:
//
//   - the matcher is nil (pre-PR-3 behaviour preserved; unit
//     tests + the e2e harness wire nil and expect the legacy
//     host→app lookup)
//   - the matcher returned nil (no rule whose host, path,
//     method matched)
//   - the same-account check failed (rule.AccountID !=
//     targetApp.AccountID): emits `edge_rule.route_blocked`
//     audit + `outcome=blocked` metric, then SILENTLY falls
//     through. The customer-facing response is the same 404
//     as a clean miss so a malicious actor can't probe for
//     cross-account targets via timing or response shape.
//   - the target app was deleted (AppBySlug 404): the cache
//     is advisory; a stale read only widens the hit window.
//     We do NOT emit audit/metric for a transient miss —
//     the next request will hit the loader and the rule
//     is naturally dropped once its app row is gone.
//
// Nil-safe: h.edgeRules == nil OR h.resolveTargetApp == nil
// returns false and ServeHTTP proceeds with the legacy
// Backend.Lookup. Extracted from ServeHTTP to keep the
// handler cap under 50 lines.
func (h *Handler) matchAndSubstituteRoute(r *http.Request, app *App) bool {
	if h.edgeRules == nil || h.resolveTargetApp == nil {
		return false
	}
	rule := h.edgeRules.MatchRoute(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch(rateLimitScopeRoute, "miss")
		}
		return false
	}
	target, ok := h.resolveTargetApp(r.Context(), rule.TargetAppSlug)
	if !ok {
		// Transient — the target app row was deleted (or
		// pending, or the slug was never on this box).
		// Silent fall-through; next request re-resolves.
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch(rateLimitScopeRoute, "miss")
		}
		return false
	}
	if target.AccountID != rule.AccountID {
		// Defense-in-depth on top of the apid create-time
		// same-account guarantee at
		// cmd/apid/handlers_edge_rules.go:184-201. Emits
		// the blocked audit + metric; the customer sees
		// the same 404 as a clean miss.
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.route_blocked", &target.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"to_slug":         rule.TargetAppSlug,
				"rule_account_id": rule.AccountID,
				"target_app_id":   target.ID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch(rateLimitScopeRoute, "blocked")
			// PR-B: cross-account is a defense-in-depth no-op, not an
			// apply failure. Surface as success so the §12 apply-rate
			// panel counts it as a successful (no-op) apply.
			h.metrics.ObserveEdgeRuleApply(rateLimitScopeRoute, "success")
		}
		return false
	}
	// Happy path: audit + metric, then substitute.
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.route_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"to_slug":   rule.TargetAppSlug,
			"app_id":    target.ID,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch(rateLimitScopeRoute, "match")
		// PR-B: route substitution is the apply-path outcome. Every
		// successful match is a successful apply (substitute ran).
		h.metrics.ObserveEdgeRuleApply(rateLimitScopeRoute, "success")
	}
	*app = target
	return true
}

// matchAndApplyRewrite (ADR-089 / issue #561 PR 4) consults the
// per-host edge-rule matcher for a `kind=rewrite` rule. On a hit,
// the rule's `From` prefix is replaced with `To` on r.URL.Path in
// place (the upstream app sees the rewritten path). The matcher's
// same-account check (rule.AccountID != app.AccountID) emits the
// `edge_rule.rewrite_blocked` audit + `outcome=blocked` metric
// and silently falls through (same posture as
// matchAndSubstituteRoute's cross-account branch).
//
// Returns true when a rule fired AND the path was actually mutated
// — the caller proceeds normally (no short-circuit). nil-safe:
// h.edgeRules == nil returns false.
//
// Extracted from ServeHTTP to keep the handler cap under 50 lines.
func (h *Handler) matchAndApplyRewrite(r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchRewrite(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("rewrite", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.rewrite_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"from_path":       r.URL.Path,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("rewrite", "blocked")
			// PR-B: cross-account is a defense-in-depth no-op.
			h.metrics.ObserveEdgeRuleApply("rewrite", "success")
		}
		return false
	}
	// Apply the prefix-strip + replacement. From="" means
	// "match-any path" (the path-glob filter passed); we still
	// need a non-empty To to actually mutate. A rule with From=""
	// but To="/v1" effectively prefixes every request — the spec
	// §4.1.2 documents this. From="*" is treated identically.
	from := rule.From
	if from == "*" {
		from = ""
	}
	if from == "" {
		// Pure prefix-add: prepend To to the existing path. Both
		// singleSlash(To) and r.URL.Path start with "/" so we can't
		// just concatenate — that produces "//api/x" (double slash)
		// when To="/" (valid per apid EdgeRuleRewriteAction.Validate
		// — non-empty is the only check). We special-case To="/"
		// (degenerate rewrite, leave path alone) and otherwise
		// concatenate the single-slashed To with r.URL.Path as-is.
		// For To="/v1" + /api/x → "/v1/api/x".
		to := singleSlash(rule.To)
		if to == "/" {
			// Degenerate rewrite (from="", To="/") — leave
			// r.URL.Path unchanged.
		} else {
			r.URL.Path = to + r.URL.Path
		}
	} else if strings.HasPrefix(r.URL.Path, from) {
		r.URL.Path = singleSlash(rule.To) + r.URL.Path[len(from):]
	} else {
		// The path-glob filter matched but the From prefix
		// doesn't actually prefix the path (e.g. glob="/api/*"
		// matched "/api/v1" but From="/v1/"). Treat as miss —
		// the customer wrote inconsistent config; we don't fire.
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("rewrite", "miss")
		}
		return false
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.rewrite_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"from":      from,
			"to":        rule.To,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("rewrite", "match")
		// PR-B: the rewrite was applied (path mutated in place).
		h.metrics.ObserveEdgeRuleApply("rewrite", "success")
	}
	return true
}

// matchAndApplyRedirect (ADR-089 / issue #561 PR 4) consults the
// per-host edge-rule matcher for a `kind=redirect` rule. On a hit,
// emits a 3xx via http.Redirect (stdlib) — additional headers from
// the rule's EdgeRuleRedirectAction.Headers map are stamped on the
// response via w.Header().Set BEFORE the redirect. Returns true
// when a rule fired; the caller MUST `return` immediately (the
// request short-circuits — no Lookup, no proxy).
//
// Same-account posture mirrors matchAndSubstituteRoute: cross-account
// falls through silently with `edge_rule.redirect_blocked` audit +
// `outcome=blocked` metric. nil-safe.
//
// Extracted from ServeHTTP to keep the handler cap under 50 lines.
func (h *Handler) matchAndApplyRedirect(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchRedirect(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("redirect", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.redirect_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"to":              rule.To,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("redirect", "blocked")
			// PR-B: cross-account is a defense-in-depth no-op.
			h.metrics.ObserveEdgeRuleApply("redirect", "success")
		}
		return false
	}
	// Stamp additional response headers (the rule's Headers map).
	for k, v := range rule.Headers {
		w.Header().Set(k, v)
	}
	status := rule.StatusCode
	if status != http.StatusMovedPermanently &&
		status != http.StatusFound &&
		status != http.StatusTemporaryRedirect &&
		status != http.StatusPermanentRedirect {
		status = http.StatusFound // 302 default per pkg/api/dto.go Validate
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.redirect_matched", nil, map[string]any{
			"rule_id":     rule.ID,
			"from_host":   r.Host,
			"to":          rule.To,
			"status_code": status,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("redirect", "match")
		// PR-B: redirect is an apply-path outcome (3xx written,
		// request short-circuits). http.Redirect always writes a
		// 3xx, so this counts as success even though the response
		// isn't 2xx.
		h.metrics.ObserveEdgeRuleApply("redirect", "success")
	}
	//nolint:gosec // rule.To is validated by apid Validate (pkg/api/dto.go:3337-3357) at create time: must be a non-empty URL/path. The customer's free-form redirect target IS the product surface — same posture as Cloudflare's "URL redirect" rules.
	http.Redirect(w, r, rule.To, status)
	return true
}

// applyEdgeRuleHeaders (ADR-089 / issue #561 PR 4) consults the
// per-host edge-rule matcher for a `kind=headers` rule. On a hit,
// applies RequestHeaders to r (mutates r.Header in place BEFORE
// the proxy leg) and ResponseHeaders to w (wraps w with a
// headerRecorder that applies ops on WriteHeader BEFORE the
// downstream status code is committed).
//
// Same-account posture mirrors matchAndSubstituteRoute: cross-account
// falls through silently with `edge_rule.headers_blocked` audit +
// `outcome=blocked` metric. nil-safe.
//
// Returns true when a rule fired (caller proceeds normally).
// Extracted from ServeHTTP to keep the handler cap under 50 lines.
func (h *Handler) applyEdgeRuleHeaders(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchHeaders(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("headers", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.headers_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("headers", "blocked")
			// PR-B: cross-account is a defense-in-depth no-op.
			h.metrics.ObserveEdgeRuleApply("headers", "success")
		}
		return false
	}
	// Request-side ops: apply directly to r.Header.
	for _, op := range rule.RequestHeaders {
		applyHeaderOp(r.Header, op)
	}
	// Response-side ops: wrap w with a headerRecorder that
	// applies ops at WriteHeader commit time. Reuses the same
	// statusRecorder pattern that PR-B streaming introduced.
	if len(rule.ResponseHeaders) > 0 && rec != nil {
		rec.installHeaderOps(rule.ResponseHeaders)
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.headers_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"req_ops":   len(rule.RequestHeaders),
			"resp_ops":  len(rule.ResponseHeaders),
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("headers", "match")
		// PR-B: headers were applied (request + response ops installed).
		h.metrics.ObserveEdgeRuleApply("headers", "success")
	}
	return true
}

// applyEdgeRuleCORS (ADR-091 / issue #561 PR 5) consults the
// per-host edge-rule matcher for a `kind=cors` rule.
//
// Two branches:
//   - method == OPTIONS + a CORS rule hits → 204 with
//     Access-Control-Allow-* headers stamped, no Backend.Lookup.
//     Returns true to signal "request short-circuited — caller must
//     `return`".
//   - any method + a CORS rule hits → stamps response-side headers
//     (Access-Control-Allow-Origin echoing the request Origin or
//     "*"; conditional AllowCredentials; AllowMethods echo) via the
//     statusRecorder.installHeaderOps hook, then falls through.
//     Returns false so the caller proceeds to enforceRequireAuthn /
//     public-auth / wake gate / proxy.
//
// Returns false on: edgeRules nil, no matching rule, origin not in
// AllowOrigins (browser will block client-side). Same-account
// posture mirrors matchAndSubstituteRoute: cross-account falls
// through silently with `edge_rule.cors_blocked` audit +
// `outcome=blocked` metric.
//
// Extracted from ServeHTTP to keep the handler cap under 50 lines.
func (h *Handler) applyEdgeRuleCORS(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchCORS(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("cors", "miss")
		}
		// CORS improvements D1/D4: per-app default CORS
		// fallback. Runs after the kind=cors miss and
		// before the JWT/IP gates. The OPTIONS
		// short-circuit is intentionally SKIPPED here —
		// the customer's backend is the authority on
		// the preflight answer; the gateway only
		// stamps response headers. A preflight still
		// reaches the customer code; the response gets
		// Allow-Origin + Allow-Methods + Allow-Headers
		// stamped on the way out.
		// nil → never set (schema default); *false →
		// explicit opt-out (col == false); *true →
		// opt-in. The nil-safe check collapses the
		// "schema default" + "explicit opt-out"
		// cases to a single "don't stamp" path so
		// the hot path is a single non-nil test.
		if app.CORSDefaultEnabled != nil && *app.CORSDefaultEnabled && len(app.CORSDefaultOrigins) > 0 {
			origin := r.Header.Get("Origin")
			allowedOrigin := matchOrigin(app.CORSDefaultOrigins, origin)
			if origin != "" && allowedOrigin != "" && rec != nil {
				rec.installHeaderOps(corsDefaultOps(allowedOrigin))
			}
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.cors_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("cors", "blocked")
			// PR-B: cross-account is a defense-in-depth no-op.
			h.metrics.ObserveEdgeRuleApply("cors", "success")
		}
		return false
	}
	origin := r.Header.Get("Origin")
	allowedOrigin := matchOrigin(rule.AllowOrigins, origin)
	if origin != "" && allowedOrigin == "" {
		// Origin not in the allowlist — browser will block
		// client-side. We still proxy the request (no
		// Access-Control-Allow-Origin means the browser drops it),
		// so this is a miss for the cors metric.
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("cors", "miss")
		}
		return false
	}
	respOps := corsResponseOps(rule, allowedOrigin)
	if len(respOps) > 0 && rec != nil {
		rec.installHeaderOps(respOps)
	}
	if r.Method == http.MethodOptions {
		// Preflight short-circuit: 204 + headers, no proxy.
		for _, op := range respOps {
			applyHeaderOp(w.Header(), op)
		}
		w.WriteHeader(http.StatusNoContent)
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.cors_matched", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"preflight": true,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("cors", "match")
			// PR-B: preflight 204 short-circuit is a successful apply
			// (the gate fired and wrote its response). CORS preflight
			// intentionally short-circuits the JWT/IP gates that
			// follow — see handler.go:2315-2320 doc-comment + ADR-091
			// Amendment 1 + FaasEdgeRuleJWTFailures.md.
			h.metrics.ObserveEdgeRuleApply("cors", "success")
		}
		return true
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.cors_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"preflight": false,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("cors", "match")
		// PR-B: non-preflight CORS stamps response-side Allow-Origin
		// via statusRecorder; the request falls through to the JWT /
		// IP gates. Counts as a successful apply.
		h.metrics.ObserveEdgeRuleApply("cors", "success")
	}
	return false
}

// matchOrigin returns the value to stamp in
// Access-Control-Allow-Origin — either "*" (echo back the wildcard),
// a literal origin from the request (if it matches an entry in
// allowList), or the echoed origin when it matches a
// subdomain/port-wildcard entry. Empty string means "no match; do
// not stamp the header".
//
// CORS improvements D2/D6:
//   - Subdomain wildcards: "https://*.example.com" matches
//     "https://app.example.com" but not
//     "https://app.sub.example.com" (only one label of wildcard,
//     no chained "**").
//   - Port wildcards: "https://localhost:*" matches
//     "https://localhost:3000" and "https://localhost:8080".
//   - Case-insensitive on scheme + host (RFC 6454 §3 says origins
//     are case-insensitive on scheme + host, case-sensitive on
//     path). Path matching is unchanged: a request Origin of
//     "https://APP.example.com/Path" still does NOT match an
//     allowList entry of "https://app.example.com" if the path
//     component is also part of the comparison (today it isn't —
//     Origin header has no path).
//
// The function is called once per request on the gateway hot path,
// so it is O(n) over allowList. The corsOriginPattern regex in
// pkg/api/dto.go validates the allowList entries at create-time;
// this function is the runtime mirror.
func matchOrigin(allowList []string, origin string) string {
	if origin == "" {
		return ""
	}
	// RFC 6454 §3: scheme + host are case-insensitive.
	// Lowercase the scheme and host of the request Origin so
	// "HTTPS://App.Example.COM" matches the
	// "https://app.example.com" allowlist entry.
	origin = strings.ToLower(origin)
	for _, raw := range allowList {
		a := strings.ToLower(raw)
		if a == "*" || a == origin {
			return a
		}
		// Subdomain wildcard: "https://*.example.com" → match
		// any "https://<single-label>.example.com". We split
		// on "://" so the ".*" pattern only applies to the
		// host segment, not the scheme.
		sch, hostSuffix, ok := splitScheme(a)
		if !ok {
			continue
		}
		rSch, rHost, ok2 := splitScheme(origin)
		if !ok2 {
			continue
		}
		if sch != rSch {
			continue
		}
		// Subdomain wildcard: "*.<rest>" — match any host
		// with exactly one extra label prefixed to <rest>.
		if strings.HasPrefix(hostSuffix, "*.") {
			suffix := hostSuffix[2:] // strip "*."
			suffixLabels := strings.Count(suffix, ".")
			if strings.HasSuffix(rHost, "."+suffix) &&
				strings.Count(rHost, ".") == suffixLabels+1 {
				return a // echo the lower-cased allowlist entry
			}
		}
		// Port wildcard: "<host>:*" — match any port.
		if strings.HasSuffix(hostSuffix, ":*") {
			prefix := strings.TrimSuffix(hostSuffix, ":*")
			if strings.HasPrefix(rHost, prefix+":") {
				return a
			}
		}
	}
	return ""
}

// splitScheme is a tiny helper that returns (scheme, "host[:port]")
// for an origin of the form "scheme://host[:port]". Used by
// matchOrigin to peel off the scheme before applying the
// subdomain/port wildcard predicates. Returns false when the input
// has no "://" separator (which the apid validator rejects at
// create-time, so this is a runtime guard against a future schema
// loosening that bypasses apid).
func splitScheme(origin string) (scheme, rest string, ok bool) {
	idx := strings.Index(origin, "://")
	if idx < 0 {
		return "", "", false
	}
	return origin[:idx], origin[idx+3:], true
}

// corsDefaultOps is the per-app default CORS header op set (CORS
// improvements D1). Mirrors the shape of corsResponseOps above
// but is opinionated: the gateway stamps a fixed
// Allow-Methods (GET, POST, OPTIONS — the methods a CORS client
// is most likely to need) and Allow-Headers: * (the per-app
// default is permissive because the customer has opted in
// to "just allow my origin" — the per-method / per-header
// surface is what kind=cors edge rules are for). No
// Allow-Credentials (the default is for uncredentialed
// cross-origin GETs; a customer wanting credentials adds a
// kind=cors rule, which takes precedence over the default).
// The function takes the resolved allowedOrigin (already
// lower-cased + matched by matchOrigin) so the response
// always echoes the matched entry verbatim.
//
// ADR-102: Streaming-Status + Streaming-Status-Accept-Hint are
// custom (non-simple) response headers, so a CORS client cannot
// read them unless the server whitelists them via
// Access-Control-Expose-Headers. The default uncredentialed path
// appends both header names so browser clients see the
// discoverability signal without the customer authoring a
// kind=cors rule. A kind=cors rule that sets its own
// Expose-Headers (corsResponseOps) takes precedence per the
// existing precedence rule and overrides this default.
func corsDefaultOps(allowedOrigin string) []EdgeRuleHeaderOp {
	return []EdgeRuleHeaderOp{
		{Action: "set", Name: "Access-Control-Allow-Origin", Value: allowedOrigin},
		{Action: "set", Name: "Access-Control-Allow-Methods", Value: "GET, POST, OPTIONS"},
		{Action: "set", Name: "Access-Control-Allow-Headers", Value: "*"},
		{Action: "set", Name: "Access-Control-Expose-Headers", Value: "Streaming-Status, Streaming-Status-Accept-Hint"},
	}
}

// corsResponseOps turns a CORS rule + resolved allowedOrigin into the
// header ops to stamp on the response. Allow-Credentials and
// Allow-Methods are conditional; Expose-Headers is a literal copy.
func corsResponseOps(rule *EdgeRuleCORSResolved, allowedOrigin string) []EdgeRuleHeaderOp {
	if allowedOrigin == "" {
		return nil
	}
	var ops []EdgeRuleHeaderOp
	ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Origin", Value: allowedOrigin})
	if rule.AllowCredentials {
		ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Credentials", Value: "true"})
	}
	if len(rule.AllowMethods) > 0 {
		ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Methods", Value: strings.Join(rule.AllowMethods, ", ")})
	}
	if len(rule.AllowHeaders) > 0 {
		ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Headers", Value: strings.Join(rule.AllowHeaders, ", ")})
	}
	if len(rule.ExposeHeaders) > 0 {
		ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Expose-Headers", Value: strings.Join(rule.ExposeHeaders, ", ")})
	}
	if rule.MaxAgeSeconds > 0 {
		ops = append(ops, EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Max-Age", Value: fmt.Sprintf("%d", rule.MaxAgeSeconds)})
	}
	return ops
}

// applyEdgeRuleJWT (ADR-091 / issue #561 PR 5) consults the
// per-host edge-rule matcher for a `kind=jwt` rule. On a hit:
//   - No Authorization header → 401 (edge_rule.jwt_missing audit
//   - outcome=missing metric).
//   - Present header but token fails verify → 401
//     (edge_rule.jwt_failed audit + outcome=failed metric; the
//     JWTVerifier distinguishes bad-sig / wrong-iss / wrong-aud /
//     missing-claim / expired).
//   - Token verifies → fall through (the rule's effects are
//     "this request reached a JWT-protected app" — the app may
//     also have require_authn=true which is enforced downstream).
//
// Returns true if the helper wrote a 401 (caller must `return`).
// nil-safe: h.edgeRules nil OR h.jwtVerifier nil short-circuit to
// fall-through. Same-account posture mirrors matchAndSubstituteRoute.
func (h *Handler) applyEdgeRuleJWT(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil || h.jwtVerifier == nil {
		return false
	}
	rule := h.edgeRules.MatchJWT(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		// Clean miss: no rule for this host. The match counter
		// surfaces this on the §12 dashboard chip; an audit row
		// here would be 100% noise (every request that doesn't
		// hit a JWT rule for this host). Match the pre-PR-A
		// behaviour: metric increment only, no audit emit. (The
		// original code at lines 1287-1292 of PR-A's parent
		// commit did the same.)
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("jwt", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		h.jwtEmit(r.Context(), "jwt", "blocked", rule.ID, r.Host, &rule.AccountID, map[string]any{
			"rule_account_id": rule.AccountID,
			"app_account_id":  app.AccountID,
		})
		return false
	}
	raw := bearerTokenFromHeader(r.Header.Get("Authorization"))
	if raw == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeUnauthorized, "Missing bearer token",
			"Authorization: Bearer <token> required for this edge rule"))
		h.jwtEmit(r.Context(), "jwt", "missing", rule.ID, r.Host, nil, nil)
		return true
	}
	claims, err := h.verifyJWTWithDeadline(r.Context(), raw, rule)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeUnauthorized, "JWT verification failed", err.Error()))
		h.jwtEmit(r.Context(), "jwt", "failed", rule.ID, r.Host, nil, map[string]any{"err": err.Error()})
		return true
	}
	h.jwtEmit(r.Context(), "jwt", "match", rule.ID, r.Host, nil, map[string]any{"sub": claims.Subject})
	// Phase 3 (ADR-104, issue #881): stamp the resolved JWT
	// subject + custom claims on the request context so
	// applyEdgeRuleThrottle can key a per-consumer bucket when the
	// matched rule opts into key_by="jwt_subject" or
	// key_by="jwt_claim". Claims.Custom is the string→string
	// subset the verifier extracted from rule.RequiredClaims — no
	// extra parse cost on the hot path.
	*r = *r.WithContext(withAuthenticated(r.Context(), Authenticated{
		JWTSubject: claims.Subject,
		JWTClaims:  claims.Custom,
	}))
	return false
}

// jwtEmit (ADR-091 hardening PR-A) consolidates the audit + metric
// emission for an applyEdgeRuleJWT outcome into one helper so the
// gate handler stays under the 50-line lint budget. fromHost is
// the customer-visible Host header (the audit's load-bearing field
// for operator triage). accountID may be nil (the unmatched /
// missing-token / failed paths don't tie the row to a customer
// account — only the blocked path does, to flag cross-account
// rule attempts). extras is merged into the audit row data map;
// nil is fine (audit.Emit tolerates nil maps). Both h.edgeRuleAudit
// and h.metrics are nil-receiver-safe; the helper guards with simple
// nil checks so the call sites stay one-liners.
func (h *Handler) jwtEmit(ctx context.Context, kind, outcome, ruleID, fromHost string, accountID *string, extras map[string]any) {
	if h.edgeRuleAudit != nil {
		data := map[string]any{
			"rule_id":   ruleID,
			"from_host": fromHost,
		}
		for k, v := range extras {
			data[k] = v
		}
		h.edgeRuleAudit.Emit(ctx, "edge_rule."+kind+"_"+outcome, accountID, data)
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch(kind, outcome)
		// ADR-091 hardening PR-B: also fire the apply-path counter
		// here so the §12 "edge rule apply rate" panel surfaces the
		// JWT gate's outcome. Mapping per the plan: match → success,
		// failed/missing → error (401 written), blocked → success
		// (cross-account fall-through, not an apply failure).
		switch outcome {
		case "match":
			h.metrics.ObserveEdgeRuleApply(kind, "success")
		case "failed", "missing":
			h.metrics.ObserveEdgeRuleApply(kind, "error")
		case "blocked":
			h.metrics.ObserveEdgeRuleApply(kind, "success")
		}
	}
}

// verifyJWTWithDeadline wraps h.jwtVerifier.Verify with the
// platform's edge-rule JWT verify deadline (api.EdgeRuleJWTVerifyTimeoutDefault,
// 5 s by default). The wall-clock cap defends against a slow IdP /
// unreachable JWKS endpoint holding the request goroutine inside
// applyEdgeRuleJWT indefinitely; r.Context() carries the upstream
// deadline (server ReadTimeout / client cancellation) so the
// tighter of the two wins.
//
// ADR-093 / PR-C: when the inbound request carries an end-to-end
// budget (installed at the gatewayd-public edge), the JWT verify
// hop becomes a child of that budget via reqbudget.WithCeiling.
// childDeadline = min(parentRemaining, EdgeRuleJWTVerifyTimeoutDefault)
// — the 5 s ceiling is unchanged; the budget can only tighten the
// cap. When no Budget is attached to ctx (admin / apid path that
// doesn't run through the edge middleware), the legacy
// context.WithTimeout ceiling is preserved so the JWT verify hop's
// hard 5 s safety cap cannot regress on the non-edge path.
//
// Returns the verifier's typed error verbatim — applyEdgeRuleJWT
// maps it into a 401 + edge_rule.jwt_failed audit + match counter
// increment. A deadline-exceeded error from context.WithTimeout
// surfaces as a 401 with the verifier's error string (the audit's
// `err` field gains a "context deadline exceeded" suffix; consumers
// that grep for the JWKS-typed errors in pkg/edgejwks/verifier.go
// continue to match them unchanged).
func (h *Handler) verifyJWTWithDeadline(ctx context.Context, raw string, rule *EdgeRuleJWTResolved) (*JWTClaims, error) {
	if h.jwtVerifier == nil {
		return nil, errors.New("jwt verifier not configured")
	}
	// PR-C: budget-aware ceiling when a budget is attached; legacy
	// 5 s WithTimeout when not (see doc comment).
	if b, ok := reqbudget.FromContext(ctx); ok {
		jwtCtx, cancel, _ := b.WithCeiling(ctx, api.EdgeRuleJWTVerifyTimeoutDefault)
		defer cancel()
		return h.jwtVerifier.Verify(jwtCtx, raw, rule)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EdgeRuleJWTVerifyTimeoutDefault)
	defer cancel()
	return h.jwtVerifier.Verify(ctx, raw, rule)
}

// applyEdgeRuleIP (ADR-091 / issue #561 PR 5) consults the
// per-host edge-rule matcher for a `kind=ip` rule. The client IP
// comes from the single trusted X-Forwarded-For hop that
// gatewayd-public writes before handing off to gatewayd-internal
// (see clientIPFromTrustedXFF — defense-in-depth guard rejects any
// request with 0 or >1 XFF entries as `caller_ip_forged`).
//
// On a hit:
//   - Deny matches → 403 (edge_rule.ip_blocked).
//   - Allow is empty (no allowlist) → fall through (only Deny
//     applies; if Deny also didn't match, the request passes).
//   - Allow is non-empty AND matches → fall through.
//   - Allow is non-empty AND no match → 403 (implicit deny).
//
// nil-safe: h.edgeRules nil short-circuits to fall-through. Same-
// account posture mirrors matchAndSubstituteRoute.
func (h *Handler) applyEdgeRuleIP(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchIP(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ip", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ip_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ip", "blocked")
			// PR-B: cross-account is a defense-in-depth no-op.
			h.metrics.ObserveEdgeRuleApply("ip", "success")
		}
		return false
	}
	clientIP, ok := clientIPFromTrustedXFF(r)
	if !ok {
		// Defense-in-depth: gatewayd-public is required to set
		// exactly one XFF entry. If we see 0 or >1, something
		// upstream tried to forge a client IP — fail closed (403)
		// rather than evaluating the rule against an untrusted
		// source.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Caller IP not in trusted set",
			"X-Forwarded-For did not contain exactly one entry; refusing to evaluate IP allow/deny rules"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.caller_ip_forged", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"xff_count": len(r.Header.Values("X-Forwarded-For")),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ip", "blocked")
			// PR-B: caller_ip_forged writes a 403; surface as apply
			// error so the §12 panel flags the forged-XFF attack.
			h.metrics.ObserveEdgeRuleApply("ip", "error")
		}
		return true
	}
	for _, denyNet := range rule.Deny {
		if denyNet.Contains(clientIP) {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
				api.CodeForbidden, "IP denied",
				"client IP matches a deny CIDR on this edge rule"))
			if h.edgeRuleAudit != nil {
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ip_blocked", nil, map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"cidr":      denyNet.String(),
					// ADR-091 D20 widening / PR-B: the offending client
					// IP so operators can grep the audit-events table
					// by IP without re-parsing XFF. clientIP is in
					// scope here — clientIPFromTrustedXFF has already
					// gated it through the defense-in-depth guard
					// (line 1491). PII: see the runbook's "audit-event
					// access control" section — masking is a separate
					// ADR and is out of scope for this PR.
					"client_ip": clientIP.String(),
				})
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("ip", "blocked")
				// PR-B: deny CIDR match wrote a 403.
				h.metrics.ObserveEdgeRuleApply("ip", "error")
			}
			return true
		}
	}
	if len(rule.Allow) > 0 {
		allowed := false
		for _, allowNet := range rule.Allow {
			if allowNet.Contains(clientIP) {
				allowed = true
				break
			}
		}
		if !allowed {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
				api.CodeForbidden, "IP not allowed",
				"client IP does not match any allow CIDR on this edge rule"))
			if h.edgeRuleAudit != nil {
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ip_blocked", nil, map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"implicit":  true,
					// ADR-091 D20 widening / PR-B: see the deny-CIDR
					// site above for the rationale — operators can now
					// see WHICH IP was implicitly denied, which is the
					// most common kind of false positive.
					"client_ip": clientIP.String(),
				})
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("ip", "blocked")
				// PR-B: implicit deny wrote a 403.
				h.metrics.ObserveEdgeRuleApply("ip", "error")
			}
			return true
		}
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ip_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			// ADR-091 D20 widening / PR-B: the matched IP for the
			// audit "this rule let through a request from IP X"
			// row. Same PII rationale as the deny sites above.
			"client_ip": clientIP.String(),
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("ip", "match")
		// PR-B: IP allow match — request falls through (no 403).
		h.metrics.ObserveEdgeRuleApply("ip", "success")
	}
	return false
}

// applyIngressIPAllowlist (ADR-118) is the per-app ingress IP
// allowlist gate. Runs in the per-app request chain BEFORE
// applyEdgeRuleIP (so an IP-blocked request short-circuits all
// edge-rule work and never wakes a Firecracker — same invariant
// as the geo gate at L4269 / ADR-091 D21).
//
// Trust chain is identical to applyEdgeRuleIP:
// clientIPFromTrustedXFF (defense-in-depth guard rejects any
// request with 0 or >1 XFF entries as `caller_ip_forged`).
//
// Empty allowlist + ip_allowlist mode is a HARD misconfig
// posture: a 500 (operator_error, "app is misconfigured") rather
// than a 403 (no rule matched). This is deliberate — a silent
// pass-through would mean every request wakes a Firecracker on
// an app that's supposed to be filtered, and an app that has
// been "armed" but not yet populated is the operator-side
// onboarding hole this loud posture is designed to surface. The
// apid handler never arms ip_allowlist mode without a list (the
// closed-enum validator at dto.go L792 rejects len==0 + ip_allowlist
// at the wire), so reaching the empty-list + ip_allowlist code
// path implies a SQL row hand-edit or a future regression that
// surfaced as a missing precondition — both operator errors.
//
// Audit vocabulary:
//   - edge_rule.ingress_ip_blocked — CIDR mismatch (implicit deny)
//   - edge_rule.ingress_ip_forged  — XFF chain wrong
//
// The kind="ingress_ip" metric label is pre-instantiated at
// pkg/gateway/metrics.go so an idle box renders zero-valued
// rows for the §12 dashboard chip.
func (h *Handler) applyIngressIPAllowlist(w http.ResponseWriter, r *http.Request, app App) bool {
	if app.PublicAuth.Mode != publicAuthModeIPAllowlist {
		return false
	}
	if len(app.PublicAuth.IPAllowlist) == 0 {
		// Operator misconfig: ip_allowlist mode without CIDRs.
		// 500 makes the noise operator-loud — the cold-boot
		// rate for this app stays low (no Firecracker wakes),
		// but every request 500s and shows up on the error
		// dashboard.
		h.log.Error("app in ip_allowlist mode with empty CIDR list — refusing",
			slog.String("app_id", app.ID),
			slog.String("slug", app.Slug))
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"app is misconfigured",
			"ip_allowlist mode requires at least one CIDR; update the app's public_auth ip_allowlist list"))
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_ip", "blocked")
			// CRIT-3 (review): misconfig wrote a 500 —
			// surface as apply error so the §12
			// "edge rule apply rate" chip doesn't stay
			// at 0 under misconfig attacks. Mirror
			// applyEdgeRuleIP's PR-B call pattern at
			// L2055.
			h.metrics.ObserveEdgeRuleApply("ingress_ip", "error")
		}
		return true
	}
	clientIP, ok := clientIPFromTrustedXFF(r)
	if !ok {
		// Defense-in-depth: same posture as applyEdgeRuleIP.
		// gatewayd-public is required to set exactly one XFF
		// entry before the unix-socket handoff.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Caller IP not in trusted set",
			"X-Forwarded-For did not contain exactly one entry; refusing to evaluate ingress IP allowlist"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_ip_forged", nil, map[string]any{
				"app_id":    app.ID,
				"from_host": r.Host,
				"xff_count": len(r.Header.Values("X-Forwarded-For")),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_ip", "blocked")
			// CRIT-3 (review): forged XFF wrote a 403.
			h.metrics.ObserveEdgeRuleApply("ingress_ip", "error")
		}
		return true
	}
	// netip.Prefix.Contains accepts a netip.Addr, not a net.IP —
	// convert the trusted client IP via the same parsing path
	// used elsewhere in the package. The conversion is best-
	// effort; an unparseable client IP is treated as a forge
	// (defense-in-depth) rather than a pass-through.
	//
	// net.ParseIP returns an IPv4 in 16-byte v4-mapped form
	// (::ffff:a.b.c.d) by default. netip.AddrFromSlice treats
	// 16-byte slices as IPv6 — without Unmap() a v4 client IP
	// would never match a v4 prefix in the allowlist. This is
	// the same convention the egress allowlist handler uses
	// (cmd/apid/handlers_ext.go:100-195) for the wire→store
	// parse, mirrored at the request layer.
	clientAddr, ok := netip.AddrFromSlice(clientIP)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Caller IP not in trusted set",
			"X-Forwarded-For contained an unparseable client IP"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_ip_forged", nil, map[string]any{
				"app_id":    app.ID,
				"from_host": r.Host,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("ingress_ip", "blocked")
			// CRIT-3 (review): unparseable client IP
			// wrote a 403 (forge path).
			h.metrics.ObserveEdgeRuleApply("ingress_ip", "error")
		}
		return true
	}
	clientAddr = clientAddr.Unmap()
	for _, prefix := range app.PublicAuth.IPAllowlist {
		if prefix.Contains(clientAddr) {
			// Pass-through. The matched metric emits the
			// match outcome so the §12 dashboard renders
			// non-zero "ingress_ip applied" rather than
			// only "blocked" — operators want to see the
			// allow side too.
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("ingress_ip", "match")
				// CRIT-3 (review): match is the
				// apply-success outcome for §12.
				h.metrics.ObserveEdgeRuleApply("ingress_ip", "success")
			}
			return false
		}
	}
	// Implicit deny — every CIDR failed to match.
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
		api.CodeForbidden, "client IP not in allowlist",
		"client IP does not match any allow CIDR on this app's ingress IP allowlist"))
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.ingress_ip_blocked", nil, map[string]any{
			"app_id":    app.ID,
			"from_host": r.Host,
			"client_ip": clientAddr.String(),
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("ingress_ip", "blocked")
		// CRIT-3 (review): implicit deny wrote a 403.
		h.metrics.ObserveEdgeRuleApply("ingress_ip", "error")
	}
	return true
}

// applyAppsMaintenanceMode (ADR-091 amendment / §4.1.2.0) is the
// coarse-grained per-app maintenance gate. When the matched app's
// apps.maintenance_mode column is true, every inbound request is
// short-circuited with 503 + Retry-After (default 60 s, env-overridable
// via FAAS_EDGE_RULE_MAINTENANCE_RETRY_AFTER_SECONDS, hard cap 24 h
// per pkg/api/limits.go) BEFORE auth, BEFORE wake, BEFORE any
// kind=maintenance rule (coarse gate beats fine-grained per D4
// ordering). Audit row carries the app slug for operator triage;
// ObserveAppMaintenance(plan) is the per-plan counter so the §12
// dashboard can break down "in maintenance" by plan tier.
//
// Returns true (caller MUST return) on apps.maintenance_mode=true.
// Returns false on apps.maintenance_mode=false (pass-through) or
// when the gate is nil (dev mode / pre-wiring path).
//
// Why coarse beats fine-grained: the customer's mental model is "I
// flipped maintenance_mode=true on the whole app". A 503 from the
// fine-grained rule with a different Problem.code would leak the
// existence of a per-route rule and confuse the operator. The two
// primitives compose because the coarse gate covers everything the
// fine-grained rules could ever cover; the fine-grained rules only
// matter when the app is NOT in coarse maintenance.
func (h *Handler) applyAppsMaintenanceMode(w http.ResponseWriter, r *http.Request, app App) bool {
	if !app.MaintenanceMode {
		return false
	}
	api.WriteProblem(w, api.ErrAppMaintenanceMode(api.EdgeRuleMaintenanceRetryAfterSeconds, app.Slug))
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "app.maintenance_mode_match", nil, map[string]any{
			"app_id":      app.ID,
			"app_slug":    app.Slug,
			"from_host":   r.Host,
			"method":      r.Method,
			"path":        r.URL.Path,
			"retry_after": api.EdgeRuleMaintenanceRetryAfterSeconds,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveAppMaintenance(string(app.Plan))
	}
	return true
}

// applyEdgeRuleMaintenance (ADR-091 amendment / §4.1.2.13) consults
// the per-host edge-rule matcher for a `kind=maintenance` rule. On a
// hit, the inbound request is short-circuited with 503 + Retry-After
// at the per-rule cap (default EdgeRuleMaintenanceRetryAfterSeconds,
// 60 s; hard cap MaxEdgeRuleMaintenanceRetryAfterSeconds, 24 h)
// BEFORE auth, BEFORE wake, BEFORE the proxy leg. The fine-grained
// sibling of applyAppsMaintenanceMode — distinct Problem.code so a
// customer can tell which gate fired.
//
// Returns true (caller MUST return) on:
//
//   - 503 maintenance (edge_rule_maintenance + Retry-After +
//     optional Problem.detail when the rule carried a Message).
//
// Returns false on a rule miss (audit "miss"), same-account
// mismatch (defense-in-depth — audit "blocked"), or when h.edgeRules
// is nil (dev mode). Cross-account rules silently fall through
// (mirrors applyEdgeRuleIP's defense-in-depth posture at line 1547).
//
// Streaming posture: short-circuits unconditionally — no
// ApplyWhileStreaming knob (mirroring the maintenance 503 always
// fires, even on a streaming response, because the customer-facing
// intent is "this endpoint is in maintenance, do not pay the wake
// cost"). The "Maintenance" detail in Problem.detail is the
// customer's `Message` field (≤ 512 B; surfaced via api.WriteProblem
// as Problem.detail so monitoring / curl users see why the endpoint
// is dark without scraping the rule row).
func (h *Handler) applyEdgeRuleMaintenance(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchMaintenance(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("maintenance", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.maintenance_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("maintenance", "blocked")
			// Cross-account blocked is a defense-in-depth no-op —
			// emit apply success so the §12 dashboard chip doesn't
			// falsely flag the cross-account rule as a wire error.
			// Mirrors applyEdgeRuleLimit (handler.go:1958) and
			// applyEdgeRuleIP (handler.go:1559).
			h.metrics.ObserveEdgeRuleApply("maintenance", "success")
		}
		return false
	}
	api.WriteProblem(w, api.ErrEdgeRuleMaintenance(rule.RetryAfterSeconds, rule.Message))
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.maintenance_matched", nil, map[string]any{
			"rule_id":     rule.ID,
			"from_host":   r.Host,
			"method":      r.Method,
			"path":        r.URL.Path,
			"retry_after": rule.RetryAfterSeconds,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("maintenance", "match")
		// 503 is a non-2xx wire write — emit apply success because
		// the applier did its job (audit + metric + Problem wire);
		// the §12 "wire errors" chip only surfaces internal
		// unexpected failures, not customer-facing 503s. Mirrors
		// the kind=limit 413 outcome at handler.go:2000 which uses
		// "error" for the 413 path; the difference is that 413 is
		// "client error / contract violation" while 503 is "service
		// answer / contract met". Apply success is correct for
		// maintenance.
		h.metrics.ObserveEdgeRuleApply("maintenance", "success")
	}
	return true
}

// applyEdgeRuleValidate (PR-B) consults the per-host edge-rule
// matcher for a `kind=validate` rule. On a hit, the inbound
// request body is buffered (up to the per-rule cap or
// api.MaxRequestBodyBytes, whichever is smaller), the compiled
// JSON-Schema is consulted via h.validator, and r.Body is restored
// to a fresh reader over the buffered bytes so the proxy leg
// downstream still sees the body.
//
// Returns true (caller MUST return) on:
//
//   - 422 schema mismatch (request_validation_failed + FieldError).
//   - 415 unsupported Content-Type (when rule.ContentTypes is set
//     and the request's Content-Type doesn't match).
//   - 413 body cap exceeded (request_too_large — applies the
//     per-rule cap; if 0, falls back to api.MaxRequestBodyBytes).
//   - 502/500 alarm-worthy compile/runtime errors.
//
// Returns false on a clean match (audit + metric "match"), a
// rule miss ("miss"), a same-account mismatch ("blocked"), a
// streaming skip (rule.ApplyWhileStreaming=false + upgrade request),
// or when both h.edgeRules and h.validator are nil (dev mode).
//
// Body restore: the buffered body is re-installed as r.Body so
// the downstream proxy leg reads the same bytes. This is the
// first production hot-path body-restore in pkg/gateway (no
// existing handler does this); the test-file idiom at
// pkg/gateway/dns01_provider_hetzner_test.go:48-49 informed the choice of
// io.NopCloser(bytes.NewReader(buf)).
func (h *Handler) applyEdgeRuleValidate(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) bool {
	if h.edgeRules == nil || h.validator == nil {
		return false
	}
	rule := h.edgeRules.MatchValidate(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "blocked")
			// PR-C: cross-account blocked is a defense-in-depth
			// no-op — emit apply success so the §12 dashboard
			// chip doesn't falsely flag the cross-account rule
			// as a wire error. Mirrors applyEdgeRuleIP
			// (handler.go:1487).
			h.metrics.ObserveEdgeRuleApply("validate", "success")
		}
		return false
	}
	// Upgrade / streaming short-circuit: the body for an
	// upgraded request is read by the proxy leg's hijacker,
	// not buffered. Validate rules opt-in via
	// rule.ApplyWhileStreaming; default false = skip.
	if !rule.ApplyWhileStreaming && isUpgradeRequest(r) {
		return false
	}
	// Content-Type gate: when the rule restricts Content-Types,
	// anything outside the list returns 415. Empty
	// ContentTypes = pass-through (back-compat with rules that
	// pre-date the field).
	ct := r.Header.Get("Content-Type")
	if len(rule.ContentTypes) > 0 && !contentTypeAllowed(ct, rule.ContentTypes) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnsupportedMediaType,
			api.CodeUnsupportedMediaType, "Unsupported media type",
			fmt.Sprintf("rule %s requires one of %v; got %q", rule.ID, rule.ContentTypes, ct)))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_unsupported_media_type", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"got":       ct,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "blocked")
			// PR-C: 415 Unsupported Media Type is a non-2xx wire
			// write — emit apply error so the §12 chip surfaces
			// the rejected pre-flight on the customer side.
			h.metrics.ObserveEdgeRuleApply("validate", "error")
		}
		return true
	}
	// Buffer the body (already bounded by the global
	// MaxBytesReader installed in ServeHTTP above this slot).
	// The per-rule cap is layered on top via an inner
	// MaxBytesReader so a rule with MaxBodyBytes=2KiB
	// short-circuits before the global cap fires.
	cap := rule.MaxBodyBytes
	if cap <= 0 || cap > api.MaxRequestBodyBytes {
		cap = api.MaxRequestBodyBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(cap))
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge,
				api.CodeRequestTooLarge, "Request body too large",
				fmt.Sprintf("rule %s caps body at %d bytes", rule.ID, cap)))
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest,
				api.CodeBadRequest, "Could not read request body", err.Error()))
		}
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"reason":    "body_read",
				"err":       err.Error(),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "failed")
			// PR-C: body-read failure (413 / 400) is a non-2xx
			// wire write — emit apply error so the §12 chip
			// surfaces the rejected request.
			h.metrics.ObserveEdgeRuleApply("validate", "error")
		}
		return true
	}
	// Restore r.Body so the proxy leg reads the same bytes.
	r.Body = io.NopCloser(bytes.NewReader(body))
	res, err := h.validator.Validate(r.Context(), &EdgeValidateIn{
		Body:        body,
		ContentType: ct,
	}, rule)
	if err != nil {
		switch {
		case errors.Is(err, ErrValidateSchemaExternalRef):
			// Compile-time defense fired at runtime —
			// shouldn't happen if apid-Validate was
			// correct. 502 signals "the gateway
			// dependency is broken"; ops will see the
			// alarm + slog.
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				api.CodeBadGateway, "Edge rule compile error",
				"validate rule contains an external $ref/$id; refusing to validate"))
		case errors.Is(err, ErrValidateSchemaInvalid),
			errors.Is(err, ErrValidateSchemaEmpty),
			errors.Is(err, ErrValidateSchemaTooLarge):
			// Broken stored schema — deploy bug. 500.
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeInternal, "Edge rule schema error",
				"validate rule schema is broken"))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				api.CodeInternal, "Edge rule validator error", err.Error()))
		}
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"reason":    "validator_error",
				"err":       err.Error(),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "failed")
			// PR-C: validator error (502 / 500) is a non-2xx
			// wire write — emit apply error so the §12 chip
			// surfaces the broken rule.
			h.metrics.ObserveEdgeRuleApply("validate", "error")
		}
		return true
	}
	if !res.OK {
		// Translate to api.FieldError on the 422 problem+json.
		// res.FirstError may be nil if the schema failed but
		// the library returned no FieldError — treat as a
		// generic 422 with an empty errors slice.
		var errs []api.FieldError
		if res.FirstError != nil {
			errs = []api.FieldError{{
				Field:    res.FirstError.Field,
				Expected: res.FirstError.Expected,
				Got:      res.FirstError.Got,
			}}
		}
		// validate_mode (issue #975 #3 / Mega-Foundation #979-a)
		// selects the post-failure behavior. Default empty string
		// == 'block' to match the schema-side default at 00293.
		// `observe` and `warn` never reject — they count the
		// failure in the validate_failures metric and let the
		// proxy leg run. `warn` additionally stamps
		// X-Validation-Warning: <rule_id> via the statusRecorder
		// so the customer's API consumer can see the warning
		// without the gateway changing the response status.
		//
		// Body has already been buffered and r.Body restored
		// above (line ~2384), so the proxy leg reads the same
		// bytes regardless of mode.
		mode := rule.ValidateMode
		if mode == "" {
			mode = api.ValidateModeBlock
		}
		var reason string
		if res.FirstError != nil {
			reason = res.FirstError.Reason()
		} else {
			reason = reasonOther
		}
		if h.metrics != nil {
			// ADR-128 §5: pass appID + ruleID so the
			// gateway_validate_failures_total counter can
			// localize failures to a specific rule on a
			// specific app. The rule_id label is admitted
			// through ruleLabelSet (cap 256 per app;
			// overflow → "__other__") so the Prometheus
			// series set stays bounded.
			h.metrics.ObserveEdgeRuleValidateFailure(rule.AppID, rule.ID, mode, reason)
		}
		switch mode {
		case api.ValidateModeObserve:
			// Counted, no header, no reject. Audit tag
			// fires so the failure is queryable from the
			// ledger even when the response is 200.
			if h.edgeRuleAudit != nil {
				auditData := map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"reason":    "schema_mismatch",
					"mode":      mode,
				}
				if res.FirstError != nil {
					auditData["field"] = res.FirstError.Field
					auditData["expected"] = res.FirstError.Expected
				}
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, auditData)
			}
			if h.metrics != nil {
				// Match is the "rule fired and returned a
				// verdict" counter; the per-{mode,reason}
				// counter is the validate_failures_total
				// line above. Both increment so the
				// dashboard can correlate.
				h.metrics.ObserveEdgeRuleMatch("validate", "match")
				h.metrics.ObserveEdgeRuleApply("validate", "success")
			}
			return false
		case api.ValidateModeWarn:
			// Like observe, plus stamp the X-Validation-Warning
			// header on the proxied response. The header op
			// goes through the same statusRecorder hook the
			// CORS / headers rules use, so the value lands
			// on the response on the way back to the client.
			// The header value is the rule ID (uuid), not
			// the failing field — keeps any PII in the
			// field path out of the response.
			rec.installHeaderOps([]EdgeRuleHeaderOp{
				{Action: "set", Name: "X-Validation-Warning", Value: rule.ID},
			})
			if h.edgeRuleAudit != nil {
				auditData := map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"reason":    "schema_mismatch",
					"mode":      mode,
				}
				if res.FirstError != nil {
					auditData["field"] = res.FirstError.Field
					auditData["expected"] = res.FirstError.Expected
				}
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, auditData)
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("validate", "match")
				h.metrics.ObserveEdgeRuleApply("validate", "success")
			}
			return false
		default:
			// 'block' (and the empty-string coerce) — the
			// pre-existing 422 path. Behavior preserved.
			api.WriteProblemWithErrors(w, api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeRequestValidationFailed, "Invalid request",
				fmt.Sprintf("body does not match schema for rule %s", rule.ID)), errs)
			if h.edgeRuleAudit != nil {
				auditData := map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"reason":    "schema_mismatch",
					"mode":      mode,
				}
				if res.FirstError != nil {
					auditData["field"] = res.FirstError.Field
					auditData["expected"] = res.FirstError.Expected
				}
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, auditData)
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("validate", "blocked")
				// PR-C: 422 schema mismatch is a non-2xx wire
				// write — emit apply error so the §12 chip
				// surfaces the customer's malformed payload.
				h.metrics.ObserveEdgeRuleApply("validate", "error")
			}
			return true
		}
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("validate", "match")
		// PR-C: validate happy path — request falls through
		// to the proxy leg. Emit apply success so the §12
		// chip tracks customer traffic that was body-validated.
		// Mirrors applyEdgeRuleIP (handler.go:1572).
		h.metrics.ObserveEdgeRuleApply("validate", "success")
	}
	return false
}

// streamingFor is the canonical 4-conjunct gate that decides
// whether a request is on the streaming opt-in path. Used at
// the §4.1.2.13 slot (applyEdgeRuleLimit call site) to pick
// between a rule's buffered and streaming caps. The proxy leg
// at handler.go:3193 has its own inline copy of the same formula
// because the streaming response-writer wrap is a separate
// concern from edge-rule application; a future refactor can lift
// both to this helper. Keep them in lockstep if the conjuncts
// ever grow — TestApplyEdgeRuleLimit_StreamingFor_FourConjuncts
// pins the §4.1.2.13 slot's view of the formula.
//
// The four conjuncts, in order:
//
//   - h.streamingEnabled: the process-wide opt-in flag, set via
//     WithStreamingEnabled on the Handler (cmd/gatewayd-internal).
//   - app.StreamingEnabled: the per-app opt-in flag, persisted in
//     apps.streaming_enabled and surfaced through the per-host
//     app cache. Without this, no per-app stream-bridge path is
//     wired in the picker, so a "streaming" cap would never
//     actually be reached.
//   - !isAcceptJSON(Accept): the streaming bridge is reserved
//     for long-lived event/stream responses. A request asking
//     for a JSON response is buffered even if the app is opted
//     in — the cap is the buffered cap in that case.
//   - !isUpgradeRequest(r): WebSocket / HTTP/2 upgrade requests
//     are long-lived but their body is read by the proxy leg's
//     hijacker, not buffered. Treat them as buffered for the
//     cap-selection purpose (the cap is the buffered cap).
//
// ADR-102: this gate is preserved for the applyEdgeRuleLimit call
// site which only needs a bool. The richer streamingDecision struct
// (with status + cap + capKind) lives in decideStreaming below;
// both helpers share the same four-conjunct predicate so they
// always agree on the boolean outcome.
func streamingFor(h *Handler, r *http.Request, app App) bool {
	_, isStreaming := decideStreaming(h, r, app)
	return isStreaming
}

// streamingDecision is the per-request resolution of the four-conjunct
// streaming gate (ADR-102 D1/D2). Status is the canonical enum value
// stamped into the Streaming-Status response header; Cap is the
// effective response body byte cap (plan-level for non-streaming
// variants, possibly overridden by a matched edge-rule for the
// streaming variant); CapKind labels the cap source for telemetry.
//
// All non-streaming variants carry the plan-level buffered cap
// (app.Plan.MaxResponseBodyBytes()); only StreamingStatusStreaming
// can carry an endpoint-rule cap (max_body_bytes_streaming from a
// matched edge rule — see effectiveResponseCap).
type streamingDecision struct {
	Status  api.StreamingStatus
	Cap     int64
	CapKind string // "plan" | "endpoint-rule" | "none"
}

// decideStreaming is the ADR-102 canonical gate (D1/D2). Returns
// the per-request classification that backs the Streaming-Status
// response header. The boolean second return value is the same
// four-conjunct predicate as the legacy streamingFor helper above,
// retained so applyEdgeRuleLimit's bool-typed signature doesn't
// need to change in this PR. The two helpers MUST agree — see
// streamingFor's body.
//
// Conjuncts, evaluated in precedence order (first failure wins):
//
//  1. h.streamingEnabled (operator opt-in via FAAS_GATEWAY_STREAMING)
//     → operator-disabled (plan cap)
//  2. app.StreamingEnabled (per-app flag from apps.streaming_enabled)
//     → flag-disabled (plan cap)
//  3. app.Plan.StreamingResponseAllowed() (D5 defense-in-depth
//     mirror of the apid CreateApp 403 — Free + flag rows must
//     never reach the streaming path even if a pre-D5 row
//     survives the data-tier migration gap)
//     → plan-disallows (plan cap)
//  4. isUpgradeRequest(r) (Connection: Upgrade set)
//     → upgrade-bypass (plan cap; the raw-bytes bridge handles it)
//  5. isAcceptJSON(r.Header.Get("Accept")) (pre-D3 advisory only;
//     D3 hard-flip means this does NOT downgrade to buffered
//     anymore, but the status still reflects what would have
//     downgraded pre-D3 so pinned-SDK customers can self-diagnose)
//     → accept-json-downgrade (plan cap)
//  5. otherwise → streaming (cap = plan or endpoint-rule cap)
//
// The plan cap is app.Plan.MaxResponseBodyBytes(); the endpoint-rule
// cap is set by looking up the matched edge rule at this call site
// (h.edgeRules.MatchLimit). The lookup is duplicated relative to
// applyEdgeRuleLimit's own call below at line ~2440 — accepted as
// cheap in-memory lookup; alternative (stashing the rule on the
// Handler between the two call sites) was rejected as cross-cutting
// state mutation that's harder to reason about than the double
// lookup. Same-account check is enforced inside MatchLimit so the
// endpoint-rule cap can never be hijacked cross-account.
func decideStreaming(h *Handler, r *http.Request, app App) (streamingDecision, bool) {
	planCap := app.Plan.MaxResponseBodyBytes()

	if !h.streamingEnabled {
		return streamingDecision{Status: api.StreamingStatusOperatorDisabled, Cap: planCap, CapKind: "plan"}, false
	}
	if !app.StreamingEnabled {
		return streamingDecision{Status: api.StreamingStatusFlagDisabled, Cap: planCap, CapKind: "plan"}, false
	}
	// Defense-in-depth mirror of the apid D5 CreateApp 403. By the
	// time the request reaches the gateway, the apid gate should
	// have prevented any Free + flag row from being persisted. If
	// a pre-D5 row survives (data-tier migration gap), this gate
	// turns the next request into a buffered response with the
	// plan-disallows enum so the Streaming-Status response header
	// surfaces the violation. The deferred CHECK constraint
	// (ADR-102 follow-up) ships once telemetry confirms zero
	// Free+flag rows in production.
	if !app.Plan.StreamingResponseAllowed() {
		return streamingDecision{Status: api.StreamingStatusPlanDisallows, Cap: planCap, CapKind: "plan"}, false
	}
	if isUpgradeRequest(r) {
		return streamingDecision{Status: api.StreamingStatusUpgradeBypass, Cap: planCap, CapKind: "plan"}, false
	}

	// Per-endpoint RESPONSE cap (ADR-102 D4). Mirrors the
	// request-side lookup that applyEdgeRuleLimit does at line
	// 2440; the duplicate call is the explicit decision per the
	// helper's doc above. Cap is the larger of the two
	// (request-side MaxBodyBytes clamped to 100 MiB;
	// response-side MaxBodyBytesStreaming up to the plan cap).
	cap, capKind := planCap, "plan"
	if h.edgeRules != nil {
		rule := h.edgeRules.MatchLimit(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
		if rule != nil && rule.AccountID == app.AccountID && rule.MaxBodyBytesStreaming > 0 {
			cap = int64(rule.MaxBodyBytesStreaming)
			capKind = "endpoint-rule"
		}
	}

	if isAcceptJSON(r.Header.Get("Accept")) {
		// D3 advisory: status is informational, the request DOES
		// stream. The accept-json-downgrade enum variant exists
		// for one release cycle so pinned-SDK customers whose
		// Accept defaults to JSON can self-diagnose via the
		// Streaming-Status header + Streaming-Status-Accept-Hint
		// advisory header.
		return streamingDecision{Status: api.StreamingStatusAcceptJSONDowngrade, Cap: cap, CapKind: capKind}, true
	}
	return streamingDecision{Status: api.StreamingStatusStreaming, Cap: cap, CapKind: capKind}, true
}

// decideProtocol (ADR-124) is the per-app wire-protocol selector.
// Returns the validated protocol enum to stamp on
// x-faas-protocol for the per-app forward proxy
// (pkg/gateway/forwardproxy.go) to read. The closed set
// {http1, http2, grpc} is enforced upstream by apid (the
// buildApp + updateApp gates), so this helper's only job is to
// (a) default to "http1" when the app carries an empty value
// (hand-built App{}s from internal callers and the pre-ADR-124
// in-memory fixtures in handler_test.go) and (b) reflect the
// app's configured value into the request header for the
// downstream proxy leg.
//
// The plan gate (Free + grpc → 403 plan_app_protocol_grpc_not_allowed)
// is enforced at apid and not re-checked here — by the time a
// request reaches the gateway, the row is already gated. The
// closed-set CHECK apps_app_protocol_chk (migration 00382) is
// the schema-level guard; a stale row that somehow carries a
// value outside the closed set is treated as "http1" so the
// forwarder never sees an unrecognised framing selector.
//
// Unlike decideStreaming, this helper does NOT consult the
// request shape or any Handler config — the customer's
// protocol choice is per-app, not per-request, so the
// (h *Handler, r *http.Request) signature of decideStreaming
// would only mislead a future reader. A request that arrives
// over H1 framing to an app with app_protocol=grpc is still
// routed through the gRPC code path; the customer's client
// (or the inner H2C bridge) is responsible for the framing
// conversion.
func decideProtocol(app App) string {
	switch app.AppProtocol {
	case api.AppProtocolHTTP1, "":
		return api.AppProtocolHTTP1
	case api.AppProtocolHTTP2:
		return api.AppProtocolHTTP2
	case api.AppProtocolGRPC:
		return api.AppProtocolGRPC
	default:
		// Closed-set CHECK guarantees this never lands in
		// practice; treat as http1 so the forwarder falls back
		// to the legacy H1 path on every unknown value.
		return api.AppProtocolHTTP1
	}
}

// applyEdgeRuleLimit (ADR-091 D24 / new ADR-0NN-edge-rule-limit)
// consults the per-host edge-rule matcher for a `kind=limit` rule.
// On a hit, r.Body is wrapped in http.MaxBytesReader at the
// per-rule cap so downstream reads (the proxy leg, the validate
// applier at §4.1.2.8b) cannot exceed it — the per-rule cap is
// always at most api.MaxRequestBodyBytes (cmd-side compileLimitRules
// clamps higher values), so this is a strict tightening of the
// global reader layered inside ServeHTTP.
//
// The load-bearing property is the **Content-Length fast path**:
// when the inbound request advertises a body larger than the cap
// via Content-Length, the applier writes 413 + RFC 7807
// request_too_large immediately, without reading a single body
// byte. A bare http.MaxBytesReader only trips when something
// reads the body, and at this hot-path slot nothing reads the
// body until the proxy leg — so without the fast path, a 30 MB
// POST against a 5 MB rule would buffer 30 MB into memory before
// tripping the cap. The fast path makes "never buffer an oversize
// request" a guarantee, not a hope.
//
// Returns true (caller MUST return) on:
//
//   - 413 request_too_large from the Content-Length fast path.
//   - 413 request_too_large from the MaxBytesReader trip on a
//     chunked (no Content-Length) oversize body — this case is
//     only reachable on a streaming opt-in path that didn't
//     trigger the fast path; the reader still catches it.
//
// Returns false on:
//
//   - nil rule (audit "miss", no cap installed).
//   - same-account mismatch (defense-in-depth — audit "blocked"
//   - apply "success" so the §12 chip doesn't falsely flag the
//     cross-account rule as a wire error; mirrors
//     applyEdgeRuleValidate at handler.go:1688–1706).
//   - clean match (audit "matched" + observe "match" + apply
//     "success", MaxBytesReader installed at the per-rule cap).
//
// Streaming posture (ADR-091 D24 §6): the rule carries an
// optional `max_body_bytes_streaming` field (≤ 100 MiB per
// pkg/api/limits.go:1652 RawStreamMaxRequestBytes, enforced by
// the apid-Validate path and by cmd-side compileLimitRules). At
// runtime, the call site computes `streamingFor(h, r, app)`
// (above) once and passes the result in as the `streaming`
// parameter; the cap-selection algorithm is:
//
//	cap = rule.MaxBodyBytes             // default "buffered"
//	capKind = "buffered"
//	if streaming && rule.MaxBodyBytesStreaming > 0 {
//	    cap = rule.MaxBodyBytesStreaming // opt-in streaming cap
//	    capKind = "streaming"
//	}
//
// Both paths run the same Content-Length fast path and the same
// MaxBytesReader install — only the cap value and the
// (audit/413-visible) cap_kind label differ. The DTO's
// `s ≥ b` invariant (pkg/api/dto.go::EdgeRuleLimitAction.Validate)
// is trusted at runtime: apid-Validate enforces it on the
// customer write path; a direct-DB row that violates it
// (seedEdgeRuleDirect) passes cmd-side compile (which clamps
// `s` to [0, 100 MiB] but not `s ≥ b`) and falls back to the
// buffered cap at runtime via the `MaxBodyBytesStreaming == 0`
// branch — safe degradation. See ADR-091 D24 §6 amendment and
// spec §4.1.2.13 for the rationale.
func (h *Handler) applyEdgeRuleLimit(w http.ResponseWriter, r *http.Request, streaming bool, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	rule := h.edgeRules.MatchLimit(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("limit", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.limit_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("limit", "blocked")
			// Cross-account blocked is a defense-in-depth no-op —
			// emit apply success so the §12 dashboard chip doesn't
			// falsely flag the cross-account rule as a wire error.
			// Mirrors applyEdgeRuleValidate (handler.go:1704) and
			// applyEdgeRuleIP (handler.go:1487).
			h.metrics.ObserveEdgeRuleApply("limit", "success")
		}
		return false
	}
	// Cap selection (ADR-091 D24 §6):
	//
	//   - streaming && rule.MaxBodyBytesStreaming > 0 → streaming cap
	//   - else → buffered cap (the existing path)
	//
	// Per-cap-kind defence-in-depth clamp: cmd-side compileLimitRules
	// already clamps at the per-kind ceiling, but a rule inserted
	// by a direct DB write that bypassed apid-Validate
	// (cmd/e2e/edge_rules_common_test.go:128 seedEdgeRuleDirect)
	// could carry an out-of-range value. The buffered clamp
	// mirrors the validate applier's clamp at handler.go:1746-1748.
	// The streaming clamp is per-kind (not the buffered ceiling)
	// because the streaming field's ceiling
	// (api.RawStreamMaxRequestBytes = 100 MiB) is strictly larger
	// than the buffered one (api.MaxRequestBodyBytes = 25 MiB) —
	// a single clamp to the buffered ceiling would silently
	// regress streaming allowances. capKind is threaded into
	// both the 413 detail suffix and the audit payload so a
	// customer debugging a 413 can bisect which cap fired.
	cap := rule.MaxBodyBytes
	capKind := "buffered"
	if streaming && rule.MaxBodyBytesStreaming > 0 {
		cap = rule.MaxBodyBytesStreaming
		capKind = "streaming"
	}
	if capKind == "buffered" {
		if cap <= 0 || cap > api.MaxRequestBodyBytes {
			cap = api.MaxRequestBodyBytes
		}
	} else {
		if cap <= 0 || int64(cap) > api.RawStreamMaxRequestBytes {
			cap = int(api.RawStreamMaxRequestBytes)
		}
	}
	// Content-Length fast path: deny before reading a single
	// body byte. r.ContentLength == 0 is treated as "unknown
	// (chunked or no body)" — fall through to the MaxBytesReader,
	// which will trip on the proxy leg's first read. r.ContentLength
	// == -1 is the http.NoBody sentinel — same fall-through.
	// A lying-low client (advertises small CL, sends large body)
	// cannot bypass: the MaxBytesReader below still trips on
	// the proxy leg's first read. The fast path can only ever
	// produce a false-positive 413, never a bypass — see
	// ADR-091 D24 §4 for the rationale. The detail suffix
	// "(buffered cap)" / "(streaming cap)" names the cap that
	// fired so a customer can see whether they tripped the
	// streaming opt-in or the buffered default.
	if r.ContentLength > 0 && r.ContentLength > int64(cap) {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge,
			api.CodeRequestTooLarge, "Request body too large",
			fmt.Sprintf("rule %s caps body at %d bytes (%s cap)", rule.ID, cap, capKind)))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.limit_rejected", nil, map[string]any{
				"rule_id":        rule.ID,
				"from_host":      r.Host,
				"content_length": r.ContentLength,
				"cap_bytes":      cap,
				"cap_kind":       capKind,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("limit", "blocked")
			// 413 is a non-2xx wire write — emit apply error so
			// the §12 chip surfaces the rejected pre-flight to
			// the customer. Mirrors applyEdgeRuleValidate's 422
			// path (handler.go:1736).
			h.metrics.ObserveEdgeRuleApply("limit", "error")
		}
		return true
	}
	// In-limit body — install MaxBytesReader at the per-rule cap.
	// This wraps r.Body so any subsequent read (the validate
	// applier at handler.go:1750, the proxy leg further down)
	// trips on the same cap if the body actually exceeds it.
	// The global reader at handler.go:2789 layers outside this
	// as the backstop for requests that don't match any limit
	// rule; nesting two MaxBytesReaders is safe — both clamp
	// to the smaller of their caps + the body, and the inner
	// reader (this one) only ever tightens.
	r.Body = http.MaxBytesReader(w, r.Body, int64(cap))
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.limit_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"cap_bytes": cap,
			"cap_kind":  capKind,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("limit", "match")
		// Limit happy path — request falls through to the proxy
		// leg (and to the validate applier if a validate rule
		// also matches). Emit apply success so the §12 chip
		// tracks customer traffic that was body-cap-matched.
		// Mirrors applyEdgeRuleIP / applyEdgeRuleValidate.
		h.metrics.ObserveEdgeRuleApply("limit", "success")
	}
	return false
}

// isUpgradeRequest is defined in pkg/gateway/upgrade.go. The
// validate applier reuses it.

// contentTypeAllowed reports whether ct matches any entry in
// allowed. Both sides are compared as-is (case-sensitive on
// the media-type, case-insensitive on the parameters). The
// apid-Validate path enforces "must start with application/" so
// the rule's entries are always a closed set of media types.
func contentTypeAllowed(ct string, allowed []string) bool {
	if ct == "" {
		return false
	}
	for _, a := range allowed {
		if a == ct {
			return true
		}
		// Match by media type only (ignore charset etc).
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			if strings.TrimSpace(ct[:i]) == a {
				return true
			}
		}
	}
	return false
}

// applyEdgeRuleGeo (ADR-091 D21 / §4.1.2.8b) consults the per-host
// edge-rule matcher for a `kind=geo` rule. Same shape as
// applyEdgeRuleIP: Deny walks first, then Allow is evaluated; an
// empty Allow is "no allowlist, only Deny applies"; a non-empty
// Allow with no match is an implicit deny. The country lookup is
// against the trusted XFF client IP via the configured
// pkg/geoip.Reader.
//
// Failure mode (§11 spirit): the lookup is fail-open. A missing
// DB, a corrupt file, an IP outside the dataset, or a decode
// error returns ("", false, err_or_nil) from the reader; the rule
// does not fire (we increment "failed" and emit a Warn log, but
// the request flows through). The operator sees the metric tick
// + the audit + the log so a missing-DB incident is detectable
// even though traffic is unaffected.
//
// nil-safe: h.edgeRules nil OR h.geoReader nil short-circuits to
// fall-through. Same-account posture mirrors applyEdgeRuleIP.
func (h *Handler) applyEdgeRuleGeo(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil || h.geoReader == nil {
		return false
	}
	rule := h.edgeRules.MatchGeo(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("geo", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.geo_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("geo", "blocked")
		}
		return false
	}
	clientIP, ok := clientIPFromTrustedXFF(r)
	if !ok {
		// Defense-in-depth: gatewayd-public is required to set
		// exactly one XFF entry. Same fail-closed posture as
		// applyEdgeRuleIP — the rule cannot be evaluated against
		// an untrusted source.
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Caller IP not in trusted set",
			"X-Forwarded-For did not contain exactly one entry; refusing to evaluate geo allow/deny rules"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.caller_ip_forged", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"xff_count": len(r.Header.Values("X-Forwarded-For")),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("geo", "blocked")
		}
		return true
	}
	country, found, lerr := h.geoReader.Lookup(clientIP)
	if lerr != nil {
		if h.log != nil {
			h.log.Warn("edge rule geo lookup failed", "ip", clientIP, "err", lerr)
		}
	} else if !found {
		if h.log != nil {
			h.log.Debug("edge rule geo lookup no country", "ip", clientIP)
		}
	}
	if lerr != nil || !found {
		// Fail-open: the rule does not fire. The metric + audit
		// surface lets the operator see the failure rate and
		// the customer is not affected.
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.geo_failed", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"reason":    geoFailReason(lerr, found),
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("geo", "failed")
		}
		return false
	}
	// Deny walks first.
	if _, denied := rule.Deny[country]; denied {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeForbidden, "Country denied",
			"client country matches a deny entry on this edge rule"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.geo_blocked", nil, map[string]any{
				"rule_id":   rule.ID,
				"from_host": r.Host,
				"country":   country,
				"decision":  "deny",
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("geo", "blocked")
		}
		return true
	}
	// If Allow is non-empty, only listed pass.
	if len(rule.Allow) > 0 {
		if _, allowed := rule.Allow[country]; !allowed {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
				api.CodeForbidden, "Country not allowed",
				"client country does not match any allow entry on this edge rule"))
			if h.edgeRuleAudit != nil {
				h.edgeRuleAudit.Emit(r.Context(), "edge_rule.geo_blocked", nil, map[string]any{
					"rule_id":   rule.ID,
					"from_host": r.Host,
					"country":   country,
					"implicit":  true,
				})
			}
			if h.metrics != nil {
				h.metrics.ObserveEdgeRuleMatch("geo", "blocked")
			}
			return true
		}
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.geo_matched", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"country":   country,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("geo", "match")
	}
	return false
}

// applyEdgeRuleThrottle (ADR-091 D20.5 amendment / issue #881 /
// §4.1.2.x) consults the per-host edge-rule matcher for a
// `kind=throttle` rule. On match, decrements the rule's bucket via
// the LRU-backed routeLimiter (constructed with NewLimiterWithLRU
// in NewHandlerWith, PR #887) keyed by `appID + "\x00" + ruleID`.
// The bucket's rps/burst come from the rule itself (cmd-side
// compileThrottleRules clamps non-positive values to {1, 1} as
// defence-in-depth). On deny, returns true after writing the
// problem+json 429 + x-faas-rate-limit-scope: route +
// X-RouteRateLimit-{Limit,Remaining,Reset}. On allow, returns
// false so the caller continues down the chain.
//
// Hot-path placement: runs AFTER applyEdgeRuleLimit
// (pkg/gateway/handler.go:2336 — kind=limit body-size cap) and
// BEFORE applyEdgeRuleValidate
// (pkg/gateway/handler.go:2037 — kind=validate schema gate).
// Rationale:
//
//   - requests denied by JWT/IP/Geo/Limit MUST NOT consume a route
//     token (already enforced by running AFTER them);
//   - the O(1) bucket lookup MUST run BEFORE validate allocates
//     and parses r.Body, so a throttled request never costs a
//     schema-validate pass;
//   - the bucket decrement is bounded by the rule's burst so a
//     sudden spike cannot starve legitimate traffic.
//
// Cross-account defence-in-depth: same posture as every other
// applier (ApplyLimit / ApplyGeo / ApplyMaintenance) — a rule
// that fires for a different account than the inbound App is
// silently passed over (audit `edge_rule.throttle_blocked` +
// metric `outcome=blocked`), so a direct-DB write that ignores
// same-account governance doesn't get to gate traffic.
//
// Phase 3 (ADR-104, issue #881 Phase 3): when the matched rule
// opts into a per-consumer KeyBy value (`api_key`, `jwt_subject`,
// `jwt_claim`), this applier reads the Authenticated struct from
// the request context (populated by enforceRequireAuthn and
// applyEdgeRuleJWT) and routes the bucket lookup through the
// routeConsumerLimiter (separate scope so per-rule and
// per-consumer buckets don't share slots). The __other__ collapse
// bucket — see ratelimit.go::AllowWithConsumerKey — is pinned
// non-evictable so an attacker cannot weaponise key-space growth
// to bypass the throttle. Empty KeyBy (and the "none" sentinel)
// preserve PR #887's `appID+"\x00"+ruleID` bucket key bit-for-bit.
//
// Returns true iff the caller MUST return (short-circuit on 429).
func (h *Handler) applyEdgeRuleThrottle(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil || h.routeLimiter == nil {
		return false
	}
	rule := h.edgeRules.MatchThrottle(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("throttle", "miss")
		}
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.throttle_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("throttle", "blocked")
			// Cross-account blocked is a defense-in-depth no-op —
			// emit apply success so the §12 dashboard chip doesn't
			// falsely flag the cross-account rule as a wire error.
			// Mirrors applyEdgeRuleLimit (handler.go:2357) and
			// applyEdgeRuleGeo (handler.go:2531).
			h.metrics.ObserveEdgeRuleApply("throttle", "success")
		}
		return false
	}
	// Bucket key is `appID + "\x00" + ruleID` for PR #887 rules
	// (KeyBy == "" or "none"). Per-consumer keying (Phase 3)
	// constructs a longer key by appending the resolved consumer
	// identity — see resolveConsumerKey below. The routeConsumerLimiter
	// owns the consumer-keyed buckets and applies the __other__
	// collapse; routeLimiter owns the rule-only buckets and is
	// untouched. Phase 1+2 rules never enter the per-consumer branch.
	bucketKey := app.ID + "\x00" + rule.ID
	// Per-rule throttle consult (ADR-104 amendment 5, issue #881
	// Phase 4 C3). When the daemon is running under
	// [ratelimit] mode = "central" (the cross-replica drift fix
	// documented in amendment 5), the local-would-reject branch
	// transparently consults pg_ratelimit_counters — wired via
	// AllowWithCentralParams + the centralKey "rule:<ruleID>:<plan>"
	// triple. Empty centralKey (mode = "local", the default) reproduces
	// today's AllowWithParams byte-for-byte. The fix to the Phase 4
	// C3 wiring gap: the per-rule call site MUST use the central-aware
	// sibling even though the boundary-case consult is invisible
	// under mode=local — otherwise enabling central mode in TOML
	// would not affect per-rule buckets and the multi-replica drift
	// documented in the 00126 schema would remain.
	centralKey := "rule:" + rule.ID + ":" + string(app.Plan)
	allowed := h.routeLimiter.AllowWithCentralParams(r.Context(), bucketKey, rule.RequestsPerSecond, float64(rule.Burst), centralKey)
	// consumerID is hoisted out of the per-consumer branch so the
	// 429 path (below) can read it for the X-RouteRateLimit-Policy
	// collapse detection (ADR-104 amendment 5, issue #881 Phase 4
	// H1). Empty string means "anonymous / not resolved" — the
	// policy-header computation treats that as "route" (no
	// collapse to advertise).
	var consumerID string
	if allowed && api.ThrottleKeyByIsPerConsumer(rule.KeyBy) {
		authed := authenticatedFrom(r.Context())
		var ok bool
		consumerID, ok = resolveConsumerKey(rule.KeyBy, rule.JWTClaimName, authed)
		if ok {
			// The consumer bucket key is the same
			// `appID+"\x00"+ruleID` prefix used by the per-rule
			// bucket, plus a "\x00"+consumerID suffix. The per-rule
			// AllowWithParams call above already admitted the
			// request into the rule scope (token decrement on the
			// rule bucket — the parent bucket the per-consumer
			// sub-buckets ride on); the per-consumer AllowWithConsumerKey
			// call throttles within that scope.
			//
			// When MaxKeysPerRule == 0 (resolver-default; cmd-side
			// compileThrottleRules substitutes the plan default) we
			// surface a sensible ceiling here. cmd-side is the source
			// of truth — but defence-in-depth against a direct-DB
			// write that bypassed compileThrottleRules.
			cap := rule.MaxKeysPerRule
			if cap <= 0 {
				cap = api.ThrottleMaxKeysPerRuleDefault
			}
			if !h.routeConsumerLimiter.AllowWithConsumerKey(bucketKey, consumerID, rule.RequestsPerSecond, float64(rule.Burst), cap) {
				allowed = false
				h.metrics.ObserveRouteConsumerThrottleDecision(rule.KeyBy, "throttle")
			} else {
				h.metrics.ObserveRouteConsumerThrottleDecision(rule.KeyBy, "admit")
			}
		} else {
			// Anonymous on a per-consumer rule — emit the
			// `anonymous` outcome so the dashboard can flag
			// authn-gated apps that are seeing unauthenticated
			// traffic on a per-consumer rule (a misconfiguration
			// signal).
			h.metrics.ObserveRouteConsumerThrottleDecision(rule.KeyBy, "anonymous")
		}
		// resolveConsumerKey returning ok=false means anonymous
		// traffic on a per-consumer rule (e.g. an unauthenticated
		// request hitting a rule with key_by=api_key). The PR #887
		// rule-only bucket has already been consumed; we treat
		// anonymous on a per-consumer rule as a free pass through
		// the per-consumer layer — anonymous collapses into the
		// rule scope via the AllowWithParams token. A future
		// hardening may explicitly 401 here, but for Phase 3 the
		// documented behaviour matches today (per-rule bucket
		// already throttled).
	}
	if !allowed {
		w.Header().Set("Retry-After", "1")
		// `route` is a new scope value alongside `account` + `app`
		// (established by per-account / per-app 429 paths
		// respectively) — see writeRouteRateLimitHeaders comment.
		w.Header().Set("x-faas-rate-limit-scope", "route")
		// ADR-104 amendment 5 (issue #881 Phase 4 H1): emit
		// X-RouteRateLimit-Policy so operators can read the
		// per-consumer collapse state without parsing the
		// X-RouteRateLimit-* numerics. `route` is the
		// back-compat default (Phase 1+2 rules); `per-consumer`
		// fires when the request hit a Phase 3 per-consumer rule
		// AND the consumer collapsed into the __other__ bucket
		// (the collapse-bucket invariant from
		// pkg/gateway/ratelimit.go:119-124).
		policy := rateLimitScopeRoute
		// Per-consumer collapse detection (ADR-104 amendment 5,
		// issue #881 Phase 4 H1): the 429 path's bucketKey is the
		// RULE-level key (app.ID+"\x00"+rule.ID); the per-consumer
		// bucket is a sub-bucket on routeConsumerLimiter that the
		// applier already consulted above. We can't read the
		// sub-bucket key from the wire here, so we ask the limiter
		// directly: ConsumerIsTracked returns false iff the
		// consumer has collapsed to the __other__ bucket (which is
		// exactly the "per-consumer" policy-header value). Rules
		// with KeyBy ∈ {"", "none"} never enter this branch
		// (ThrottleKeyByIsPerConsumer is false) so the
		// back-compat "route" default is preserved.
		if api.ThrottleKeyByIsPerConsumer(rule.KeyBy) {
			if !h.routeConsumerLimiter.ConsumerIsTracked(bucketKey, consumerID) {
				policy = "per-consumer"
			}
		}
		h.writeRouteRateLimitHeaders(w, bucketKey, rule.RequestsPerSecond, rule.Burst, policy)
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
			"Rate limit exceeded", "slow down and retry"))
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.throttle_rejected", nil, map[string]any{
				"rule_id": rule.ID,
				"app_id":  app.ID,
				"rps":     rule.RequestsPerSecond,
				"burst":   rule.Burst,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("throttle", "blocked")
			// 429 is a non-2xx wire write — emit apply success
			// because the applier did its job. Mirrors
			// applyEdgeRuleMaintenance (handler.go:2019) and the
			// applyEdgeRuleGeo 403 paths (handler.go:2531). The
			// match result is captured separately above for the
			// §12 dashboard's "match outcome" panel.
			h.metrics.ObserveEdgeRuleApply("throttle", "success")
		}
		return true
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.throttle_matched", nil, map[string]any{
			"rule_id": rule.ID,
			"app_id":  app.ID,
			"rps":     rule.RequestsPerSecond,
			"burst":   rule.Burst,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("throttle", "match")
		h.metrics.ObserveEdgeRuleApply("throttle", "success")
	}
	return false
}

// geoFailReason returns a short audit-friendly string explaining
// why the geo lookup was deemed "failed" (err vs not-found). The
// audit pipeline copies this into the `"reason"` field of the
// edge_rule.geo_failed event so an operator can distinguish a
// corrupt DB from a missing record without re-reading the log.
func geoFailReason(lerr error, found bool) string {
	if lerr != nil {
		return "lookup_error"
	}
	if !found {
		return "no_country"
	}
	return "unknown"
}

// clientIPFromTrustedXFF extracts the single trusted X-Forwarded-For
// entry that gatewayd-public writes before handing the request off
// to gatewayd-internal. The cross-daemon handoff is via unix
// socket, so r.RemoteAddr is the unix-socket peer (always the
// gatewayd-public PID), NOT the customer IP. gatewayd-public
// already overwrote any inbound XFF with the actual customer IP
// (pkg/gateway/internal_proxy.go:286-289); we read that one entry
// and treat anything else (0 entries, 2+ entries) as a forged XFF
// and fail closed.
//
// Returns (zero, false) on parse failure — the caller's deny
// posture is enforced at the caller.
func clientIPFromTrustedXFF(r *http.Request) (net.IP, bool) {
	values := r.Header.Values("X-Forwarded-For")
	if len(values) != 1 {
		return nil, false
	}
	ipStr := strings.TrimSpace(values[0])
	if ipStr == "" {
		return nil, false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, false
	}
	return ip, true
}

// singleSlash collapses a path to the canonical slash form (no
// double slashes from `To: "/v1"` + `/api/...`). Helper for
// matchAndApplyRewrite's prefix-add and replace branches.
func singleSlash(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

// applyHeaderOp applies one EdgeRuleHeaderOp mutation to a header
// map. Action ∈ {add, set, remove}; Value is empty for "remove".
// Blacklist (Host, Content-Length, Transfer-Encoding, Connection,
// x-faas-*) is enforced at apid-Validate-time (PR 1) so this
// helper trusts the input.
func applyHeaderOp(hdr http.Header, op EdgeRuleHeaderOp) {
	switch op.Action {
	case "remove":
		hdr.Del(op.Name)
	case "set":
		if op.Value == "" {
			hdr.Del(op.Name)
			return
		}
		hdr.Set(op.Name, op.Value)
	default: // "add" and any unknown verb
		if op.Value != "" {
			hdr.Add(op.Name, op.Value)
		}
	}
}

// bearerTokenFromHeader is the per-deployment authz branch's
// local copy of pkg/auth.Middleware.bearerToken. Duplicated
// here (rather than re-exported from pkg/auth) so pkg/gateway
// keeps its zero-dep-on-pkg/auth posture (the same decoupling
// the streaming fallback helper has). Matches the same
// case-insensitive RFC 6750 §2.1 shape.
//
// Empty string in → empty string out. A bare "Bearer " with
// no token is also empty out (the caller treats it as missing).
func bearerTokenFromHeader(h string) string {
	const scheme = "bearer "
	if len(h) < len(scheme) {
		return ""
	}
	if !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}

// enforcePublicAuth (issue #477 / ADR-079) is the per-app
// public-URL auth gate. Returns true when the request is
// authorised to proceed (either the routed app has
// mode='open' OR the caller presented valid credentials
// for the active mode); returns false after writing the
// deny response and the metrics observation. The boolean
// keeps the call-site in ServeHTTP at one line (mirrors
// enforceRequireAuthn's shape).
//
// Modes:
//
//	open    pass-through, no audit (preserves pre-#477
//	        behaviour so every existing app stays
//	        public-by-default; the apid PATCH layer is
//	        the plan gate — Free-plan apps never reach
//	        here with mode='bearer' or 'basic').
//	bearer  re-uses the require_authn chain
//	        (h.requireAuthnAuthn.AuthenticateKey); the
//	        bearer resolves to a different account than
//	        App.AccountID receive 403 insufficient_scope.
//	basic   verifies Basic <base64(user:pass)> against
//	        the unsealed credential cached in
//	        PublicAuthCache (60s TTL + per-key invalidation
//	        on db.NotifyKeyChanged); on miss the cache
//	        calls PublicAuthUnsealer.UnsealBasicAuth on the
//	        secretbox-sealed BasicSealed blob.
//
// Deny paths:
//
//	401 unauthorized     no Authorization header for the
//	                     active mode, or the credential is
//	                     malformed / unknown / revoked.
//	                     Carries the WWW-Authenticate
//	                     response header per RFC 6750 §3
//	                     (bearer) / RFC 7617 §4 (basic) so
//	                     the client knows which credential
//	                     to present next.
//	403 forbidden        mode='bearer', key resolves to a
//	                     valid principal but on the wrong
//	                     account (mirrors
//	                     enforceRequireAuthn's 403 path;
//	                     never fires for mode='basic' since
//	                     basic creds are app-scoped, not
//	                     account-scoped).
//
// Audit emission:
//
//	instances.public_auth_missing   401 path, no/garbled
//	                                header on a gated app.
//	instances.public_auth_invalid   401 path, credential
//	                                rejected (mode='bearer'
//	                                unknown/expired/revoked;
//	                                mode='basic' wrong
//	                                password).
//	instances.public_auth_scope     403 path, mode='bearer'
//	                                cross-account key.
//
// All three audit rows carry app_id + slug + (mode =
// 'bearer'|'basic') + (mode='bearer' → key_id when known).
// The subject pointer is the account ID for scope, nil for
// missing/invalid (no principal to stamp). Best-effort — a
// failed emit never blocks the deny response (matches
// emitAuthnAudit's contract).
func (h *Handler) enforcePublicAuth(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	// Open (or unknown) → pass-through. Unknown / empty Mode
	// is treated as 'open' so the pre-#477 customer behaviour
	// is preserved (a fakeBackend unit test that doesn't
	// populate PublicAuthMode gets the same path as a real
	// open-mode app). No audit row is emitted — open traffic
	// is the default; only denials are interesting.
	mode := app.PublicAuth.Mode
	if mode == "" || mode == publicAuthModeOpen {
		return true
	}
	switch mode {
	case publicAuthModeBearer:
		return h.enforcePublicAuthBearer(w, r, rec, app)
	case publicAuthModeBasic:
		return h.enforcePublicAuthBasic(w, r, rec, app)
	default:
		// Fail-open on unknown mode (ADR-079 §Consequences).
		// Distinct from the two adjacent nil-check branches
		// (requireAuthnAuthn == nil, publicAuthUnsealer == nil)
		// which fail-closed (500) — those are deploy-failure
		// signals ("the daemon isn't wired"), while an unknown
		// mode is a data-event signal ("a row landed with a
		// value the schema doesn't recognize"). The SQL CHECK
		// constraint apps_public_auth_mode_chk is the canonical
		// data-integrity backstop; a row that bypassed it is a
		// code-path bug we want to surface, not a deploy failure
		// we want to amplify. The per-mode warn-then-dedup keeps
		// the diagnostic surface bounded (one log line per
		// distinct mode value across the process lifetime).
		h.warnUnknownPublicAuthMode(mode)
		return true
	}
}

// enforcePublicAuthBearer is the bearer-mode branch of
// enforcePublicAuth. Mirrors the (1)/(2)/(3) deny structure
// of enforceRequireAuthn so an operator reading the two side
// by side sees parallel paths. nil-safe: a nil
// h.requireAuthnAuthn (the pre-#560 default; tests + dev
// boxes that don't wire the chain) → 500 rather than
// pass-through, because a customer who flipped mode='bearer'
// expects the gate to fire and a silent pass-through would
// be a security regression.
func (h *Handler) enforcePublicAuthBearer(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	if h.requireAuthnAuthn == nil {
		rec.status = http.StatusInternalServerError
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Auth chain not wired",
			"this app requires bearer auth but the auth chain is not configured"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (1) Bearer extraction — fail-fast at 401 if no token.
	tok := bearerTokenFromHeader(r.Header.Get("Authorization"))
	if tok == "" || !api.ValidAPIKeyFormat(tok) {
		h.emitAuthnAudit(r, app, nil, "instances.public_auth_missing", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"mode":   publicAuthModeBearer,
			"reason": "missing_or_malformed_bearer",
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "this app requires an API key; present it as `Authorization: Bearer <token>`").
			WithHeader("WWW-Authenticate", `Bearer realm="apps"`))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (2) Verify the key via the shared require_authn chain
	// (same api.HashAPIKey helper; defense in depth — keys
	// never reach the store unhashed).
	acct, key, err := h.requireAuthnAuthn.AuthenticateKey(r.Context(), api.HashAPIKey(tok))
	if err != nil {
		reason := authnReasonInvalidBearer
		if errors.Is(err, ErrAPIKeyExpired) {
			reason = authnReasonExpired
		} else if errors.Is(err, ErrAPIKeyRevoked) {
			reason = authnReasonRevoked
		}
		h.emitAuthnAudit(r, app, nil, "instances.public_auth_invalid", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"mode":   publicAuthModeBearer,
			"reason": reason,
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "the presented API key is not valid for this app").
			WithHeader("WWW-Authenticate", `Bearer realm="apps"`))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (3) Cross-account check — a valid key on the wrong
	// account is 403, not 401. Distinct from
	// enforceRequireAuthn so the SDK can pivot on the
	// response code instead of inspecting the body, but the
	// audit kind differs (public_auth_scope vs authn_scope)
	// so the per-gate dashboards stay separate.
	if acct.ID != app.AccountID {
		h.emitAuthnAudit(r, app, &acct.ID, "instances.public_auth_scope", map[string]any{
			"app_id":            app.ID,
			"slug":              r.Host,
			"mode":              publicAuthModeBearer,
			"caller_account_id": acct.ID,
			"app_account_id":    app.AccountID,
			"key_id":            key.ID,
		})
		rec.status = http.StatusForbidden
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Insufficient scope", "this API key does not belong to the account that owns the app"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	return true
}

// enforcePublicAuthBasic is the basic-mode branch of
// enforcePublicAuth. Reads the Basic <base64(user:pass)>
// header, looks up the unsealed credential in
// PublicAuthCache (which calls PublicAuthUnsealer on miss
// and caches the result for 60s + per-key invalidation),
// and constant-time-compares the presented password
// against the unsealed one. Same constant-time posture as
// every other credential compare in the platform
// (api.HashAPIKey / cmd/apid webhook signing).
func (h *Handler) enforcePublicAuthBasic(w http.ResponseWriter, r *http.Request, rec *statusRecorder, app App) bool {
	if h.publicAuthCache == nil || h.publicAuthUnsealer == nil {
		rec.status = http.StatusInternalServerError
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Auth chain not wired",
			"this app requires basic auth but the credential cache is not configured"))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (1) Basic extraction — fail-fast at 401 if no header.
	user, pass, ok := basicCredsFromHeader(r.Header.Get("Authorization"))
	if !ok {
		h.emitAuthnAudit(r, app, nil, "instances.public_auth_missing", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"mode":   publicAuthModeBasic,
			"reason": "missing_or_malformed_basic",
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "this app requires basic auth; present it as `Authorization: Basic <base64(user:pass)>`").
			WithHeader("WWW-Authenticate", `Basic realm="apps"`))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (2) Look up the unsealed credential via the cache (60s
	// TTL + per-key invalidation). A cache miss invokes
	// PublicAuthUnsealer.UnsealBasicAuth on the
	// secretbox-sealed BasicSealed blob stored on the apps
	// row.
	expectedUser, expectedPass, ok := h.publicAuthCache.Get(app.ID, app.PublicAuth.BasicSealed, func() (string, string, bool) {
		u, p, err := h.publicAuthUnsealer.UnsealBasicAuth(r.Context(), app.PublicAuth.BasicSealed)
		if err != nil {
			return "", "", false
		}
		return u, p, true
	})
	if !ok {
		// Unseal failure is treated as a credential mismatch
		// (401) so a brute-forcer can't tell the difference
		// between "no creds configured" and "wrong creds".
		h.emitAuthnAudit(r, app, nil, "instances.public_auth_invalid", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"mode":   publicAuthModeBasic,
			"reason": "credential_unavailable",
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "the presented credentials are not valid for this app").
			WithHeader("WWW-Authenticate", `Basic realm="apps"`))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	// (3) Constant-time compare. Both sides are base64-
	// decoded; we re-encode back to bytes for the compare so
	// the function stays allocation-light. Length-mismatch
	// short-circuits to 0 before the compare so an attacker
	// who knows the password length can't probe via timing.
	if subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) != 1 ||
		subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPass)) != 1 {
		h.emitAuthnAudit(r, app, nil, "instances.public_auth_invalid", map[string]any{
			"app_id": app.ID,
			"slug":   r.Host,
			"mode":   publicAuthModeBasic,
			"reason": "wrong_credentials",
		})
		rec.status = http.StatusUnauthorized
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "the presented credentials are not valid for this app").
			WithHeader("WWW-Authenticate", `Basic realm="apps"`))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return false
	}
	return true
}

// basicCredsFromHeader parses an `Authorization: Basic
// <base64(user:pass)>` header per RFC 7617 §2. Returns
// ok=false on any malformed input (no scheme, non-base64,
// missing colon, empty username OR password). The username
// is everything before the FIRST colon; passwords may
// contain colons (RFC 7617 §2 explicitly permits them), so
// strings.SplitN(2) is the right primitive. Empty string
// out → empty string out (mirrors bearerTokenFromHeader).
func basicCredsFromHeader(h string) (username, password string, ok bool) {
	const scheme = "basic "
	if len(h) < len(scheme) {
		return "", "", false
	}
	if !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", "", false
	}
	raw := h[len(scheme):]
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	if user == "" || pass == "" {
		return "", "", false
	}
	return user, pass, true
}

// unknownPublicAuthModeWarned is the process-wide trip flag
// for warnUnknownPublicAuthMode (issue #477). An unrecognised
// mode should be impossible thanks to the CHECK constraint;
// the warning is genuinely a code-path bug (or a rebase
// race that left an old row behind) and one log line per
// mode is enough to surface it.
var unknownPublicAuthModeWarned sync.Map // map[string]struct{}

// warnUnknownPublicAuthMode logs a warning the first time
// enforcePublicAuth sees an unrecognised PublicAuth.Mode.
// Subsequent occurrences are silent (per-mode dedup, not
// per-process, so two distinct legacy modes each surface
// once). The atomic map is process-scoped, not per-Handler,
// because the warning is genuinely a code-path bug.
func (h *Handler) warnUnknownPublicAuthMode(mode string) {
	if _, seen := unknownPublicAuthModeWarned.LoadOrStore(mode, struct{}{}); seen {
		return
	}
	if h.log != nil {
		h.log.Warn("gateway: unknown PublicAuth.Mode; passing through as open",
			"mode", mode, "note", "apps_public_auth_mode_chk should reject this; check migration 00151")
	}
}

// ErrAPIKeyExpired / ErrAPIKeyRevoked are local sentinels for
// the AuthenticateKey error contract (issue #560). The real
// errors live in pkg/state (state.ErrAPIKeyExpired /
// state.ErrAPIKeyRevoked). pkg/gateway can't import pkg/state
// without dragging in the whole sqlc-generated surface, so we
// mirror the bare error type here and the production adapter in
// cmd/gatewayd-internal adapts the real sentinels into these via
// a thin wrapper (see cmd/gatewayd-internal/require_authn_adapter.go).
//
// ServeHTTP compares with errors.Is, which is type-aware, so the
// string values below are cosmetic — operator-facing audit rows
// never log these strings, and the gate doesn't string-compare.
// The values are kept verbatim from state.Err*.Error() so an
// operator pasting an error message from a log into
// errors.Is(errAPIKey) matches by mistake rather than by design.
var (
	ErrAPIKeyExpired = errAuthnSentinel("api_key_expired")
	ErrAPIKeyRevoked = errAuthnSentinel("api_key_revoked")
)

// errAuthnSentinel is a named-error type so errors.Is works
// against the constant strings above without forcing pkg/gateway
// to know about state.Err*. The string values are the same codes
// pkg/state emits; the production adapter in cmd/gatewayd-internal/
// translates via errors.Is on the incoming error.
type errAuthnSentinel string

func (e errAuthnSentinel) Error() string { return string(e) }

// streamingFallbackLog emits a once-per-process deprecation line for
// the buffered-fallback path (issue #471 PR-A AC #4). A misconfigured
// Free-tier app could land an SSE-emitting response on the buffered
// path; the log tells operators the flag-off branch fired so they
// can either flip the per-app flag to false (the correct fix) or
// upgrade the customer. Keyed on (appID, contentType) so two
// distinct apps emitting SSE each get their own line; the log
// dedup uses sync.Map so the hot path is allocation-free after the
// first observation per key.
func (h *Handler) streamingFallbackLog(appID, contentType string) {
	if h.log == nil {
		return
	}
	key := appID + "\x00" + contentType
	if _, seen := h.streamingWarned.Load(key); seen {
		return
	}
	h.streamingWarned.Store(key, struct{}{})
	h.log.Warn("gateway: streaming fallback (per-app streaming_enabled=true on a plan that did not unlock streaming; response buffered end-to-end)",
		"app", appID, "content_type", contentType)
}

// logBodyCapWarnOnce emits a structured slog.Warn the first time
// (per process) an app trips a body-warn bucket (near_threshold or
// exceeded). The Prometheus counter is incremented unconditionally by
// capWriter.onWarn; this helper is the once-per-process log dedup so
// a runaway app doesn't flood the log stream. Issue #995 Phase 4 /
// ADR-121.
//
// bucket is the closed-set label matching the metric enum
// {near_threshold, exceeded}. bytes and cap are the body size at the
// threshold crossing and the per-plan cap respectively — surfaced as
// numeric fields so the operator can sort/cut by appID in the log
// pipeline.
func (h *Handler) logBodyCapWarnOnce(appID, bucket string, bytes, cap int64) {
	if h == nil || appID == "" || bucket == "" {
		return
	}
	key := appID + "\x00" + bucket
	if _, loaded := h.bodyCapWarned.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if h.log == nil {
		return
	}
	h.log.Warn("gateway: response body approaching or exceeding per-plan cap",
		"app", appID,
		"bucket", bucket,
		"bytes", bytes,
		"cap", cap,
	)
}

// isAcceptJSON reports whether the request's Accept header opts the
// request into the buffered path regardless of the per-app
// streaming_enabled flag (spec §4.1, ADR-047). The check is case-
// insensitive on the prefix and tolerates parameters like
// "application/json; charset=utf-8". Anything else (no header,
// wildcard "*/*", "text/event-stream", etc.) leaves the per-app
// flag as the source of truth. Returns false for empty string — a
// client that sets Accept: "" is treated the same as not setting
// the header at all.
func isAcceptJSON(accept string) bool {
	if accept == "" {
		return false
	}
	// Header may carry multiple comma-separated values; the
	// per-request opt-out fires when ANY of them is
	// application/json. The plan-of-record is "if you set
	// application/json at all, you're opting in to buffered".
	for _, raw := range strings.Split(accept, ",") {
		mt := strings.TrimSpace(strings.ToLower(raw))
		// Strip parameters (e.g. "; charset=utf-8").
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			mt = mt[:i]
		}
		mt = strings.TrimSpace(mt)
		if mt == "application/json" {
			return true
		}
	}
	return false
}

// setupStreamingWriter arms the per-request Flusher path. The
// returned writer chains: MaxBytesWriter → statusRecorder →
// original. Cap-exceeded writes emit a 413 problem+json via the
// supplied onCap callback and disable further writes. The recorder
// receives a per-flush onFlush closure that:
//   - computes delta = cumulativeBytes - lastReported
//   - attributes the delta to the egress sink
//     (target.InstanceID, current minute)
//   - increments the per-(app, plan) response bytes counter
//   - tracks lastReported in a closure-local int64 (no field on
//     the recorder — the recorder stays a value type that can
//     still be embedded inside statusRecorder or copied
//     accidentally without losing accounting state)
//
// The flush window (256 KiB / 200 ms) comes from
// api.StreamingFlushBytesDefault / api.StreamingFlushIntervalDefault
// — keeping them in pkg/api/limits.go means the operator can
// later add a per-cluster override without touching this file.
// writeTimeout is the per-flush deadline installed on the first
// flush via http.ResponseController.SetWriteDeadline; the
// subsequent flushes re-install the deadline relative to "now" so
// the total wall time the connection can stay open is
// flushInterval × N (with N bounded by the upstream duration).
//
// Returns the OUTER writer (the cap-wrapped recorder). The
// Handler then calls proxy.ServeHTTP(w, r) with this writer;
// every Write hits the cap check, the recorder's maybeFlush,
// and the recorder's flush (which calls onFlush).
// setupStreamingWriter installs the per-flush metering + cap-wrap
// pipeline on w. The cap parameter is the ADR-102-resolved response
// body byte cap (plan cap by default; endpoint-rule
// MaxBodyBytesStreaming if a kind=limit edge rule matched the
// request — see decideStreaming at handler.go:~2295). Pre-ADR-102
// the cap was always plan-derived (app.Plan.MaxResponseBodyBytes
// hardcoded below), so this signature change is the D4 wire-up
// point.
func (h *Handler) setupStreamingWriter(w http.ResponseWriter, rec *statusRecorder, app App, target Target, cap int64, writeTimeout time.Duration) http.ResponseWriter {
	var lastReported int64
	onFlush := func(cumulative int64) {
		delta := cumulative - lastReported
		lastReported = cumulative
		if delta <= 0 {
			return
		}
		if h.egressSink != nil && target.InstanceID != "" {
			h.egressSink.RecordResponseBytes(target.InstanceID, delta)
		}
		if h.metrics != nil && app.ID != "" {
			h.metrics.ObserveResponseBytes(app.ID, string(app.Plan), delta)
		}
		if h.metrics != nil {
			h.metrics.ObserveStreamFlush(app.ID, string(app.Plan))
		}
	}

	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		// The wrapped writer isn't an http.Flusher. The buffered
		// path stays; we still install the onFlush hook so the
		// recorder tracks Bytes for the once-per-response
		// recordEgress call. (This branch is reachable in unit
		// tests where httptest.NewRecorder doesn't implement
		// Flusher; production uses http.ResponseWriter from
		// net/http.Server which always does.)
		rec.installFlushHook(nil, onFlush, 0, 0, 0)
		return w
	}
	rec.installFlushHook(flusher, onFlush, api.StreamingFlushBytesDefault, api.StreamingFlushIntervalDefault, writeTimeout)

	// Wrap with MaxBytesWriter so a streaming app that exceeds
	// its plan's response body cap (Free 25 MB, Hobby 100 MB,
	// etc.) gets a 413 problem+json instead of a stdlib-default
	// 502. The onCap callback fires once per request — the
	// MaxBytesWriter goroutine detects the cap, signals the
	// response goroutine via the closed channel, the response
	// goroutine writes the 413 problem+json and disables
	// further writes via the capWriter.disabled flag.
	cw := &capWriter{
		ResponseWriter: rec,
		cap:            cap,
		onCap: func() {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeStreamingNotAvailable,
				"streaming response exceeded plan cap",
				fmt.Sprintf("app %s on plan %s is capped at %d bytes per response", app.ID, app.Plan, cap)))
		},
		disabled: &atomic.Bool{},
	}
	// Issue #995 Phase 4 / ADR-121: emit the response-body warn
	// counter on threshold crossings (same shape as the buffered
	// path; the streaming surface uses the same metric family).
	if h.metrics != nil {
		cw.onWarn = func(bucket string) {
			h.metrics.ObserveResponseBodyWarn(app.ID, bucket == "near_threshold", bucket == "exceeded")
			h.logBodyCapWarnOnce(app.ID, bucket, cw.written, cap)
		}
	}
	return cw
}

// setupBufferedCapWriter wraps w with a capWriter that enforces the
// per-plan MaxResponseBodyBytes cap on the BUFFERED reverse-proxy
// path (issue #995 Phase 2 / ADR-121). Distinct from
// setupStreamingWriter: this helper doesn't install the
// per-flush metering hooks (the buffered path doesn't flush
// incrementally) and emits the response_too_large problem code
// instead of streaming_not_available so the buffered-cap surface
// has its own stable error contract.
//
// The companion upstream guard (io.LimitReader wrap on the
// upstream's response body) is installed by defaultProxy's
// ModifyResponse hook. The cap is threaded through h.proxyFor
// (signature changed in #996 review-fix cycle to take cap int64)
// so the upstream LimitReader fires at cap+1 bytes. Issue #995
// Phase 2 / ADR-121.
func (h *Handler) setupBufferedCapWriter(w http.ResponseWriter, app App, cap int64) http.ResponseWriter {
	if cap <= 0 {
		return w
	}
	cw := &capWriter{
		ResponseWriter: w,
		cap:            cap,
		onCap: func() {
			api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeResponseTooLarge,
				"response exceeded plan cap",
				fmt.Sprintf("app %s on plan %s is capped at %d bytes per response", app.ID, app.Plan, cap)))
		},
		disabled: &atomic.Bool{},
	}
	// Issue #995 Phase 4 / ADR-121: emit the response-body warn
	// counter on threshold crossings. The bucket label matches
	// the metrics contract (near_threshold|exceeded).
	if h.metrics != nil {
		cw.onWarn = func(bucket string) {
			h.metrics.ObserveResponseBodyWarn(app.ID, bucket == "near_threshold", bucket == "exceeded")
			h.logBodyCapWarnOnce(app.ID, bucket, cw.written, cap)
		}
	}
	return cw
}

// capWriter is a thin http.ResponseWriter wrapper that enforces
// a per-response body byte cap. On cap-exceeded Write returns an
// error and fires onCap (which writes a problem+json to the
// ORIGINAL writer before this wrapper was installed, so the
// problem lands on the wire even though the wrapped Write is
// blocked). After onCap fires, the disabled flag prevents
// subsequent Writes from consuming bytes.
//
// This is a separate type from http.MaxBytesWriter because the
// stdlib one writes a 502 directly to the underlying writer with
// no opportunity to inject our 413 problem+json shape.
type capWriter struct {
	http.ResponseWriter
	cap      int64
	written  int64
	onCap    func()
	disabled *atomic.Bool
	// onWarn fires at 80% / 95% of cap and on exceeded (issue
	// #995 Phase 4 / ADR-121). bucket is the closed-set label
	// the caller passes to the metrics counter: ∈
	// {"near_threshold", "exceeded"}. CAS-guarded to fire
	// exactly once per threshold per request.
	near80   atomic.Bool
	near95   atomic.Bool
	exceeded atomic.Bool
	onWarn   func(bucket string)
}

func (c *capWriter) Write(b []byte) (int, error) {
	if c.disabled.Load() {
		return 0, http.ErrHandlerTimeout
	}
	if c.written+int64(len(b)) > c.cap {
		// Fire once; idempotent under concurrent writes thanks
		// to disabled CAS.
		if c.disabled.CompareAndSwap(false, true) && c.onCap != nil {
			c.onCap()
		}
		if c.exceeded.CompareAndSwap(false, true) && c.onWarn != nil {
			c.onWarn("exceeded")
		}
		return 0, http.ErrHandlerTimeout
	}
	// Pre-Write warn-on-approach hook (issue #995 Phase 4 /
	// ADR-121): fire once at 95% of cap, once at 80% (95%
	// takes priority if the same Write crosses both — the
	// higher threshold is the more urgent signal). The
	// thresholds are computed against c.written (bytes already
	// written) + len(b) (about-to-be-written) so the boundary
	// check matches the over-cap check above. The two
	// thresholds are independent CAS guards — both fire when
	// the same Write crosses both boundaries, and neither
	// short-circuits the other.
	if c.onWarn != nil && c.cap > 0 {
		wouldWrite := c.written + int64(len(b))
		if wouldWrite >= c.cap*95/100 {
			if c.near95.CompareAndSwap(false, true) {
				c.onWarn("near_threshold")
			}
		}
		if wouldWrite >= c.cap*80/100 {
			if c.near80.CompareAndSwap(false, true) {
				c.onWarn("near_threshold")
			}
		}
	}
	n, err := c.ResponseWriter.Write(b)
	if n > 0 {
		c.written += int64(n)
	}
	return n, err
}

// WriteHeader is the buffered-cap path's primary hook. By the time
// the upstream proxy calls Write(body), it has already issued
// WriteHeader(200), and api.WriteProblem's WriteHeader(413) silently
// no-ops on httptest.ResponseRecorder (and writes a warning
// "superfluous header" to logs in production). The fix: install a
// pre-header guard via the wrapper's WriteHeader — if any prior
// Write has crossed the cap, refuse with 413 BEFORE the upstream
// headers reach the wire.
//
// The first call to WriteHeader here is always a pass-through
// (we haven't seen any body bytes yet); the cap check on this
// surface is purely a defence-in-depth net for an edge case where
// the upstream's headers themselves exceed the cap (rare;
// header-size cap is enforced at the http.Server layer).
func (c *capWriter) WriteHeader(statusCode int) {
	if c.disabled.Load() {
		// The cap already fired; refuse to acknowledge further
		// status codes so the problem+json onCap result is the
		// only thing on the wire.
		return
	}
	c.ResponseWriter.WriteHeader(statusCode)
}

// Flush is forwarded so a streaming upstream calling Flush
// directly on the cap-wrapped writer still triggers the
// recorder's flush logic. The cap check happens in Write, not
// Flush, so a Flush that hasn't been preceded by a new Write is
// a no-op for the cap.
func (c *capWriter) Flush() {
	if c.disabled.Load() {
		return
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// WithEgressSink installs the per-instance HTTP response byte ring
// buffer (pkg/gateway/egresssink). ADR-046 (per-instance egress
// metering, telemetry only) wires this once at gatewayd-internal boot from
// cmd/gatewayd-internal/main.go. nil-safe: passing nil clears the sink;
// ServeHTTP short-circuits the record path the moment it sees
// h.egressSink == nil, so unit tests don't have to install one.
//
// Mutates the receiver in place; the returned *Handler is the same
// pointer, provided for fluent chaining. Discarding the return
// value (statement-form `h.WithEgressSink(...)`) is correct.
func (h *Handler) WithEgressSink(sink *egresssink.EgressSink) *Handler {
	h.egressSink = sink
	return h
}

// WithInFlightTracker installs the per-request WaitGroup-backed
// drain tracker (pkg/gateway/drain) the gateway's graceful
// shutdown uses to bound the listener exit by known in-flight
// request goroutines, not a hard wall-clock deadline (issue
// #587 / PR-A). nil-safe: passing nil disables drain tracking,
// matching the pre-PR-A behaviour; the production path installs
// a single tracker shared with the control mux + InternalReverseProxy
// + TraceHandler so the drain WaitGroup covers every ServeHTTP
// surface in the daemon.
//
// Must be called BEFORE the listener starts accepting traffic.
// The daemons call it once in main() after the tracker is
// constructed and before servers.Serve is invoked.
//
// Mutates the receiver in place; the returned *Handler is the same
// pointer, provided for fluent chaining. Discarding the return
// value (statement-form `h.WithInFlightTracker(...)`) is correct.
func (h *Handler) WithInFlightTracker(tracker *drain.Tracker) *Handler {
	h.drain = tracker
	return h
}

// serveHTTPWithDrain is the per-request drain Begin/Done pair.
// nil-safe: returns a no-op closure when h.drain is nil so the
// call site stays a single line. The deferred closure fires on
// every return path of ServeHTTP, including the early-out problem
// writes, so a request that 4xx/5xx immediately still drains
// cleanly. Label is fixed at "http" for the per-request path; the
// raw-stream Upgrade hijacker (pkg/gateway/forwardproxy.go:830)
// uses Begin("upgrade") directly so the conn-scoped pump
// goroutine can hold its own Done closure past ServeHTTP return.
func (h *Handler) serveHTTPWithDrain() func() {
	if h.drain == nil {
		return func() {}
	}
	return h.drain.Begin("http")
}

// SetWakeGateHook installs a callback that wakes the queue-depth gauge each
// time WakeGate mutates an entry, and hands the wake-queue histogram to the
// gate so Wait can observe per-caller wait duration. Called by main once the
// metrics bundle exists.
func (h *Handler) SetWakeGateHook() {
	h.gate.onChange = func(appID, accountID string, depth int) {
		if h.metrics != nil {
			h.metrics.SetQueueDepth(appID, accountID, depth)
		}
	}
	h.gate.SetMetrics(h.metrics)
}

// SetTopNSample installs the per-request bump callback for the
// gateway-side top-N sampler (issue #300). Called by main once
// the sampler is constructed. The Handler stores the callback
// and invokes it from observe() on every non-sentinel app id;
// nil = no-op (unit-test seam).
//
// The callback signature is func(appID string) so pkg/gateway
// stays free of any dependency on pkg/wire or the local
// cmd/gatewayd-internal/topAccountSet — same decoupling pattern as
// SetWakeGateHook above.
func (h *Handler) SetTopNSample(sample func(appID string)) {
	h.topNSample = sample
}

// WithRequestTelemetryRecorder (ADR-127) wires the in-process
// request-telemetry recorder (pkg/gateway/request_telemetry.go)
// into the Handler. Call once at daemon boot from
// cmd/gatewayd-internal/main.go alongside SetTopNSample. nil
// disables the data plane (every observe call no-ops the enqueue
// step — the same posture as the unit-test seam).
func (h *Handler) WithRequestTelemetryRecorder(r *requestTelemetryRecorder) {
	h.requestTelemetry = r
}

// Metrics exposes the Prometheus bundle (used by the control listener to mount
// /metrics). May be nil if NewHandler was used and nothing initialized one.
func (h *Handler) Metrics() *Metrics { return h.metrics }

// Limiter exposes the per-app rate limiter so callers (SIGHUP handler,
// admin endpoints) can forget buckets. Mostly an M5 hook: when apid starts
// pushing plan changes via Postgres + LISTEN, the gateway's reload path
// calls ForgetAll() on this limiter.
func (h *Handler) Limiter() *Limiter { return h.limiter }

// AccountLimiter exposes the per-account rate limiter (ADR-040 / issue #292).
// Same SIGHUP contract as Limiter — the gateway's reload path calls
// ForgetAll() on both limiters. Test seam: WithAccountLimiter installs a
// noop for load tests that need to bypass the account bucket.
func (h *Handler) AccountLimiter() *Limiter { return h.accountLimiter }

// writeWebSocketNotAllowed short-circuits the upgrade path with a
// deterministic 501 + x-faas-error-reason: websocket_not_on_plan
// when the inbound request is an upgrade but the platform can't
// service it (issue #707). Two trigger paths:
//   - app.WebSocketEnabled is false (Hobby+ customer who PATCHed
//     the flag off).
//   - h.rawByNode is nil (raw forwarder not wired — test fixtures
//     OR the FAAS_GATEWAY_RAW_STREAM_ENABLED=false follow-up).
//
// The previous fall-through to proxyByNode stripped Connection +
// Upgrade as hop-by-hop (RFC 7230 §6.1) and returned 502 from the
// upstream — a confusing failure shape that retried the WS
// handshake in an infinite loop.
//
// The response is a stable RFC 7807 problem document with
// code=plan_websocket_not_allowed; the x-faas-error-reason header
// carries the same code for the WS-client-retry path (clients
// read headers, not the problem body). The 501 status is the
// canonical "feature not enabled on this deployment" code (the
// WS protocol allows clients to back off; 502 doesn't).
func (h *Handler) writeWebSocketNotAllowed(w http.ResponseWriter, appID string, forwarderMissing bool) {
	w.Header().Set("x-faas-error-reason", "websocket_not_on_plan")
	w.Header().Set("Content-Type", "application/problem+json")
	detail := "This app has WebSocket / Upgrade traffic disabled."
	if forwarderMissing {
		detail = "This deployment has the WebSocket / Upgrade-traffic raw-bytes bridge disabled."
	}
	body := fmt.Sprintf(`{"type":"`+docsTypeBase+`/plan_websocket_not_allowed","title":"WebSocket not enabled on this app or plan","status":501,"detail":%q,"code":"plan_websocket_not_allowed","app_id":%q}`, detail, appID)
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = fmt.Fprint(w, body)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The edge-rule budget is stamped after app resolution, so its timer
	// cancellation must be deferred against the final request context rather
	// than inside stampRequestBudget itself. This keeps cold-start and proxy
	// work alive for the allotted budget while still releasing the timer on
	// every return path.
	defer func(ctx context.Context) { cancelStampedRequestBudget(ctx) }(r.Context())

	// Drain tracker (issue #587 / PR-A): the returned closure fires
	// on every return path below, including the early-out problem
	// writes. nil-safe via serveHTTPWithDrain — unit tests that
	// don't wire WithInFlightTracker pay zero overhead. Place BEFORE
	// statusRecorder creation so even panic-recovered paths keep
	// the drain balanced.
	defer h.serveHTTPWithDrain()()

	// Status-class capture (used for metrics + slog). Doesn't buffer the body
	// or alter the headers — strictly observability.
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = rec

	// Stamp the request-received timestamp onto the context so every exit
	// path can measure the SAME elapsed interval (was previously always
	// falling back to time.Now() because WithStartTime was dead code —
	// issue #273 / ADR-042 fixed this so gateway_request_duration_seconds
	// has a real elapsed to record and the slog latency_ms field stops
	// being effectively zero).
	start := time.Now()
	r = r.WithContext(WithStartTime(r.Context(), start)) //nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.

	// Request ID is generated once per request and set on the response BEFORE
	// any error path so even 4xx responses are correlatable. Inbound
	// x-faas-request-id overrides (lets curl/clients supply their own trace).
	rid := r.Header.Get("x-faas-request-id")
	if rid == "" {
		rid = newRequestID()
	}
	w.Header().Set("x-faas-request-id", rid)
	r = r.WithContext(WithRequestID(r.Context(), rid)) //nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.

	host := hostname(r.Host)

	// Issue #463 / ADR-069 / ADR-071 / PR-C §5: split the
	// routing-key hostname into (appHost, sidecarName) via
	// the `--` separator. The appsSuffix strips first so
	// the `--` search bounds itself to the bare
	// app+selector; appHost is the segment back-left of
	// `--` (re-suffixed), sidecarName is the segment
	// back-right. A sidecarName="" branch is the legacy
	// single-app routing — main workload's port. The split
	// runs BEFORE the appsSuffix check so the suffix gate
	// sees the appHost (the inner host), not the full
	// selector hostname.
	appHost, sidecarName := SplitHostSelectorWithSuffix(host, h.appsSuffix)
	// Host allowlist suffix check (spec §4.1 wildcard contract). Closes the
	// door on stale DNS records that land on the edge post-TLS by rejecting
	// anything not matching the configured suffix before the cache is touched.
	// Set via NewHandlerWithSuffix or WithAppsSuffix; empty suffix disables
	// the check (the Backend.Lookup table is still authoritative).
	if h.appsSuffix != "" && !strings.HasSuffix(appHost, h.appsSuffix) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
			api.CodeNotFound, "No such app",
			fmt.Sprintf("host %q does not match the configured apps suffix", appHost)))
		h.observe(r, rec.status, "", "", false, Target{})
		return
	}

	// Issue #561 / ADR-089 PR 3 — consult the per-host
	// edge-rule matcher BEFORE Backend.Lookup. On a
	// `kind=route` hit the matcher overwrites `app` with
	// the target App and we skip the Lookup entirely
	// (the substituted App is authoritative; re-running
	// Lookup on the inbound hostname would waste a cache
	// miss). Downstream RequireAuthn / PublicAuth / wake
	// gate / proxy all see the *target* app's context,
	// not the inbound host's. nil-safe: h.edgeRules nil
	// (default) returns false and we fall through to the
	// legacy host→app lookup.
	var (
		app       App
		lookedApp App
		ok        bool
	)
	if h.matchAndSubstituteRoute(r, &app) {
		goto haveApp
	}
	//nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.
	lookedApp, ok = h.backend.Lookup(r.Context(), appHost)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No such app", fmt.Sprintf("no app is routed to %q", appHost)))
		h.observe(r, rec.status, "", "", false, Target{})
		return
	}
	app = lookedApp
haveApp:
	// ADR-127: stamp account_id + app_id onto the request context
	// so the recorder can build a row at Handler.observe without
	// observe's signature changing. nil-safe on accountID (the
	// gateway can't resolve an account for an app that was
	// substituted by an edge rule without an owner — those rows
	// are pre-picker and observe drops them).
	if h.requestTelemetry != nil {
		var accountUUID, appUUID uuid.UUID
		if app.AccountID != "" {
			if u, err := uuid.Parse(app.AccountID); err == nil {
				accountUUID = u
			} else if h.log != nil {
				// Malformed account UUID is a backend bug — the
				// recorder drops the row silently when the parse
				// fails, so the only observable symptom is an
				// empty telemetry table for the affected app.
				// Warn so operators see the data plane is
				// running but missing rows for this app.
				h.log.Warn("request_telemetry: app.AccountID is not a valid UUID; row will be dropped",
					"app_id", app.ID, "host", r.Host, "account_id_raw", app.AccountID)
			}
		}
		if app.ID != "" {
			if u, err := uuid.Parse(app.ID); err == nil {
				appUUID = u
			} else if h.log != nil {
				h.log.Warn("request_telemetry: app.ID is not a valid UUID; row will be dropped",
					"host", r.Host, "app_id_raw", app.ID)
			}
		}
		r = withAppAndAccount(r, accountUUID, appUUID)
	}
	// ADR-093: derive the per-request route label and stash it
	// on the request context so Handler.observe can read it on
	// the single exit funnel. The label is method + raw path
	// (pre-rewrite, ADR-093 D6) — the route identity is the
	// customer-facing endpoint, so a kind=rewrite edge rule that
	// rewrites /v1/foo → /v2/foo reports the inbound route, not
	// the rewritten one. The routeLabelSet bounds the per-app
	// distinct-route count to 50 + the __route_other__ overflow
	// bucket. The label is empty when the app is not opted in
	// (routeSetFor returns nil) — Handler.observe short-circuits
	// the per-route emission on "".
	routeLabel := ""
	if set := h.routeSetFor(app.ID, app.RouteMetricsEnabled && h.routeMetricsEnabled); set != nil {
		preLabel := r.Method + " " + r.URL.Path
		routeLabel = set.admit(preLabel)
		r = withRouteLabel(r, routeLabel)
		// Pre-instantiate the closed `class` set under
		// (app.ID, routeLabel) on the per-route histogram the
		// first time the route is admitted. The admit() map is
		// non-evicting, so we guard with a second sync.Map key
		// (routeSetsPi) keyed by (app.ID, routeLabel) so the
		// pre-instantiation runs exactly once per app per route.
		// The dedupe keeps the hot path allocation-free after
		// first sight.
		h.preInstantiateAppRoute(app.ID, routeLabel)
	}

	// Issue #561 / ADR-089 PR 4 — apply the per-host
	// kind=redirect / kind=rewrite / kind=headers edge rules
	// against the (possibly substituted) App context. Runs AFTER
	// route substitution + Lookup so the same-account check sees
	// the right AccountID. nil-safe: all three helpers short-
	// circuit when h.edgeRules is nil (PR 3 behaviour preserved
	// for unit tests + the e2e harness).
	//
	// Order (spec §4.1.2): redirect first (short-circuits BEFORE
	// rewrite/headers so a redirect doesn't leak a rewritten path
	// or stamped header), then rewrite (mutates r.URL.Path so
	// the proxy leg sees the new path), then headers (mutates
	// r.Header + installs a response-side hook on `rec`). A
	// redirect with `Content-Type` set by a same-host headers
	// rule would leak the header; the order prevents this.
	//
	// ADR-091 amendment — the two maintenance gates fire BEFORE
	// the rewrite/redirect/headers triple and BEFORE the auth chain
	// (the same coarse-then-fine-grained posture documented at
	// §4.1.2.0 + §4.1.2.13). Coarse gate (apps.maintenance_mode)
	// beats fine-grained (kind=maintenance) per the D4 ordering
	// table — a customer who flipped the whole app should never see
	// a per-route 503 from a different Problem.code, because that
	// would leak the existence of the fine-grained rule.
	if h.applyAppsMaintenanceMode(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	if h.applyEdgeRuleMaintenance(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	if h.matchAndApplyRedirect(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	h.matchAndApplyRewrite(r, app)
	h.applyEdgeRuleHeaders(w, r, app, rec)
	// Issue #561 / ADR-091 PR 5 — apply kind=cors preflight AFTER
	// rewrite (so a rewritten path is matched against CORS rules)
	// and AFTER headers (so request-side header ops don't shadow
	// the preflight's Allow-* headers). CORS preflight short-
	// circuits with 204 + Access-Control-Allow-* headers; the
	// caller MUST `return` to skip the auth gates.
	if h.applyEdgeRuleCORS(w, r, app, rec) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	// Issue #561 / ADR-091 PR 5 — kind=jwt + kind=ip gates run
	// AFTER rewrite/headers (so a rewritten path is the one being
	// auth'd / IP-filtered) and BEFORE require_authn / public_auth
	// (so a JWT-failed or IP-denied request never reaches the
	// per-deployment auth chain — saves the bearer lookup on
	// already-rejected traffic). Each helper writes the deny
	// response + audit + metric on its own; caller MUST `return`.
	if h.applyEdgeRuleJWT(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	// ADR-118: per-app ingress IP allowlist runs BEFORE applyEdgeRuleIP
	// (kind=ip) so an IP-blocked request short-circuits all edge-rule
	// work and never wakes a Firecracker microVM — same invariant as
	// the geo gate at L4269. The two gates share the
	// clientIPFromTrustedXFF trust chain so a forged XFF fails closed
	// in both layers without double-charging the audit stream.
	if h.applyIngressIPAllowlist(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	// ADR-119: per-app ingress 'internal_only' mode runs AFTER
	// applyIngressIPAllowlist (so an IP-blocked request short-
	// circuits first) and BEFORE applyEdgeRuleIP (so a JWT-failed
	// request never wakes a Firecracker). Trust chain: gatewayd-
	// public MUST strip inbound Authorization (see
	// internal_proxy.go:~351 — added in this PR) so external
	// callers can never reach this gate with an Authorization
	// header intact. Only daemons that dial gatewayd-internal
	// directly via /run/faas/gatewayd-internal.sock reach this
	// gate. The synth-side gate (SynthServer.handleSynthesize,
	// pkg/gateway/synth.go) is the parallel cron-fired path —
	// both gates share the same verifier (cmd/gatewayd-internal/
	// internal_svc_verifier.go).
	if h.applyIngressInternalSvc(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	if h.applyEdgeRuleIP(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	// ADR-091 D21 / §4.1.2.8b — kind=geo runs AFTER kind=ip (so a
	// customer who uses IP as a poor-man's geo block today is
	// nudged to migrate to geo without their IP rules changing
	// behaviour) and BEFORE require_authn (so a denied-by-country
	// request never wakes a Firecracker microVM — the cold-wake
	// gate is gated on the country, not on auth). The gate is
	// fail-open on lookup failure (see applyEdgeRuleGeo for the
	// metric + audit + slog path).
	if h.applyEdgeRuleGeo(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// ADR-091 D24 / kind=limit body cap gate. Runs AFTER JWT / IP
	// (so rejected-on-ip / failed-jwt traffic never costs a body
	// read) and BEFORE the global MaxBytesReader installed at
	// handler.go:2944 (so the per-rule cap is the OUTER reader on
	// the in-limit path; the global reader then layers INSIDE as
	// the backstop for requests that don't match any limit rule).
	// The Content-Length fast path inside applyEdgeRuleLimit
	// delivers the "never buffer an oversize body" property —
	// the global reader alone would buffer 30 MB into memory
	// before tripping on a 5 MB rule. Placing the global reader
	// AFTER applyEdgeRuleLimit keeps the fast path observable
	// (the rule's 413 fires before the global reader wraps
	// r.Body). Same posture as validate: short-circuit on deny,
	// caller MUST `return`.
	if h.applyEdgeRuleLimit(w, r, streamingFor(h, r, app), app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// ADR-091 D20.5 amendment / kind=throttle (issue #881). Runs
	// AFTER applyEdgeRuleLimit (which short-circuits with `return
	// true` on a 413 at handler.go:2442, so a body-cap rejection
	// does NOT consume a route token — this matches
	// applyEdgeRuleThrottle's own doc at line 2661: requests denied
	// by JWT/IP/Geo/Limit MUST NOT consume a route token) and BEFORE
	// applyEdgeRuleValidate (so a schema-gate 422 never costs a
	// bucket decrement). The O(1) bucket lookup is the cheapest
	// hot-path step short of the path-glob match itself. See
	// applyEdgeRuleThrottle's doc for the rationale + the
	// cross-account audit/metric posture.
	if h.applyEdgeRuleThrottle(w, r, app) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// PR-B / kind=validate body gate. Runs AFTER rewrite /
	// headers / CORS / JWT / IP (so a rewritten path is matched
	// against validate rules, and rejected-on-ip traffic never
	// costs a body read) and BEFORE require_authn / public_auth
	// (so a 4xx on bad body never reaches the auth chain). The
	// applier buffers r.Body, restores it for the proxy leg, and
	// returns 422 + RFC 7807 problem+json on schema mismatch.
	//
	// Body-cap placement: the global MaxBytesReader cap (spec
	// §4.1) is installed HERE rather than further down so the
	// validate read is bounded. Moved from the post-rate-limit
	// block — same cap, same value, just earlier so this
	// applier sees the bounded body.
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxRequestBodyBytes)
	if h.applyEdgeRuleValidate(w, r, app, rec) {
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// ADR-093: stamp the per-request wall-clock budget onto
	// r.Context() via reqbudget.WithRemaining. Runs AFTER the
	// validate applier (which needs the inbound body read) and
	// BEFORE the wake gate (so a slow upstream never pins
	// listener / goroutine / socket resources for the full
	// platform WriteTimeout — deadline fires at the budget
	// boundary and the handler chain unwinds). The middleware
	// observes the deadline fire and writes 504 + RFC 7807
	// `code: request_budget_exceeded`; this applier never
	// short-circuits (it always stamps a budget, even on
	// miss — the plan-level default applies).
	h.applyEdgeRuleBudget(w, r, app)

	// Issue #560 / per-deployment require_authn. Runs AFTER
	// Host→app resolution (so we know which app's
	// require_authn to consult) and BEFORE the per-account
	// rate limit / wake gate (so unauthenticated traffic
	// can't trigger a cold-boot on a token-gated app — the
	// wake gate's schedd RPC and the meter's first-byte
	// observability never see traffic from a missing or
	// invalid bearer). nil-safe: a nil requireAuthnAuthn
	// (tests + dev boxes that don't wire the chain) is a
	// pass-through, so the pre-issue behaviour is preserved.
	if !h.enforceRequireAuthn(w, r, rec, app) {
		return
	}

	// Issue #477 / ADR-079 / per-app public_auth gate.
	// Runs AFTER enforceRequireAuthn (so the require_authn
	// gate fires first when both are active on the same app,
	// keeping the 401 ordering deterministic for
	// "this app wants both gates" customers) and BEFORE
	// sidecar port resolve / account limiter / wake gate
	// (so unauthenticated traffic can't trigger a cold-boot
	// on a token-gated app — the wake gate's schedd RPC
	// and the meter's first-byte observability never see
	// traffic from a missing or invalid public_auth
	// credential). nil-safe: open / unset modes pass
	// through (the pre-#477 default is preserved).
	if !h.enforcePublicAuth(w, r, rec, app) { //nolint:contextcheck // request ctx is the canonical inbound ctx; the helper uses r.Context() internally so passing ctx separately would shadow it.
		return
	}

	// ADR-122 §Decision: kind=cache serve path. Consulted AFTER
	// enforcePublicAuth (so a cache hit cannot bypass the auth
	// gate) and BEFORE the wake gate (so a hit returns without
	// calling gate.Wait — no VM, no gb_ram_hour). The applier
	// returns true when the request was fully served from the
	// cache (no wake) and false on miss (the caller falls through
	// to the existing wake path).
	//
	// Placement also AFTER CORS (line 4241, which short-circuits
	// preflight OPTIONS) so preflight responses are never
	// cached — an OPTIONS cached against the wrong Origin is a
	// real CORS bypass.
	cacheRule := (*EdgeRuleCacheResolved)(nil)
	if served, rule := h.applyEdgeRuleCache(w, r, app, rec); served {
		return
	} else if rule != nil && h.responseCache != nil && r.Header.Get("Authorization") == "" && !hasSessionCookie(r) && (r.Method == "GET" || r.Method == "HEAD") {
		// Miss path — install the cacheWriter tee so the
		// upstream response populates the cache. The rule
		// non-nil + auth-bypass-clear + method-in-vocab
		// predicate is repeated here (rather than threaded
		// back through applyEdgeRuleCache) so the security
		// check is the single chokepoint: an authed request
		// cannot reach the tee even if applyEdgeRuleCache
		// itself short-circuited to a miss.
		cw := newCacheWriter(w, rec, rule, ResponseCachePerEntryMaxBytes)
		w = cw
		cacheRule = rule
		defer func() {
			if cw.shouldStore() {
				key := CacheKey{
					AppID:          app.ID,
					DeploymentID:   "",
					RuleID:         rule.ID,
					Method:         r.Method,
					NormalizedPath: r.URL.Path,
					Query:          sortQuery(r.URL.RawQuery),
					VaryHash:       computeVaryHash(r, rule.VaryOn),
				}
				cw.finishCacheCapture(h.responseCache, key, time.Now())
			} else {
				// shouldStore() returned false — bump the
				// store_skipped counter so the dashboard
				// chip surfaces "why isn't my cache
				// populating?". The actual reason is opaque
				// (predicate veto) — a follow-on ADR can
				// widen the counter with a `reason` label
				// if operators need finer breakdown.
				h.metricsIncCacheOutcome("store_skipped")
			}
			// Refresh the occupancy gauges regardless of the
			// store outcome — the gauge is a snapshot, not a
			// delta.
			if h.metrics != nil {
				h.metrics.responseCacheBytes.Set(float64(h.responseCache.Bytes()))
				h.metrics.responseCacheEntries.Set(float64(h.responseCache.Len()))
			}
		}()
		// Stash the matched rule on the request ctx so the
		// wake-failure branch further down can consult the
		// cache for a stale-if-error entry. Without this
		// stash, the gate-failure path would have to
		// re-run the matcher (the rule was already
		// resolved above, no point doing it twice).
		if rule != nil {
			r = r.WithContext(withCacheRuleContext(r.Context(), rule, app.ID, r.Method, r.URL.Path, sortQuery(r.URL.RawQuery), computeVaryHash(r, rule.VaryOn)))
		}
		_ = cacheRule
	}

	// Issue #463 / ADR-069 / ADR-071 / PR-C §5: resolve the
	// sidecar port when sidecarName != "". A sidecarName
	// that doesn't match the deployment's sidecar roster
	// is a 404 — the customer-facing URL `host--sidecar`
	// only succeeds if the deployment actually declares
	// that sidecar. The port is stored on a local variable
	// so the picker's Target.Port assignment later in this
	// handler sees the sidecar override instead of the
	// main app's port.
	if sidecarName != "" {
		port, sidecarOK := SidecarSelectorForApp(app, sidecarName)
		if !sidecarOK {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
				api.CodeNotFound, "No such sidecar",
				fmt.Sprintf("app %q has no sidecar named %q", app.ID, sidecarName)))
			h.observe(r, rec.status, app.ID, "", false, Target{})
			return
		}
		// Pin the target-port override on the request's
		// logical port. The forwarder reads Target.Port to
		// populate ForwardHTTPRequestInit.Port (PR-B); the
		// picker uses per-port forwarders when present.
		// We can't override Target.Port on the per-target
		// cursor (Target.Port is per-instance and the
		// sidecar-port override applies to ALL instances
		// of the same deployment), so the forwarder
		// reads the sidecar-port override from the
		// request context via a sentinel added below.
		_ = port
		r = withSidecarPort(r, port)
	}

	// Issue #273 / ADR-042 — pre-instantiate the closed (class) set
	// for this app so its histogram rows surface from the first
	// request rather than after the first observation. App IDs are
	// runtime values, but the inner class set is bounded; the sync.Map
	// dedupes so the hot path stays allocation-free after first sight.
	h.preInstantiateApp(app.ID)

	// Per-account rate limit (ADR-040 / issue #292). Runs BEFORE the
	// per-app limit so a botnet rotating across many apps within an
	// account cannot evade the throttle by keeping per-app rps low.
	// Empty AccountID is only reachable from fakeBackend unit tests
	// (production joins always populate it via pgRouter.toApp) — pass
	// through unmetered and log once per process so the test suite
	// keeps working without flooding logs.
	if app.AccountID == "" {
		h.warnEmptyAccountOnce()
	} else if !h.accountLimiter.AllowAccount(r.Context(), app.AccountID, app.Plan) { //nolint:contextcheck // r.Context() is the inherited per-request ctx in ServeHTTP
		w.Header().Set("Retry-After", "1")
		w.Header().Set("x-faas-rate-limit-scope", "account")
		// Per-account 429 still surfaces the per-account bucket state
		// so a customer debugging a 429 storm can see which throttle
		// tripped. Distinct X-AccountRateLimit-* header family so
		// generic tooling that auto-parses X-RateLimit-* doesn't
		// conflate per-app and per-account values (Finding 6).
		h.writeAccountRateLimitHeaders(w, app.AccountID, app.Plan)
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
			"Rate limit exceeded", "slow down and retry"))
		if h.metrics != nil {
			h.metrics.ObserveAccountRateLimit(app.AccountID, string(app.Plan))
		}
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// Per-app rate limit (spec §4.1). Over-limit → 429.
	if !h.limiter.Allow(r.Context(), app.ID, app.Plan) { //nolint:contextcheck // r.Context() is the inherited per-request ctx in ServeHTTP
		w.Header().Set("Retry-After", "1")
		w.Header().Set("x-faas-rate-limit-scope", "app")
		// 429 path: write the post-decrement bucket snapshot so
		// clients can compute Retry-After locally without parsing the
		// problem+json body. The header set runs before the
		// api.WriteProblem below so the body has time to read them.
		h.writeAppRateLimitHeaders(w, app.ID, app.Plan)
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
			"Rate limit exceeded", "slow down and retry"))
		if h.metrics != nil {
			h.metrics.ObserveRateLimit(app.ID, string(app.Plan))
		}
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}

	// Success path: stamp the per-app headers BEFORE the proxy runs so
	// they reach the wire regardless of what the upstream does (a
	// committed upstream response body would otherwise overwrite the
	// headers we set here). Allow already consumed one token above; the
	// Peek snapshot therefore reflects "tokens left after this
	// request" which is the standard X-RateLimit-Remaining contract.
	h.writeAppRateLimitHeaders(w, app.ID, app.Plan)

	burstDone := h.burstPressure.begin(app.ID)
	defer burstDone()
	limits, _ := api.LimitsFor(app.Plan)
	var (
		cold       bool
		wakeID     string
		wakeMethod WakeMethod
		err        error
	)

	// PickWarm is the combined warm-path decision for the production backend. A
	// routable target proves that no wake is needed, so avoid the previous
	// HealthyCount → ensureCapacity → Pick sequence and its extra target-cache
	// synchronization on every warm request. Legacy/custom backends retain the
	// original ordering so their test seams and post-admission race behavior do
	// not change. A failed warm probe still enters the existing single-flight
	// wake path; the gate re-checks HealthyCount under its lock.
	pick := PickResult{}
	if warmPicker, ok := h.backend.(warmPathPicker); ok {
		pick = warmPicker.PickWarm(app.ID)
	}
	if !pick.OK {
		// Per-app fan-out admission (issue #168). The WakeGate's
		// shouldWake predicate runs HealthyCount against the plan's
		// effective max_concurrency, so a burst of N requests admits up to
		// N instances before short-circuiting.
		//nolint:contextcheck // request ctx at handler boundary.
		cold, wakeID, wakeMethod, err = h.ensureCapacity(r.Context(), app.ID, app.AccountID, app.Scope, limits.MaxConcurrency, app.Plan)
		if err != nil {
			// The per-app gate rejects excess cold-wake followers before the
			// gateway-wide admission queue is reached. Count that outcome on
			// the same bounded admission surface so operators can distinguish
			// an app-local queue from a saturated gateway queue.
			if h.metrics != nil && errors.Is(err, ErrQueueFull) {
				h.metrics.ObserveWakeAdmission(string(app.Plan), err, false, 0)
			}
			// ADR-122 §Decision: kind=cache stale-on-error path.
			// On wake failure (queue full, bootstrap abort, etc.)
			// consult the cache for a stale entry BEFORE falling
			// through to writeWakeError. A stale serve on origin
			// failure is strictly better than a hard 503 — the
			// body is recent enough that the customer experience
			// stays smooth, and the alternative (503) loses both
			// the request AND the wake budget for nothing.
			if served, _ := h.tryServeStaleOnWakeError(w, r, app, rec); served {
				return
			}
			writeWakeError(w, err)
			h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
			return
		}
		// The wake guarantees one routable target in the normal case. Re-pick
		// after the gate because the target may have been populated by a peer
		// or by the leader's admission.
		pick = h.backend.Pick(app.ID)
	}
	// The first request above guarantees one routable target. Reconcile the
	// request pressure accumulated by the whole burst before forwarding so
	// requests do not all pile onto that first target while sibling VMs are
	// still restoring. The admission worker is detached internally, but this
	// request remains cancellable by its own budget.
	if burstErr := h.maybeBurstCapacity(r.Context(), app, limits.MaxConcurrency, limits.ConcurrencyPerVMBound); burstErr != nil {
		// A burst that cannot become routable within the request budget is
		// a controlled timeout, not an upstream 502. Client disconnects
		// remain silent; genuine admission failures use the normal
		// capacity problem response.
		writeBurstCapacityError(w, r, burstErr)
		h.observe(r, rec.status, app.ID, string(app.Plan), cold, Target{})
		return
	}

	// Wake-fan-out (issue #556 / PR-C): when Pick landed on a
	// cold bucket in a multi-deployment app, signal the handler
	// via ColdBucket. Admit an instance on that specific
	// deployment so the retry Pick has a routable Target.
	// Bounded to ONE admit per request — sustained cold-bucket
	// hits are recovered via the next deployment_changed notify
	// that re-seeds the cache.
	if !pick.OK && pick.ColdBucket != "" {
		//nolint:contextcheck // request ctx at handler boundary; this is the wake-fan-out retry branch.
		if _, _, _, err := h.backend.Admit(r.Context(), app.ID, pick.ColdBucket, app.Scope, sched.TriggerGateway, limits.MaxConcurrency); err != nil {
			// Log-and-continue: the existing "warmest bucket"
			// fallback inside Pick already handled the
			// fallback path. Failure here means the cold
			// bucket won't wake this request — the next
			// notify will refresh weights.
			h.log.Warn("apid: wake-fan-out admit failed", "err", err, "deployment_id", pick.ColdBucket)
		}
		pick = h.backend.Pick(app.ID)
	}
	if !pick.OK {
		// Race: every cached instance was evicted between
		// ensureCapacity returning and our Pick. Surface the observed
		// (current) HealthyCount so the operator's metrics panel
		// shows 0 vs the cap (was 1+ microseconds ago).
		writeWakeError(w, api.ErrAppConcurrencyReached(limits, h.backend.HealthyCount(app.ID)))
		h.observe(r, rec.status, app.ID, string(app.Plan), false, Target{})
		return
	}
	target := pick.Target

	// Mirror fan-out (issue #72 / ADR-124 / ADR-125 PR-A3). After
	// the customer request has been routed, fan out one goroutine
	// per enabled rule (closed set: bounded by Limits.MirrorTargetsPerApp ≤
	// 3 per app). The dispatch goroutine is fire-and-forget — it
	// derives its own detached ctx (ADR-098 pattern, mirror of
	// pkg/gateway/gate.go:172-205) so the customer's request
	// cancellation never reaches the mirror. A wedged mirror VM is
	// bounded by MirrorMaxLifetimeSeconds=5. Cache miss returns
	// (nil, false) and is treated as "no mirror", not an error —
	// the notifier-driven RefreshMirrorRules populates the cache
	// asynchronously. The lookup itself does NOT block on the
	// store read.
	//
	// Source snapshot (code-review PR-A3 #1, #2): the dispatch
	// goroutine needs the source status + body to drive
	// ClassifyResult AND to replay the body against the mirror.
	// The proxy consumes r.Body downstream and the status is only
	// known after the proxy commits a response header, so the
	// handler captures both BEFORE fanout:
	//
	//   - sourceBody: io.ReadAll(r.Body) capped at MirrorBodySnapshotCap
	//     bytes (default 64 KiB — more than enough for status_diff
	//     detection; larger bodies still go to the source VM
	//     unbuffered).
	//   - r.Body is restored to a fresh bytes.Reader so the proxy
	//     downstream sees the full body unchanged.
	//   - sourceStatus: read from `rec` after the proxy commits
	//     WriteHeader. The fanout schedules BEFORE the proxy runs,
	//     so the goroutine reads rec.status via the shared
	//     statusRecorder pointer. nil body when the capture
	//     failed (oversized body); statusDiff defaults to true so
	//     the metric surfaces "we don't know what the source
	//     did" rather than a silent no-diff.
	if rules, ok := h.backend.LookupMirrorRules(r.Context(), app.ID); ok { //nolint:contextcheck // request ctx at handler boundary.
		sourceBody, restoreBody := snapshotSourceBody(r)
		defer restoreBody() // safety net in case the goroutine didn't already take ownership
		// Install the cross-goroutine status sink BEFORE the proxy
		// commits its WriteHeader, so the dispatchMirror goroutine
		// reads the committed status via rec.captureStatusForMirror()
		// once its round-trip completes (proxy is local + fast,
		// round-trip is to a cold-boot VM, so the goroutine's read
		// lands after the proxy's store in practice — the atomic
		// makes the handoff explicit anyway).
		rec.mirrorStatusSink = &atomic.Int32{}
		for _, rule := range rules {
			rule := rule
			if !shouldMirrorRequest(rule.Percent, pick.Picked) {
				continue
			}
			go h.dispatchMirror(r.Context(), target.InstanceID, &target, rule, snapshotRequestForMirror(r), sourceBody, rec)
		}
	}

	// Stamp the per-instance identity on the request BEFORE proxying so
	// the per-node vmmd forwarder (issue #98 / ADR-028) can attribute
	// the HTTP bytes to this exact instance. Overwrites any inbound
	// x-faas-instance so an attacker can't steer the proxy to an
	// arbitrary instance by setting the header (issue #168 trust model).
	r.Header.Set("x-faas-instance", target.InstanceID)

	// Per-request wake-timing recorder (spec §6.3) installed AFTER
	// upstream stamping so the trace sees only the proxy hop, not the
	// stamping overhead.
	firstByteRec := &firstByteRecorder{}
	//nolint:contextcheck // WithFirstByteRecorder wraps context.WithValue on r.Context(); lint can't trace through the function call.
	r = r.WithContext(WithFirstByteRecorder(r.Context(), firstByteRec))

	if cold {
		// Cold-wake transparency (UX spec §6): let developers see the penalty.
		w.Header().Set(wire.WakeHeader, wire.ColdWakeValue)
	}
	if wakeID != "" {
		// Per-wake correlation handle (gaps analysis 2026-07-23). Sits
		// next to x-faas-wake so the two are co-located on every
		// response, and never set on a Phase-1 fast-path response.
		w.Header().Set("x-faas-wake-id", wakeID)
	}

	// The forwarding bridge can detect a terminal vmmd/netns failure after
	// routing has already selected this target. Evict that one cache entry so
	// the next request can ask schedd for a fresh instance instead of replaying
	// the same dead target forever. The signal is internal and only set by the
	// bridge on transport/liveness failures; ordinary guest 502/503 responses
	// are therefore left untouched.
	staleContext := r.Context()
	staleSignal := &staleTargetSignal{
		onStale: func() {
			// Evict synchronously with the transport failure so a
			// concurrent request cannot pick this known-dead target.
			// RecoverStaleTarget detaches and bounds lifecycle work in
			// the production backend; it does not inherit the client
			// cancellation even though the request context is passed in.
			if evictor, ok := h.backend.(interface {
				EvictInstance(appID, instanceID string)
			}); ok {
				evictor.EvictInstance(app.ID, target.InstanceID)
				h.log.Warn("gateway: evicted stale target", "app_id", app.ID,
					"instance_id", target.InstanceID, "node_id", target.NodeID)
			}
			if recovery, ok := h.backend.(staleTargetRecovery); ok {
				recovery.RecoverStaleTarget(staleContext, app.ID, app.Scope, limits.MaxConcurrency)
			}
		},
	}
	//nolint:contextcheck // withStaleTargetSignal intentionally inherits r.Context.
	r = r.WithContext(withStaleTargetSignal(r.Context(), staleSignal))

	// Streaming decision (PR-B / ADR-047). Four-way AND: the operator
	// must have opted the process in (h.streamingEnabled, set via
	// FAAS_GATEWAY_STREAMING), the per-app apps.streaming_enabled flag
	// must be true (set on build / PATCH / plan default), the
	// per-request Accept header must not opt out, AND the request
	// must NOT be an Upgrade (issue #708 / PR-3 review finding). The
	// Accept: application/json override is the customer-visible
	// contract from spec §4.1 — a client can force the buffered
	// path for one request without a per-app PATCH.
	//
	// Placement: AFTER Backend.Pick so the per-flush onFlush hook
	// has target.InstanceID to attribute egress bytes. BEFORE the
	// proxy runs so the wrapped ResponseWriter (which is both an
	// http.ResponseWriter and an http.Flusher via embed) is in
	// place when the upstream first calls Write. The wrap is
	// nil-safe in the sense that when the gate is false, w stays
	// the original ResponseWriter and the recorder is never
	// installed — the buffered path is a strict pass-through.
	//
	// Issue #708 (PR-3 review finding): an upgrade request is
	// ALWAYS long-lived; wrapping it in capWriter (which enforces
	// plan.MaxResponseBodyBytes, 100 MiB for Hobby+) would fire
	// onCap mid-WS-frame and break the WS protocol. The raw path
	// has its own 101 Switching Protocols contract that runs
	// before any body bytes flow, so the cap is meaningless on
	// the upgrade path. Skipping the wrap here keeps the WS
	// session alive past 100 MiB of cumulative response bytes.
	// ADR-102 D2 + D4: resolve the per-request streaming decision
	// once. The decision carries:
	//   - Status: the Streaming-Status enum value, stamped
	//     UNCONDITIONALLY below so buffered responses also carry
	//     the header (and pinned-SDK customers with Accept:
	//     application/json see the accept-json-downgrade variant
	//     + the advisory header per D3).
	//   - Cap: the effective response body byte cap. For non-
	//     streaming variants this is the plan-level buffered cap;
	//     for StreamingStatusStreaming it may be overridden by a
	//     matched edge rule's MaxBodyBytesStreaming field (D4).
	//   - isStreaming: the same four-conjunct predicate as the
	//     legacy streamingFor helper, used to gate the writer
	//     wrap below.
	decision, isStreaming := decideStreaming(h, r, app)

	// Streaming-Status header stamp (ADR-102 D2). UNCONDITIONAL —
	// the header must land on EVERY response (streaming AND
	// buffered) so customers can self-diagnose. Precedent:
	// x-faas-request-id at handler.go:3443-3451 (also stamped
	// before any Write). Status-only; the per-endpoint cap
	// doesn't go in the header — that's surfaced via the SDK
	// probe at GET /v1/apps/{slug}/streaming-cap (D6).
	w.Header().Set(api.StreamingStatusHeader, string(decision.Status))

	// Advisory header (ADR-102 D3). One-cycle hint for pinned-SDK
	// customers whose Accept defaults to application/json. The
	// accept-json-downgrade status already tells them what
	// happened; the advisory header names the action ("would have
	// buffered pre-D3") so a customer grepping for the migration
	// marker finds the doc. Deleted in ADR-102-followup when
	// the enum variant is retired.
	if decision.Status == api.StreamingStatusAcceptJSONDowngrade {
		w.Header().Set(api.StreamingStatusAcceptHintHeader, api.StreamingStatusAcceptHintValue)
	}

	if isStreaming {
		writeTimeout := app.Plan.ResponseWriteTimeout()
		// ADR-093 / PR-C: clamp the per-flush writeTimeout to the
		// inbound budget's remaining time when one is attached.
		// The flush loop re-installs the deadline on every flush
		// (statusRecorder.installFlushHook), so capping the
		// initial value caps the entire session's wall-clock
		// budget. The plan's ResponseWriteTimeout is the absolute
		// ceiling — the budget can only shorten it. When no
		// Budget is on ctx the per-flush deadline is unchanged.
		if b, ok := reqbudget.FromContext(r.Context()); ok { //nolint:contextcheck // request ctx at handler boundary; budget lookup is a reader (ctx.Value equivalent).
			if rem := b.Remaining(time.Time{}); rem > 0 && rem < writeTimeout {
				writeTimeout = rem
			}
		}
		w = h.setupStreamingWriter(w, rec, app, target, decision.Cap, writeTimeout)
		// Internal header stamp (PR-B + PR-C / ADR-047). The
		// forwarder (pkg/gateway/forwardproxy.go) reads this to
		// switch to the bidi ForwardHTTPStream RPC and lift the
		// per-request cap/timeout on the vmmd side. Not exposed
		// to the client (the request stays on the inbound HTTP
		// path); the forwarder strips x-faas-* headers before
		// bridging so the guest never sees it.
		r.Header.Set("x-faas-stream", "true")
		// ADR-124: per-app wire-protocol selector stamp. Same
		// internal-header contract as x-faas-stream above —
		// the forwarder reads x-faas-protocol as
		// observability (slog.Debug "framing selection" line
		// at forwardproxy.go::fwdStreamOnceWithEvents) and as
		// the framing knob for any future bridge-side
		// consumer (filed in spec §17 G19). Set
		// unconditionally on the streaming path so a
		// streaming gRPC call carries both the stream flag
		// and the protocol flag. Note: the actual framing
		// switch (so `grpc` actually reaches the guest's
		// `:8080` as gRPC trailers) is G19 — today the
		// bridge re-frames to H1+chunked on the guest side
		// per PR #750, so the per-app selector is metadata-
		// only on the read side.
		r.Header.Set("x-faas-protocol", decideProtocol(app))
		// ADR-047 PR-D: bookend the streaming concurrency gauge.
		// setupStreamingWriter installs the per-flush onFlush hook
		// that increments streamFlushes; this Inc opens the
		// streamActive window, balanced by the Dec below in the
		// defer after finalFlush. The buffered path never touches
		// the gauge — buffered requests durate the response in a
		// single Go-time transfer and don't participate in the
		// streaming concurrency model.
		if h.metrics != nil {
			h.metrics.ObserveStreamStart(app.ID, string(app.Plan))
		}
	}
	// Defer the Dec with the streaming flag captured so a panic
	// between ObserveStreamStart and finalFlush still drains the
	// gauge. The streaming path is the only path that incremented;
	// the buffered path is a no-op for the Dec.
	defer func() {
		if isStreaming && h.metrics != nil {
			h.metrics.ObserveStreamEnd(app.ID, string(app.Plan))
		}
	}()

	// Include admission, queueing, and restore from the ingress timestamp.
	// Starting here measures only the proxy after the guest is already ready.
	wakeStart := start
	// Issue #676 / ADR-080: detect inbound Connection: Upgrade +
	// Upgrade: <token> requests BEFORE the plain-HTTP forwarder
	// strips the hop-by-hop headers. The raw-bytes bridge carries
	// the verbatim bytes (including the upgrade headers) into the
	// guest's netns TCP socket via the ForwardRawStream RPC, so
	// the WS / h2c / MQTT-over-WS handshake survives. The
	// detector is shared between this dispatch point and the
	// public→internal hop (pkg/gateway/internal_proxy.go) so the
	// hop-by-hop strip is bypassed on both sides.
	//
	// Gate order: isUpgradeRequest first (cheapest), then the
	// per-app flag (cheap map lookup), then the rawByNode
	// installation (the production cmd/gatewayd-internal/main.go wires
	// it; tests that don't exercise the raw path leave it nil).
	//
	// Issue #707 (PR-3 review finding): an upgrade request on an
	// app with WebSocketEnabled=false (Hobby+ customer who PATCHed
	// the flag off) OR on a deployment where the raw forwarder
	// is not wired (FAAS_GATEWAY_RAW_STREAM_ENABLED=false
	// follow-up, or a unit test) MUST short-circuit to 501 with
	// x-faas-error-reason: websocket_not_on_plan. The previous
	// fall-through to proxyByNode stripped Connection + Upgrade
	// as hop-by-hop (RFC 7230 §6.1) and returned 502 from the
	// upstream — a confusing customer-facing failure that retried
	// the WS handshake in an infinite loop. A deterministic 501
	// names the cause and the WS client can back off cleanly.
	if isUpgradeRequest(r) {
		// Issue #676 / ADR-080 follow-up, PR-B: stamp
		// (plan, metrics) onto the request context so the
		// raw forwarder can label its gateway_ws_*
		// observations without a wider public signature
		// (see pkg/gateway/upgrade.go::withWSContext).
		// h.metrics is nil in the pre-metrics test corpus;
		// withWSContext no-ops on nil so the legacy test
		// path keeps compiling.
		r = r.WithContext(withWSContext(r.Context(), app.Plan, h.metrics)) //nolint:contextcheck // request ctx is the canonical inbound ctx at the HTTP handler boundary.
		if !app.WebSocketEnabled {
			if h.metrics != nil {
				h.metrics.IncWSUpgrade(string(app.Plan), WSOutcomePlanDenied)
			}
			h.writeWebSocketNotAllowed(w, app.ID, false)
			return
		}
		if h.rawByNode == nil {
			if h.metrics != nil {
				h.metrics.IncWSUpgrade(string(app.Plan), WSOutcomeBridgeDisabled)
			}
			h.writeWebSocketNotAllowed(w, app.ID, true)
			return
		}
		if h.metrics != nil {
			h.metrics.IncWSUpgrade(string(app.Plan), WSOutcomeAccepted)
		}
		// ADR-064 wake-timeline vocab: stamp the upgrade flag so
		// downstream observability (slog fields, dashboards)
		// can distinguish raw-bytes sessions from plain HTTP
		// without re-deriving from Connection/Upgrade.
		r.Header.Set("x-faas-upgrade", "true")
		h.rawByNode(target).ServeHTTP(w, r)
		// Per-request accounting still fires for the raw path
		// (issue #676 / ADR-080): the upgrade request is one
		// HTTP request for metrics purposes — the per-request
		// ObserveRequest histogram, top-N sampler, and lastSeen
		// touch all flow through observe(). What we SKIP is
		// the streamingFallbackLog (the raw path has no
		// buffered fallback concept; a WS handshake that
		// succeeded is exactly the success case the customer
		// wants). The raw forwarder emits its own
		// gateway_ws_session_duration_seconds / bytes counters
		// via evts.Platform so the session-level accounting
		// doesn't double-count.
		h.observe(r, rec.status, app.ID, string(app.Plan), cold, target)
		h.recordUsageRequest(target, cold && wakeMethod == WakeMethodColdBoot)
		return
	}
	if h.proxyByNode != nil {
		// Issue #98 / ADR-028: Target.NodeID is the compute_node.id;
		// the forwarder dials the per-node vmmd over the overlay and
		// bridges the HTTP bytes through the instance netns via the
		// ForwardHTTPStream RPC. target stays in scope for the
		// metrics labels and observe() last-seen hook below.
		//
		// PR-C (issue #460 / ADR-053): the callback receives the
		// full Target so the forwarder can stamp
		// ForwardHTTPRequestInit.port with the per-deployment
		// override port cached at admit time.
		//
		// Issue #995 Phase 2 / ADR-121: wrap w with the buffered
		// capWriter so the forwarder surface honours the same
		// per-plan response body cap as the addr-based path
		// (cap is per-app.Plan.MaxResponseBodyBytes()). The
		// capWriter's onCap surfaces 413 problem+json if the
		// upstream Write trips the cap mid-response; the
		// connection-reset fallback that stdlib takes when Write
		// returns ErrHandlerTimeout is the hardening path firing
		// (see pkg/gateway/buffered_cap_test.go).
		planCap := app.Plan.MaxResponseBodyBytes()
		capped := h.setupBufferedCapWriter(w, app, planCap)
		// ADR-124: per-app wire-protocol selector on the
		// buffered path. Same observability-only contract as
		// the streaming stamp above — the forwarder reads
		// x-faas-protocol as observability (slog.Debug) and
		// as the framing knob for the future bridge-side
		// consumer (G19). Set unconditionally so a buffered
		// HTTP/2 / gRPC request still carries the header for
		// the forwarder to log. The actual framing switch on
		// the inner leg is a separate file (the bridge today
		// re-frames to H1+chunked on the guest side per
		// PR #750).
		r.Header.Set("x-faas-protocol", decideProtocol(app))
		h.proxyByNode(target).ServeHTTP(capped, r)
	} else {
		// Legacy addr-based path. Target.NodeID is treated as a
		// host:port by defaultProxy — preserved for tests and the
		// e2e harness without a vmmd overlay.
		//
		// Issue #995 Phase 2 / ADR-121: wrap w with the buffered
		// capWriter before the proxy runs. See the proxyByNode
		// branch above for the onCap-vs-connection-reset contract.
		planCap := app.Plan.MaxResponseBodyBytes()
		capped := h.setupBufferedCapWriter(w, app, planCap)
		h.proxyFor(target.NodeID, planCap).ServeHTTP(capped, r)
	}
	// Issue #471 / ADR-047 PR-A buffered-fallback AC. The
	// per-app streaming_enabled flag (ap.StreamingEnabled,
	// propagated through pgRouter.toApp) is the load-bearing
	// signal for the fallback log — the customer asked for
	// streaming, apid's plan-gate (CodePlanStreamingNotAllowed)
	// should already have rejected the request on a non-paid
	// plan, but a misconfiguration could land a Free app with
	// streaming_enabled=true here. PR-D tightens the gate:
	//
	//   !streaming — the buffered path actually got used. A
	//     valid Hobby+ SSE on the streaming path is the
	//     normal-flush case, NOT a fallback; logging it would
	//     be a noisy false positive.
	//   app.Plan == PlanFree — the misconfig surface is
	//     specifically Free+flag. Hobby/Pro/Scale plans
	//     default streaming_enabled=true at the apid layer
	//     (per PR-A), so the buffered path on those plans is
	//     the operator's FAAS_GATEWAY_STREAMING flag being
	//     off, not a customer misconfig. Log only when both
	//     gates fire.
	//
	// The dedup in streamingFallbackLog keeps the hot path
	// allocation-free after the first observation per
	// (appID, contentType), so the log fires at most once per
	// misconfigured app.
	// streamingFallbackLog gate (ADR-102). Pre-ADR-102 this fired
	// whenever a Free app with streaming_enabled=true served a
	// text/event-stream response — the silent buffered fallback
	// that D5 closes at apid. Post-ADR-102 the gate fires on
	// plan-disallows (legacy Free rows that pre-date D5) and on
	// flag-disabled (a customer with streaming_enabled=false who
	// is hitting a text/event-stream endpoint). D3's
	// accept-json-downgrade variant does NOT fire — that case
	// streams for real, the status is informational only.
	if !isStreaming && app.StreamingEnabled &&
		app.Plan == api.PlanFree &&
		strings.HasPrefix(strings.ToLower(rec.ContentType), "text/event-stream") {
		h.streamingFallbackLog(app.ID, rec.ContentType)
	}
	h.observe(r, rec.status, app.ID, string(app.Plan), cold, target)
	h.recordUsageRequest(target, cold && wakeMethod == WakeMethodColdBoot)
	// PR-B residual capture. On the streaming path the per-flush
	// deltas already attributed every byte that hit the wire; the
	// one outstanding delta is the trailing slice between the
	// last periodic flush and the upstream finishing. finalFlush
	// fires one more onFlush with rec.Bytes (the cumulative
	// count) which the hook subtracts against its lastReported.
	// The buffered path short-circuits via nil-safe flusher ==
	// nil. The streaming flag is set by setupStreamingWriter
	// alongside flusher, so checking streaming here would be
	// equivalent to checking flusher != nil; we use flusher
	// directly to keep the helper self-contained.
	if rec.flusher != nil {
		rec.finalFlush()
	}
	// ADR-046 (per-instance egress metering, telemetry only):
	// record the response body bytes that egressed the gateway on
	// this instance. Gated on 2xx/3xx because 4xx/5xx the
	// ReverseProxy never actually wrote the response body to the
	// wire (it short-circuited on the upstream reject) — billing
	// bytes that didn't egress would be wrong on both ends of the
	// financial model. nil-safe for unit tests.
	h.recordEgress(rec, target, app)
	if cold && h.metrics != nil {
		// Wake latency is "request-received to first upstream byte". The
		// wake-timing RoundTripper stamps the inbound request's recorder at
		// GotFirstResponseByte; reading it back here yields the actual wake
		// slice, not "wake + full upstream body copy" (the prior proxy return
		// measurement). On any path where the stamp never landed (proxy error
		// before headers), we fall back to the full duration with a Warn so
		// the gap is observable but the dashboard still gets a sample.
		firstByteAt, ok := FirstByteFrom(r)
		if !ok {
			h.log.Warn("gateway: wake-timing first-byte stamp missing; observing full request duration",
				"app", app.ID, "node", target.NodeID, "instance", target.InstanceID)
			firstByteAt = time.Now()
		}
		h.metrics.ObserveColdBoot(app.ID, firstByteAt.Sub(wakeStart), target.NodeID)
		// Wake-locality classifier (PR scale-out readiness). Increment
		// AFTER the existing first-byte observation so the 350 ms
		// measurement path is unchanged. Only fires on a real admit
		// (cold==true), so warm requests, at-capacity benign outcomes,
		// and admission errors do not enumerate. Today's outcomes are
		// local_snapshot / local_coldboot; remote_* outcomes slot in
		// when a second compute node joins.
		h.metrics.ObserveWakeLocality(wakeMethod.String())
		// Snapshot-tier counter (issue #470 / PR #470-FU-B). The
		// tier ∈ {warm, init, cold} refinement of the wake
		// outcome is produced by PR #470-FU-A (engine
		// usableSnapshotForWake). Until the engine threads the
		// tier onto the Admit response, we map the existing
		// WakeMethod to the closest tier: snapshot→init (today
		// every restored snapshot is init; warm is a refinement)
		// and cold-boot→cold. Update to the real tier field once
		// PR #470-FU-A lands; the Observe method's empty-string
		// fallback already covers the transition seam.
		h.metrics.ObserveWakeSnapshotTier(tierFromWakeMethod(wakeMethod))
	}
}

// observe emits one metric increment + one structured log line per request.
// Always called exactly once on the ServeHTTP exit path; missing it would
// skew the §12 dashboard. On a 2xx response it also Touches the LastSeenSink
// keyed by InstanceID (issue #168 — per-instance attribution survives the
// multi-instance fan-out where multiple instances share a single node).
//
// ADR-093: routeLabel is the per-request route label computed
// at the post-Lookup derivation site (handler.go's `haveApp:`
// block) and stashed on the request context via withRouteLabel.
// Empty when the app is not opted in. observe() reads the
// context here so the call sites stay agnostic of the
// routeLabelSet; the empty-string sentinels mirror the
// `gateway_requests_total{app="-"}` pattern.
func (h *Handler) observe(r *http.Request, status int, appID, plan string, cold bool, target Target) {
	code := statusClass(status)
	requestID := requestIDFrom(r)
	// Read the stashed route label BEFORE the sentinel-coercion
	// below — the route label is independent of appID and stays
	// empty when the app is not opted in OR the request didn't
	// resolve to an app.
	routeLabel := RouteLabelFrom(r)
	// Measure elapsed against the same start stamp set at the top of
	// ServeHTTP (issue #273 / ADR-042 fixed the WithStartTime dead-code
	// bug so this is now request-received → handler-return, not
	// "now() — now()").
	elapsed := time.Since(startTime(r))
	if h.metrics != nil {
		// Use placeholder labels for the unknown-host path so 404s show up on
		// the dashboard under a sentinel app_id (e.g. "-" or "").
		if appID == "" {
			appID = "-"
			plan = "-"
		}
		h.metrics.ObserveRequest(appID, plan, code)
		// Per-app full request duration histogram (issue #273 / ADR-042).
		// The class label is the 3-digit status bucketed to 2xx/3xx/4xx/5xx
		// to keep cardinality bounded — full status codes would explode
		// per-app series count past 60× the class-based set.
		// Per-app full request duration histogram with the bounded
		// deployment label (issue #273 / ADR-042 + ADR-127 §Decision
		// 4 / Debugger UX v1). The deployment id is admitted via
		// deploymentLabelSet, capped per-app at the customer's plan
		// cap (Free=0, Hobby=10, Pro=50, Scale=200 — see
		// pkg/api/limits.go:DebugTelemetryDeploymentsPerApp). Empty
		// deployment (legacy single-targetSet) routes to the ""
		// reserved sentinel without consuming capacity. Over-cap
		// collapses to "__other__". Replaces the prior
		// ObserveRequestDuration call; that helper is retained for
		// test paths that don't carry a deployment id.
		deploymentLabel := h.metrics.deploymentLabels.admit(appID, api.Plan(plan), target.DeploymentID)
		h.metrics.ObserveRequestDurationByDeployment(appID, statusClassBucket(status), deploymentLabel, elapsed)
		// ADR-093: per-route emission. Gated on a non-empty
		// routeLabel (the routeLabelSet always returns a non-empty
		// label for an opted-in app — the empty string is the
		// reserved "no appID" sentinel and would create a
		// route="__empty__" series that nobody reads). The plan
		// for non-opt-in apps is the per-app counters above
		// (preserved per ADR-042 §1 deviation).
		if routeLabel != "" {
			h.metrics.ObserveRequestRoute(appID, plan, routeLabel, code)
			h.metrics.ObserveRequestDurationRoute(appID, routeLabel, statusClassBucket(status), elapsed)
			if status >= 400 {
				h.metrics.RequestFailureRoute(appID, plan, routeLabel, code)
			}
		}
	}
	// Issue #300: feed the per-tenant rolling count for the
	// 5s gateway_top_tenant_rps sampler (cmd/gatewayd-internal/topn.go).
	// The sentinel "-" id keeps unknown-host traffic off the
	// top-N gauge (it would otherwise flood the gauge with one
	// series per scanner host). Cheap path — the sampler is
	// the only writer to the gauge; this call only bumps the
	// rolling count. Separated from the h.metrics != nil check
	// because the sampler is wired via SetTopNSample and has
	// its own nil-handling (topNSample is nil in unit tests
	// that construct Handler without metrics).
	if appID != "" && appID != "-" && h.topNSample != nil {
		h.topNSample(appID)
	}
	// Use the locally-measured elapsed (request-received → handler-return).
	// WithStartTime was dead code in the repo (issue #273 / ADR-042); the
	// WithContext call at the top of ServeHTTP now stamps the real start
	// time so the slog latency_ms field is no longer effectively ~0. Doing
	// the time.Since(startTime(r)) call here would yield the same result
	// but recomputes; `elapsed` was already measured above.
	(&requestLogger{log: h.log}).Log(appID, code, elapsed, cold, requestID)

	// Idle reaper hook (spec §4.1): 2xx → the instance is alive. 4xx/5xx are
	// not evidence of activity (a misconfigured client can hammer a dead
	// instance with 401s forever and we'd never park it).
	if h.lastSeen != nil && status >= 200 && status < 300 && target.InstanceID != "" {
		h.lastSeen.Touch(target.InstanceID, time.Now())
	}
	// ADR-127: enqueue a request-telemetry row at the single
	// exit funnel. nil-safe — the recorder is wired at boot in
	// cmd/gatewayd-internal/main.go; unit tests + older paths
	// leave it nil and the enqueue is skipped. The row drops
	// silently when accountID is unset (pre-picker path: no
	// haveApp: stamp landed). DeploymentID is parsed from the
	// resolved target; empty when the picker fell back to the
	// legacy single-targetSet behavior (Target.DeploymentID ""
	// — see handler.go:407-410). The Publisher's dedupe
	// (request_telemetry_publisher.go) collapses the burst later.
	if h.requestTelemetry != nil {
		acctUUID := accountIDFromContext(r.Context())
		appUUID := appIDFromContext(r.Context())
		if acctUUID != uuid.Nil && appUUID != uuid.Nil {
			var deploymentUUID uuid.UUID
			if target.DeploymentID != "" {
				deploymentUUID, _ = uuid.Parse(target.DeploymentID)
			}
			telemetryRoute := routeLabel
			if telemetryRoute == "" {
				// Route metrics are optional; the telemetry schema requires a label.
				telemetryRoute = otherRouteLabel
			}
			h.requestTelemetry.RecordFromObserve(RequestTelemetryRow{
				AccountID:    acctUUID,
				AppID:        appUUID,
				DeploymentID: deploymentUUID,
				Route:        telemetryRoute,
				Method:       r.Method,
				Status:       status,
				LatencyMS:    int(elapsed / time.Millisecond),
				ColdBoot:     cold,
				TraceID:      requestID,
				ReceivedAt:   time.Now(),
			})
		}
	}
}

// statusClass turns an HTTP status into a 3-digit label ("200", "404", "503").
func statusClass(status int) string {
	if status < 100 || status > 999 {
		status = http.StatusInternalServerError
	}
	const digits = "0123456789"
	return string([]byte{digits[(status/100)%10], digits[(status/10)%10], digits[status%10]})
}

// statusClassBucket turns an HTTP status into a class label for the
// gateway_request_duration_seconds histogram (issue #273 / ADR-042).
// Distinct from statusClass above: that one returns the FULL 3-digit
// code (used for the counter label so dashboards can drill into e.g.
// 404 vs 503); this one buckets to the closed 5-set so a histogram
// with ~13 series per label combo stays bounded per app.
//
// The 1xx arm (issue #709 / PR-3 review finding): a successful
// WebSocket / h2c handshake returns 101 Switching Protocols via
// rawStreamOnceWithEvents; bucketing that as 5xx would inflate the
// §12 dashboard's errors panel on every successful WS upgrade.
// 100 Continue / 102 Processing / 103 Early Hints are rare on the
// public edge but follow the same pattern — informational, not
// errors.
func statusClassBucket(status int) string {
	switch {
	case status >= 100 && status < 200:
		return "1xx"
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	default:
		// 5xx and anything outside 100-599 lands in 5xx — this
		// matches the §12 dashboard's "errors" panel definition.
		return "5xx"
	}
}

// preInstantiateApps deduplicates PreInstantiateApp calls per appID.
// Issue #273 / ADR-042 — PreInstantiateApp is cheap (returns the
// existing series on repeat calls), but the sync.Map.Load+Store
// short-circuits the work entirely after first sight so the hot path
// stays allocation-free. A value-typed field on Handler: the zero
// value is valid, so NewHandlerWith doesn't need to initialise it
// (avoiding a lazy-init write that would race with parallel
// ServeHTTP readers — the load test under -race caught that).
type preInstantiateApps struct{ m sync.Map }

func (p *preInstantiateApps) seen(appID string) bool {
	_, loaded := p.m.LoadOrStore(appID, struct{}{})
	return loaded
}

// routeSetFor returns the per-app *routeLabelSet for appID,
// creating a fresh one on the first sight of an opt-in app. The
// lookup is O(1) (sync.Map.Load). Returns nil when the app is not
// opted in (enabled=false) — the caller short-circuits the
// per-route emission on nil. The map is never deleted; the
// underlying routeLabelSet is non-evicting (ADR-093 D2).
//
// Concurrency: sync.Map handles the parallel first-sight race
// correctly. Two goroutines hitting the same opt-in app for the
// first time may each create a fresh *routeLabelSet; only one
// wins the LoadOrStore. The loser's *routeLabelSet is dereferenced
// by the GC and the in-flight admit() calls it observers are
// bounded by the cap, so the loser-drop is safe.
func (h *Handler) routeSetFor(appID string, enabled bool) *routeLabelSet {
	if !enabled || appID == "" {
		return nil
	}
	if v, ok := h.routeSets.Load(appID); ok {
		return v.(*routeLabelSet)
	}
	fresh := newRouteLabelSet()
	actual, _ := h.routeSets.LoadOrStore(appID, fresh)
	return actual.(*routeLabelSet)
}

// recordEgress attributes response body bytes to the (instanceID,
// minute) bucket in h.egressSink. Called after the proxy ServeHTTP
// returns so the recorder has observed every body chunk that hit
// the wire. The 2xx/3xx gate mirrors what the gateway actually
// delivered to the caller: 4xx/5xx responses never reach the body
// stage on the ReverseProxy path (it short-circuits to the error
// branch), so trying to count their bytes would over-attribute.
//
// PR-B streaming path: when the recorder is armed with a flusher
// (rec.flusher != nil, set by setupStreamingWriter), the per-flush
// onFlush hook has ALREADY recorded every byte in deltas plus the
// residual from finalFlush(). Calling recordEgress again would
// double-count (rec.Bytes + sum(deltas) = 2 × total). The streaming
// gate short-circuits the function; the buffered path remains
// unchanged.
//
// The InstanceID guard means tests that exercise only fakeBackend
// (where pick-instance is empty) skip the recording without
// crashing. The sink is nil-safe itself (RecordResponseBytes
// no-ops on a nil/empty instance id and <= 0 bytes), but the early
// return here skips the function-call overhead on the hot path.
//
// Also increments the gateway_response_bytes_total counter for the
// (app.id, app.plan) tuple. The plan label uses the same string
// cast as the rest of handler.observe; if it changes, audit the
// §12 dashboard panels to keep the join on plan stable.
func (h *Handler) recordEgress(rec *statusRecorder, target Target, app App) {
	if h == nil || rec == nil {
		return
	}
	// PR-B streaming path: per-flush + residual already covered
	// the byte accounting; suppress the once-per-response call to
	// avoid double-counting.
	if rec.flusher != nil {
		return
	}
	if rec.status < 200 || rec.status >= 400 {
		return
	}
	if rec.Bytes <= 0 {
		return
	}
	if h.egressSink != nil && target.InstanceID != "" {
		h.egressSink.RecordResponseBytes(target.InstanceID, rec.Bytes)
	}
	if h.metrics != nil && app.ID != "" {
		h.metrics.ObserveResponseBytes(app.ID, string(app.Plan), rec.Bytes)
	}
}

// recordUsageRequest attributes one request that reached an instance to the
// same minute-bucketed stream as response bytes. The wake method comes from
// schedd's authoritative wake result, so restores are not mislabeled as cold
// boots merely because the request had to wake a parked instance.
func (h *Handler) recordUsageRequest(target Target, coldBoot bool) {
	if h.egressSink == nil || target.InstanceID == "" {
		return
	}
	h.egressSink.RecordRequest(target.InstanceID, coldBoot)
}

// writeAppRateLimitHeaders writes the X-RateLimit-{Limit,Remaining,Reset}
// header trio for the per-app bucket after the most recent Allow has
// consumed one token. Skips silently when Peek returns ok=false (a
// noop limiter, an unknown plan, or a bucket the limiter has not yet
// seen for this app) — the absence of the headers is the loader-side
// signal that the value is "not yet established" rather than
// "exhausted". Set on the 2xx success path so dashboards that read
// X-RateLimit-* see the post-consume value, and on the per-app 429
// path so clients can compute Retry-After from the headers without
// parsing the problem+json body.
//
// Cross-process limitation (Finding 6 / ADR-040 follow-up): in a
// multi-gatewayd-internal fleet each gatewayd-internal process keeps its own bucket;
// the value reflects this gatewayd-internal's view. ADR-040 already flagged
// this; the X-RateLimit-* contract is documented to expose rather
// than to remove the limitation. When a shared-bucket design ships
// this method moves behind a shared-state seam with the same call
// signature.
func (h *Handler) writeAppRateLimitHeaders(w http.ResponseWriter, appID string, plan api.Plan) {
	if h == nil || h.limiter == nil {
		return
	}
	limit, remaining, reset, ok := h.limiter.Peek(appID, plan)
	if !ok {
		return
	}
	w.Header().Set("X-RateLimit-Limit", intToString(limit))
	w.Header().Set("X-RateLimit-Remaining", intToString(remaining))
	w.Header().Set("X-RateLimit-Reset", intToString(reset))
}

// writeAccountRateLimitHeaders writes the X-AccountRateLimit-*
// header trio for the per-account bucket (ADR-040 / Finding 6).
// Distinct header family from X-RateLimit-* so generic
// X-RateLimit-* consumers (e.g. browser DevTools) don't conflate the
// two scopes. Set only on the per-account 429 path today; the
// per-account value is rarely useful to a customer on the 2xx path
// (they care about their app's bucket, not their account-wide one).
func (h *Handler) writeAccountRateLimitHeaders(w http.ResponseWriter, accountID string, plan api.Plan) {
	if h == nil || h.accountLimiter == nil || accountID == "" {
		return
	}
	limit, remaining, reset, ok := h.accountLimiter.PeekAccount(accountID, plan)
	if !ok {
		return
	}
	w.Header().Set("X-AccountRateLimit-Limit", intToString(limit))
	w.Header().Set("X-AccountRateLimit-Remaining", intToString(remaining))
	w.Header().Set("X-AccountRateLimit-Reset", intToString(reset))
}

// writeRouteRateLimitHeaders writes the X-RouteRateLimit-* header
// trio for the per-rule bucket (ADR-091 D20.5 amendment,
// issue #881). Distinct header family from X-RateLimit-* and
// X-AccountRateLimit-* so generic X-RateLimit-* consumers (e.g.
// browser DevTools) don't conflate the three scopes. Set on both
// the per-rule 429 path (so a customer debugging a 429 storm can
// see which throttle tripped) and the 2xx success path (so a
// customer's standard X-RateLimit-*-style dashboard surfaces the
// per-rule values without bespoke parsing).
//
// rps is the rule's requests_per_second (post-clamp at apid +
// post-clamp at compile); burst is the rule's burst ceiling
// (same dual-clamp story). The limiter's PeekWithParams takes
// the same rps/burst so the visible reset time uses the same
// denominator the rule actually refills at — mirroring the
// per-app + per-account paths.
//
// policy is the X-RouteRateLimit-Policy value (ADR-104 amendment
// 5, issue #881 Phase 4 H1): "route" for rules where KeyBy ∈
// {"", "none"} (back-compat default — the value pre-Phase 4
// callers expect to read), or "per-consumer" when the consumer
// collapsed into the __other__ bucket on a per-consumer rule.
// The header is emitted on EVERY call site (never silently
// omitted) so dashboards that key off its presence don't break;
// pre-Phase 4 callers observed only the trio and the new header
// is additive.
func (h *Handler) writeRouteRateLimitHeaders(w http.ResponseWriter, bucketKey string, rps float64, burst int, policy string) {
	if h == nil || h.routeLimiter == nil || bucketKey == "" {
		return
	}
	limit, remaining, reset, ok := h.routeLimiter.PeekWithParams(bucketKey, rps, float64(burst))
	if !ok {
		return
	}
	w.Header().Set("X-RouteRateLimit-Limit", intToString(limit))
	w.Header().Set("X-RouteRateLimit-Remaining", intToString(remaining))
	w.Header().Set("X-RouteRateLimit-Reset", intToString(reset))
	// ADR-104 amendment 5 (issue #881 Phase 4 H1): the policy
	// hint is emitted unconditionally so dashboards that key off
	// its presence don't break; the value distinguishes the
	// route vs per-consumer collapse scope without polluting
	// the existing x-faas-rate-limit-scope enum.
	if policy == "" {
		policy = rateLimitScopeRoute
	}
	w.Header().Set("X-RouteRateLimit-Policy", policy)
}

// intToString is a tiny strconv.Itoa shim so handler.go doesn't grow
// a strconv import for three call sites. Avoids the existing
// scheduler.go itoa(uint64) so the package keeps a single per-type
// convention.
func intToString(i int) string {
	// Local format avoids the strconv import. Negative values render
	// with a leading "-" so a Peek that observes (somehow) a negative
	// token count is visible to the client; in practice limit /
	// remaining are non-negative by construction (the floor in Peek).
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// preInstantiateApp records appID once and delegates to
// Metrics.PreInstantiateApp. Safe on a nil h (test seam) and a nil
// Metrics (also nil-safe inside the method).
func (h *Handler) preInstantiateApp(appID string) {
	if h == nil || h.metrics == nil || appID == "" {
		return
	}
	if h.piApps.seen(appID) {
		return
	}
	h.metrics.PreInstantiateApp(appID)
}

// RouteSetForTest is the test-only seam that exposes the
// lazy-create behaviour of routeSetFor. Production must use
// the gated-with-RouteMetricsEnabled call site at the
// post-Lookup block (handler.go:2333). Exists so a unit test
// can seed a known state without standing up the full Backend +
// Edge-Rule matcher; tests should call RoutesFor on the
// returned set and AdmitForTest on the underlying *routeLabelSet.
func (h *Handler) RouteSetForTest(appID string) *routeLabelSet {
	return h.routeSetFor(appID, true)
}

// RoutesFor (ADR-093) returns a copy of the admitted route labels
// for appID, in deterministic order (insertion order via sorted
// keys). The caller is the /v1/internal/apps/{slug}/routes
// control-listener handler; the snapshot is read-only and
// allocation-bounded: at most routeLabelSetCap + reservedCount
// entries per call. Returns nil when the app is not opted in
// (Handler.routeSetFor returns nil for disabled apps) — the
// control handler renders an empty Routes array on nil so the
// dashboard can distinguish "feature off" from "no traffic yet".
//
// Concurrency: routeLabelSet.mu guards the snapshot read; the
// returned slice is a copy so the caller can iterate without
// holding the lock. Returning the underlying map directly would
// race with admit() on the hot path.
//
// Returns (routes, overflowed) so the dashboard-side caller can
// render "you have hit the 50-route cap" without counting Routes
// (which is ambiguous: 5 real routes + __route_other__ for a
// one-off wildcard probe is indistinguishable from 50 real routes
// + overflow). overflowed is true iff the app's routeLabelSet has
// reached its cap (routeLabelSetCap = 50 in production, smaller
// in tests) and additional routes are collapsing into
// __route_other__. The cap constant is exported separately as
// `routeLabelSetCap`; the function returns only the values the
// caller actually consumes (PR-B1 code-review finding: the
// earlier 3-tuple widened the surface for a value no production
// caller read).
//
// On unknown apps (routeSetFor never created the set, either
// because the app isn't opted in or because no traffic has
// reached the per-route path yet) the function returns (nil,
// false) — the caller normalises nil routes to [] and treats the
// empty state as "no data", not "below cap".
func (h *Handler) RoutesFor(appID string) (routes []string, overflowed bool) {
	if h == nil {
		return nil, false
	}
	v, ok := h.routeSets.Load(appID)
	if !ok {
		return nil, false
	}
	s := v.(*routeLabelSet)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.admitted))
	for k := range s.admitted {
		out = append(out, k)
	}
	sort.Strings(out)
	// Cap-hit is the same condition admit() checks at
	// route_label_set.go:152: len(admitted) - reservedCount >= cap.
	// Duplicated here rather than exported as a method on
	// routeLabelSet to avoid widening the type's surface (the
	// only callers are the dashboard-side wire reader and the
	// observe_route tests; both can carry the trivially-cheap
	// subtraction).
	const reservedCount = 2
	return out, len(s.admitted)-reservedCount >= s.cap
}

// preInstantiateAppRoute (ADR-093) records (appID, route) once
// and delegates to Metrics.PreInstantiateAppRoute. The dedupe is
// keyed by (appID, routeLabel) so the closed `class` set under
// the per-route histogram is written exactly once per app per
// route. Mirrors the preInstantiateApp shape above; the per-
// route dedupe uses a separate sync.Map so an app's per-app
// pre-instantiation and its per-route pre-instantiations do not
// race on the same key.
func (h *Handler) preInstantiateAppRoute(appID, routeLabel string) {
	if h == nil || h.metrics == nil || appID == "" || routeLabel == "" {
		return
	}
	key := appID + "\x00" + routeLabel
	if _, loaded := h.routeSetsPi.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	h.metrics.PreInstantiateAppRoute(appID, routeLabel)
}

// statusRecorder is a thin ResponseWriter wrapper that records the HTTP status
// that was written so metrics can label without buffering headers/body.
//
// Bytes is the cumulative response body length observed via Write. Streaming
// responses (no Content-Length, Write called multiple times) accumulate the
// sum — the ADR-046 telemetry path doesn't need per-chunk fidelity, only the
// total. Negative or zero-byte callers (HEAD, 304 Not Modified, error paths
// without a body) leave Bytes at zero, which the post-proxy recording site
// treats as "no traffic to meter" (see recordEgress in ServeHTTP).
//
// ContentType (issue #471) captures the upstream response's Content-Type
// at WriteHeader time so the post-proxy site can decide whether the
// response was an SSE stream (text/event-stream) that got buffered on
// the way out. The field is read once after proxy.ServeHTTP returns.
//
// PR-B (issue #471) extends the recorder with optional streaming
// fields. The semantics: when `flusher` is nil, the recorder is a
// strict pass-through (today's buffered path) and `Write` ignores
// the onFlush hook + flush triggers. When `flusher` is non-nil
// (installed by Handler.setupStreamingWriter), `Write` calls
// maybeFlush() after the underlying write; maybeFlush triggers
// a flush when bytes-since-last-flush ≥ flushBytes
// (StreamingFlushBytesDefault, 256 KiB) OR
// time-since-last-flush ≥ flushInterval (StreamingFlushIntervalDefault,
// 200 ms) AND bytes-since-last-flush > 0. The first flush after
// WriteHeader unconditionally fires (to set the per-flush write
// deadline via http.ResponseController) and is the onFlush hook's
// caller-side gate for the per-flush tx_bytes increment.
//
// `streaming` is the boolean the gateway sets when it has decided
// to take the streaming path (operator flag + per-app flag +
// Accept opt-out). The handler reads it once at the post-proxy
// branch to decide whether to call finalFlush (the residual
// capture) and whether to skip the once-per-response recordEgress
// (the per-flush deltas already account for everything on the
// streaming path).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	Bytes       int64
	ContentType string

	// headerOps (ADR-089 / issue #561 PR 4) is the per-request
	// list of EdgeRuleHeaderOp mutations a kind=headers rule
	// applied BEFORE the proxy leg. Applied at WriteHeader commit
	// time so the response headers are stamped on the wire BEFORE
	// the status code is sent. nil = no-op (default; tests + the
	// e2e harness without edge rules). installHeaderOps is the
	// setter the applyEdgeRuleHeaders helper calls; the ops run
	// in declared order (Cloudflare "first wins" semantics for
	// `set`).
	headerOps []EdgeRuleHeaderOp

	// Streaming fields (PR-B, nil → buffered path). Install via
	// installFlushHook; the fields stay zero otherwise.
	flusher          http.Flusher
	onFlush          func(cumulativeBytes int64)
	lastFlushAt      time.Time
	lastFlushedBytes int64
	flushBytes       int64
	flushInterval    time.Duration
	// firstFlush gates the one-shot write-deadline install that
	// happens on the first flush after WriteHeader. True until the
	// first flush fires.
	firstFlush bool
	// writeDeadline is the per-flush deadline installed via
	// http.ResponseController.SetWriteDeadline on the first flush
	// (and re-installed on every subsequent flush to keep the
	// deadline sliding forward — the plan enforces "no more than
	// this many seconds of blocking write per flush window").
	writeDeadline time.Duration
	// streaming is the Handle ctx-side flag: true when the gateway
	// took the streaming path on this request. Read by the post-
	// proxy site to decide whether to call finalFlush (residual
	// capture) and whether to skip the once-per-response
	// recordEgress call (the per-flush deltas already account for
	// everything on the streaming path).
	streaming bool

	// mirrorStatusSink (issue #72 / ADR-133 / ADR-125 PR-A3
	// code-review fix) is the cross-goroutine channel the
	// dispatchMirror goroutine reads to learn the source
	// response's HTTP status. nil = "no mirror on this request"
	// (the dispatcher never scheduled a mirror, so the field
	// stays unset — ClassifyResult falls back to status=0 which
	// surfaces statusDiff=true rather than a silent no-diff).
	//
	// Handle installs the *atomic.Int32 pointer on a mirror
	// fanout path; the proxy's WriteHeader Stores the committed
	// status code; the dispatch goroutine Loads it after its
	// round-trip completes (by which time the proxy has
	// committed the response header — the proxy is local, the
	// round-trip is to a cold-boot VM, so the gateway's local
	// WriteHeader fires microseconds before the goroutine reads).
	//
	// atomic.Int32 (not int) because the writer (proxy /
	// WriteHeader) runs in a different goroutine than the
	// reader (dispatchMirror) — the Go memory model would flag a
	// plain int read/write as a race.
	mirrorStatusSink *atomic.Int32
}

// captureStatusForMirror (issue #72 / ADR-133 / ADR-125 PR-A3)
// returns the source response's HTTP status as committed by the
// proxy's WriteHeader. Returns 0 if no mirror was scheduled for
// this request (mirrorStatusSink is nil), which the dispatch
// goroutine treats as "unknown source status" — ClassifyResult
// emits statusDiff=true to surface the "we don't know what the
// source did" shape rather than a silent no-diff.
//
// Safe to call from any goroutine. nil-receiver safe.
func (s *statusRecorder) captureStatusForMirror() int {
	if s == nil || s.mirrorStatusSink == nil {
		return 0
	}
	return int(s.mirrorStatusSink.Load())
}

// installFlushHook arms the recorder for streaming. After install,
// every Write triggers maybeFlush, and every flush fires onFlush.
// The hook is nil-safe: a nil flusher or nil onFlush silently
// no-ops on the flush attempt (the recorder still tracks Bytes
// for the buffered path).
func (s *statusRecorder) installFlushHook(flusher http.Flusher, onFlush func(int64), flushBytes int64, flushInterval, writeDeadline time.Duration) {
	if flusher == nil {
		return
	}
	s.flusher = flusher
	s.onFlush = onFlush
	s.flushBytes = flushBytes
	s.flushInterval = flushInterval
	s.writeDeadline = writeDeadline
	s.firstFlush = true
	s.streaming = true
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
		// Issue #72 / ADR-133 / ADR-125 PR-A3: if Handle armed a
		// mirrorStatusSink for this request, store the committed
		// status so the async dispatchMirror goroutine can read
		// it (via captureStatusForMirror) and drive
		// ClassifyResult's statusDiff branch correctly. nil
		// sink = no mirror on this request, no-op.
		if s.mirrorStatusSink != nil {
			s.mirrorStatusSink.Store(int32(code))
		}
		// Issue #471: capture the upstream Content-Type at header
		// commit time so the post-proxy site can detect a buffered
		// SSE response for the deprecation log. Header() returns the
		// current header map (mutable until WriteHeader fires), so
		// reading it here is race-free relative to the underlying
		// ResponseWriter.
		s.ContentType = s.ResponseWriter.Header().Get("Content-Type")
		// ADR-089 / issue #561 PR 4: apply any EdgeRuleHeaderOp
		// mutations the applyEdgeRuleHeaders helper installed
		// BEFORE the status code is committed. The ops run in
		// declared order; `add` appends, `set` replaces,
		// `remove` deletes (see applyHeaderOp in handler.go).
		// Blacklist (Host, Content-Length, Transfer-Encoding,
		// Connection, x-faas-*) is enforced at apid-Validate-time
		// so we trust the slice here.
		for _, op := range s.headerOps {
			applyHeaderOp(s.Header(), op)
		}
	}
	s.ResponseWriter.WriteHeader(code)
}

// installHeaderOps (ADR-089 / issue #561 PR 4) arms the recorder
// with the kind=headers rule's response-side mutations. Called by
// applyEdgeRuleHeaders when a rule fires; nil-safe (a nil ops
// slice or a nil receiver is a no-op). The ops apply on the next
// WriteHeader call — the writer chain is unchanged (no wrapper
// insertion; we just mutate the inner Header map at commit time,
// matching the spec §4.1.2 "applied before status code" contract).
func (s *statusRecorder) installHeaderOps(ops []EdgeRuleHeaderOp) {
	if s == nil || len(ops) == 0 {
		return
	}
	s.headerOps = ops
}

// lgtm[go/reflected-xss] false-positive: statusRecorder is a pass-through; every caller writes application/json, application/problem+json (api.WriteProblem at :326/:335/:366/:384/:906/:911/:914) or proxies to a Firecracker guest rendered via html/template. See statusRecorder doc-comment.
func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		// First Write with no explicit WriteHeader → 200.
		s.status = http.StatusOK
		s.wroteHeader = true
	}

	// lgtm[go/reflected-xss] false-positive: statusRecorder is a pass-through; every caller writes application/json, application/problem+json (api.WriteProblem at :326/:335/:366/:384/:906/:911/:914) or proxies to a Firecracker guest rendered via html/template. See statusRecorder doc-comment.
	n, err := s.ResponseWriter.Write(b)
	// bytes only advance when the underlying Write actually consumes
	// the buffer — a partial write (err != nil, n<len(b)) still counts
	// what made it to the wire, because that's what egressed. The
	// !s.hasBodyWrite guard prevents double-counting in the rare case
	// where the embedded ResponseWriter is itself wrapped; we don't
	// currently wrap a second layer so it's a no-op in practice but
	// cheap insurance for the future.
	if n > 0 {
		s.Bytes += int64(n)
	}
	if s.flusher != nil {
		s.maybeFlush()
	}
	return n, err
}

// Flush is the http.Flusher pass-through. The ReverseProxy doesn't
// call it directly today (the buffered path), but a streaming
// upstream that wants to push bytes between Writes can call it
// and the same onFlush hook fires. Used by Handler.finalFlush too.
// Nil-safe: returns instantly if the recorder is on the buffered
// path (no flusher installed).
func (s *statusRecorder) Flush() {
	if s.flusher == nil {
		return
	}
	s.doFlush()
}

// maybeFlush checks the periodic-flush triggers and calls doFlush
// when any are met. Cheap when no trigger fires: a bytes-delta
// subtract + a time.Since + a comparison.
func (s *statusRecorder) maybeFlush() {
	if s.firstFlush {
		s.doFlush()
		return
	}
	bytesDelta := s.Bytes - s.lastFlushedBytes
	if bytesDelta >= s.flushBytes {
		s.doFlush()
		return
	}
	if bytesDelta > 0 && time.Since(s.lastFlushAt) >= s.flushInterval {
		s.doFlush()
		return
	}
}

// doFlush is the single point that fires onFlush + pushes bytes
// to the wire. Called from maybeFlush (periodic triggers), Flush
// (explicit upstream call), and finalFlush (residual capture).
func (s *statusRecorder) doFlush() {
	if s.firstFlush && s.writeDeadline > 0 {
		// Install the per-flush write deadline on the very first
		// flush. http.ResponseController is the post-Go-1.20 way
		// to set a write deadline on a ResponseWriter without
		// touching the underlying net/http internals; if the
		// controller is unavailable (e.g. the wrapped writer
		// predates Go 1.20), the deadline is silently skipped and
		// the http.Server.WriteTimeout safety net applies. We
		// don't error-out because production is on Go 1.23 per
		// CLAUDE.md, but the nil-check keeps the unit tests on
		// older httptest.NewRecorder paths honest.
		if rc := http.NewResponseController(s.ResponseWriter); rc != nil {
			_ = rc.SetWriteDeadline(time.Now().Add(s.writeDeadline))
		}
		s.firstFlush = false
	}
	if s.onFlush != nil {
		s.onFlush(s.Bytes)
	}
	s.flusher.Flush()
	s.lastFlushAt = time.Now()
	s.lastFlushedBytes = s.Bytes
}

// finalFlush is the residual capture. The post-proxy site calls
// this after the proxy returns on the streaming path. It fires
// onFlush one more time with the final Bytes (cumulative) so the
// per-flush tx_bytes deltas cover the trailing bytes between the
// last periodic flush and the upstream finishing. Nil-safe: no-op
// on the buffered path.
//
// The two-phase accounting (per-flush + residual) is documented
// in ADR-047; the contract is that the sum of every per-flush
// delta after proxy.ServeHTTP returns equals total egress bytes
// for the request.
func (s *statusRecorder) finalFlush() {
	if s.flusher == nil {
		return
	}
	s.doFlush()
}

// ensureCapacity (issue #168) is the request-side wake primitive.
//
// A request wakes an app only when there is no routable target. The plan's
// max_concurrency is a ceiling, not a request-per-instance target: admitting a
// new VM merely because HealthyCount < max_concurrency makes every sequential
// request cold until the ceiling is reached. Reactive scale-up belongs to the
// scheduler's signal-driven targets/scaleup workers, which have real inflight
// and RPS/CPU evidence before calling Backend.Admit.
//
// The cold path goes through the WakeGate, and the production backend's
// EnsureWarm capability delegates to schedd's cross-producer EnsureWake. This
// gives both the local gateway and other wake producers one authoritative
// single-flight operation while preserving the existing optional Backend test
// seam.
//
// Returns (cold, wakeID, method, err):
//   - cold=true on a fresh admit (one or more new instances reached RUNNING);
//     cold=false when the request hit an existing cached target with no
//     fresh admit fired.
//   - wakeID is non-empty on a fresh admit, empty when no admit fired.
//   - method reports what schedd actually did (snapshot restore or cold
//     boot) on the admitted path. WakeMethodUnspecified on
//     cold=false/at-capacity paths and on real failures.
//   - err is non-nil only on real admission failures (RAM headroom, chooser,
//     store). The benign app_concurrency_reached outcome is never lifted to
//     an error by Backend.Admit.
//
// scope (issue #272 / ADR-095 PR-B) is the per-lookup scope label
// forwarded to Backend.Admit so schedd's wake path resolves the
// preview app's deployment row (scope="pr-N") rather than the parent
// prod app's. Empty = prod (legacy). When the cold-start path calls
// coldStart and coldStart in turn calls Admit, scope is plumbed
// through both paths.
func (h *Handler) ensureCapacity(ctx context.Context, appID, accountID, scope string, maxConcurrency int, plan api.Plan) (cold bool, wakeID string, method WakeMethod, err error) {
	// HealthyCount is intentionally process-local for the hot path, but an
	// empty process-local cache is not authoritative in a multi-node fleet.
	// The empty-cache reconciliation now runs inside coldStart's WakeGate
	// leader callback. That makes the whole cache-repair → wake decision one
	// single-flight operation instead of letting every request in a burst enter
	// the reconciliation path before the gate coalesces them.
	if h.backend.HealthyCount(appID) > 0 {
		return false, "", WakeMethodUnspecified, nil
	}
	cold, wakeID, method, err = h.coldStart(ctx, appID, accountID, scope, maxConcurrency, plan)
	if err != nil {
		return false, "", WakeMethodUnspecified, err
	}
	return cold, wakeID, method, nil
}

// coldStart is path 1 of ensureCapacity: HealthyCount == 0, so we go
// through the WakeGate's single-flight coalescing. shouldWake is held
// under the gate lock and re-runs HealthyCount; if a peer's admit has
// just landed, we skip the redundant cold boot.
func (h *Handler) coldStart(ctx context.Context, appID, accountID, scope string, maxConcurrency int, plan api.Plan) (bool, string, WakeMethod, error) {
	var (
		admittedWakeID string
		cold           bool
		method         WakeMethod
	)
	policy := WakeAdmissionPolicyForPlan(plan)
	werr := h.gate.WaitWithPolicy(ctx, appID, accountID, policy,
		func() bool {
			// max_concurrency is a ceiling for scheduler-driven scale-up,
			// not a reason for the request path to create another VM. Once
			// any healthy target exists, this wake generation is satisfied.
			return h.backend.HealthyCount(appID) == 0
		},
		func(ctx context.Context) error {
			// Only the WakeGate leader reaches this callback. Repair a
			// process-local cache miss before spending a scheduler RPC on a
			// new wake. A peer wake, cron/floor worker, or pre-restart live
			// instance is therefore reused by all followers.
			if reconciler, ok := h.backend.(liveTargetReconciler); ok {
				if reconcileErr := reconciler.ReconcileLiveTargets(ctx, appID); reconcileErr != nil {
					if h.log != nil {
						h.log.Warn("gateway: live target reconciliation failed", "app_id", appID, "err", reconcileErr)
					}
				} else if h.backend.HealthyCount(appID) > 0 {
					return nil
				}
			}
			admit := func(admitCtx context.Context) error {
				if ensurer, ok := h.backend.(warmEnsurer); ok && scope == "" {
					id, m, atCapacity, e := ensurer.EnsureWarm(admitCtx, appID, scope, sched.TriggerGateway)
					if e != nil {
						return e
					}
					if atCapacity {
						return nil
					}
					admittedWakeID = id
					method = m
					cold = true
					return nil
				}
				id, m, atCapacity, e := h.backend.Admit(admitCtx, appID, "", scope, sched.TriggerGateway, maxConcurrency)
				if e != nil {
					return e
				}
				if atCapacity {
					return nil
				}
				admittedWakeID = id
				method = m
				cold = true
				return nil
			}
			if h.admissionQueue == nil {
				return admit(ctx)
			}
			queued, wait, admitErr := h.admissionQueue.Do(ctx, appID, string(plan), policy, admit)
			if h.metrics != nil {
				h.metrics.ObserveWakeAdmission(string(plan), admitErr, queued, wait)
			}
			return admitErr
		},
		// ADR-098 C7: bootstrap-cap predicate. The detached leader
		// polls this on a 1s tick; if the queue drained (the gate
		// itself tracks waiters) and there's still no live instance,
		// the leader aborts with reason "queue_empty_no_instance"
		// instead of staying alive for the full TTL. The plan
		// MaxMinInstances check would require the coldStart path to
		// re-resolve the app; the gate's total waiter count already
		// reflects the relevant signal — the leader remains a waiter
		// while its request is waiting and reaches zero only after
		// the caller has left. coldStart only runs when the picker
		// fast-path found no live instance. onAbort bumps
		// the gateway_leader_bootstrap_aborts_total counter and
		// surfaces the abort reason on the §12 dashboard chip.
		func() bool {
			return h.gate.InflightWaiters(appID) == 0 && h.backend.HealthyCount(appID) == 0
		},
		func(reason string) {
			h.metrics.ObserveLeaderBootstrapAbort(reason)
		},
	)
	if werr != nil {
		return false, "", WakeMethodUnspecified, werr
	}
	return cold, admittedWakeID, method, nil
}

func writeWakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrWakeQueueWaitTimeout):
		retryAfter := wakeRetryAfterSeconds(err, 5)
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Wake queue wait budget exceeded", "the app remained cold beyond its queue wait budget; retry shortly"))
	case errors.Is(err, ErrQueueFull):
		retryAfter := wakeRetryAfterSeconds(err, 5)
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Briefly at capacity", "the wake queue is full; retry shortly"))
	case errors.Is(err, ErrWakeAdmissionQueueFull):
		retryAfter := wakeRetryAfterSeconds(err, 5)
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Wake admission is busy", "the gateway is admitting other cold wakes; retry shortly"))
	case errors.Is(err, ErrBootstrapAborted):
		// ADR-098 C7: the leader aborted under the bootstrap cap
		// (queue empty AND no live instance). The customer should
		// retry — the next request finds the picker fast-path
		// still empty, so a fresh wake fires immediately.
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Leader bootstrap aborted", "no live instance and no plan floor; retry to wake"))
	default:
		var prob *api.Problem
		if errors.As(err, &prob) {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("wake failed"))
	}
}

func wakeRetryAfterSeconds(err error, fallback int) int {
	if fallback < 1 {
		fallback = 1
	}
	var retryAfter time.Duration
	var perAppFull *WakeQueueFullError
	var perAppTimeout *WakeQueueWaitTimeoutError
	var globalFull *WakeAdmissionQueueFullError
	var globalTimeout *WakeAdmissionQueueWaitTimeoutError
	switch {
	case errors.As(err, &perAppFull):
		retryAfter = perAppFull.RetryAfter
	case errors.As(err, &perAppTimeout):
		retryAfter = perAppTimeout.RetryAfter
	case errors.As(err, &globalFull):
		retryAfter = globalFull.RetryAfter
	case errors.As(err, &globalTimeout):
		retryAfter = globalTimeout.RetryAfter
	default:
		return fallback
	}
	seconds := int(retryAfter / time.Second)
	if retryAfter%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return fallback
	}
	return seconds
}

// sharedUpstreamTransport is the single *http.Transport gatewayd-internal uses to
// proxy to all upstream microVMs. It is wrapped in a firstByteRoundTripper
// so the wake-timing trace can stamp the inbound request's recorder at
// "first upstream response byte" (spec §6.3, §12). Sharing one transport
// across requests matches Go's stdlib expectation (connection pooling
// requires a single transport per upstream) and the spec's "single public
// listener" invariant — gatewayd-internal owns this transport exclusively.
var sharedUpstreamTransport = newFirstByteRoundTripper(&http.Transport{
	ResponseHeaderTimeout: 60 * time.Second, // spec §4.1
	IdleConnTimeout:       90 * time.Second,
})

// defaultProxy returns a reverse proxy to addr (spec §4.1: 60 s to first
// response byte). The spec's "25 MB either direction" outbound cap is
// enforced at two sites (issue #995 Phase 2 / ADR-121):
//   - Downstream: setupBufferedCapWriter, installed by ServeHTTP at
//     the dispatch site once the resolved App is in scope.
//   - Upstream: this function's ModifyResponse hook wraps the
//     upstream body in io.LimitReader(body, cap+1) so the guest
//     EOFs at cap+1 bytes instead of streaming past the cap. The
//     cap parameter threads through h.proxyFor(addr, cap); cap<=0
//     disables the upstream guard.
//
// The proxy is constructed once per (addr, cap) pair; the cap is
// set by the caller at ServeHTTP time. The downstream capWriter
// emits 413 response_too_large on cap-exceeded; the upstream
// LimitReader prevents the guest from wasting CPU + egress after
// the cap fires.
func defaultProxy(addr string, cap int64) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = sharedUpstreamTransport
	// Issue #995 Phase 2 / ADR-121 — the upstream guard. Wrap the
	// upstream's response body in io.LimitReader(body, cap+1) so a
	// runaway guest EOFs cleanly at cap+1 bytes instead of
	// continuing to stream into the downstream capWriter. The
	// downstream capWriter still emits 413 response_too_large on
	// its side; this hook is the upstream-side defence so the
	// guest's bytes stop flowing as soon as the cap is reached.
	//
	// cap <= 0 means no upstream guard (mirrors setupBufferedCapWriter's
	// no-op behaviour for a missing plan). The downstream capWriter
	// already guards against runaway bytes; the upstream LimitReader
	// just prevents the guest from wasting CPU + egress after the
	// cap has fired.
	if cap > 0 {
		limit := cap + 1
		p.ModifyResponse = func(resp *http.Response) error {
			if resp.Body != nil {
				resp.Body = struct {
					io.Reader
					io.Closer
				}{io.LimitReader(resp.Body, limit), resp.Body}
			}
			return nil
		}
	}
	return p
}

// hostname strips any port from the Host header and lowercases it.
func hostname(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}
