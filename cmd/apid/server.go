package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/auth"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// server is apid's HTTP service: the public REST API and the only writer to
// customer-intent tables (spec §4.2, §Component ownership). It validates plan
// quotas before any work, authenticates every request by API-key hash, and
// publishes row changes via pg_notify (spec §Component ownership).
//
// M5+: handlers are grouped by resource in handlers.go (apps, deployments,
// crons, domains, keys, instances, usage); this file owns the middleware
// (auth, idempotent), the route table, and small request/response helpers.
// M7.5: githubd is the GitHub App integration handle — see ADR-012. Slice 1
// wires a stub that returns 503 for every RPC; slices 7-8 replace with a
// live socket-dialed client.
type server struct {
	objectStorage                    *objectstorage.Registry
	managedPostgres                  *managedpostgres.Service
	managedPostgresReconciler        *managedpostgres.Reconciler
	managedPostgresBindings          *managedpostgres.BindingService
	managedPostgresBindingReconciler *managedpostgres.BindingReconciler
	managedPostgresUsageCollector    *managedpostgres.UsageCollector
	store                            state.Store
	log                              *slog.Logger
	// devSourceCacheMu serializes reconstruction with best-effort cache
	// replacement. The cache is node-local and disposable; this lock is not
	// cross-node coordination and never guards customer-intent state.
	devSourceCacheMu sync.Mutex
	domain           string // apps base domain for URLs
	// cliAuthURLBase is the public web origin used by the CLI device-code
	// response. The public edge at this origin forwards /cli-auth to apid.
	cliAuthURLBase string
	notif          Notifier
	// invocationCompletion multiplexes the durable invocation_done
	// notification stream for synchronous invoke waiters. Nil keeps the
	// legacy per-request LISTEN path for tests and degraded boot.
	invocationCompletion *invocationCompletionWaiter
	// stripeWebhookSecret is the endpoint signing secret Stripe uses
	// for the v1 HMAC. Empty disables signature verification (dev mode).
	stripeWebhookSecret string
	// resendWebhookSecret is the Svix / Standard Webhooks signing
	// secret Resend stamps on bounce / complaint deliveries
	// (issue #246 acceptance item 8). Empty fails-closed: the
	// resendWebhook handler returns 503 so a missing env var
	// cannot silently accept unsigned events. Wired via
	// WithResendWebhookSecret from cmd/apid/main.go after
	// the sealed-env loader has resolved the value.
	resendWebhookSecret string
	// mailer emits the dunning + quota-warning emails. nil falls back
	// to the noop sender so callers never need to nil-check.
	mailer Mailer
	// mailBounce dispatches Resend bounce / complaint events into
	// the suppression + dunning pipeline (issue #246 acceptance
	// item 8). Wired via WithMailBounce from cmd/apid/main.go
	// after the state store + audit auditor are loaded; the
	// resendWebhook handler returns 500 if it's nil at request
	// time so a misconfiguration is loud rather than silent.
	mailBounce mailBounceHandler
	// githubd is apid's handle to the githubd daemon (ADR-012). Never nil:
	// slice 1 default is stubGithubdClient; slice 7 swaps for a live dial.
	githubd GithubdClient
	// gatewaydControlURL (ADR-093) is the loopback URL apid uses
	// to reach gatewayd-internal's control listener
	// (default http://127.0.0.1:9090). Only the /v1/internal/apps/{slug}/routes
	// endpoint is dialled today; the quota endpoint at
	// /v1/internal/quota has no apid-side caller yet so it's
	// still operator-curl-only. Empty disables the reverse-
	// proxy path; getAppRoutes surfaces
	// X-Faas-Routes-State: unavailable so the dashboard
	// distinguishes "gatewayd not reachable" from "no traffic
	// yet". Set via env FAAS_GATEWAYD_CONTROL_URL at boot.
	gatewaydControlURL string
	// events is the in-process broadcaster the SSE handlers read from
	// (slice 5/6). nil falls back to a fresh one so callers can defer
	// initialization in unit tests.
	events *events.Broadcaster
	// eventsPlatform is the pkg/events.Platform (issue #517 / PR-C /
	// ADR-064). When non-nil, the deploy handler emits
	// wake.deploy_failed on the events table for every
	// pre-build rejection (signature gate, image format, override
	// cap). nil opts out (pre-PR-C unit tests + dev mode that
	// doesn't want the events row side-effect). The same value
	// lives on the cmd/apid wiring site so a single
	// construction serves every consumer in this binary.
	eventsPlatform *events.Platform
	// specCache is the in-process LRU backing the
	// ?source=auto OpenAPI generation (ADR-126 / issue #975
	// item #2). The cache is keyed on (app_id, sha(doc),
	// sha(routes), sha(rules)) and flushed per-app on either
	// pg_notify channel — NotifyAppOpenAPIDocChanged (new)
	// and NotifyEdgeRuleChanged (existing). nil is safe —
	// the handler just bypasses the cache on every read and
	// never invalidates. Wired via WithSpecCache from
	// cmd/apid/main.go (production) or kept nil (unit tests).
	specCache *openapidiff.SpecCache
	// PR #1099 P2 redesign: force-park + force-cold-boot now
	// route through the operator_intents table + pg_notify
	// (migrations/00431). apid never imports pkg/scheddgrpc —
	// the apid-control-plane-only depguard rule
	// (.golangci.yml:41-58) is preserved. The handler flow is
	// INSERT operator_intents row → s.notif.Notify('operator_intent',
	// payload) → return 202 Accepted; schedd (the only writer
	// to instances) claims + dispatches via the
	// pkg/sched/operator_intent_subscriber.go drain.
	// sessions seals + verifies dashboard cookies. nil falls back to an
	// ephemeral manager (so the daemon still boots in dev with no
	// /etc/faas/secrets/session.key) — see cmd/apid/main.go.
	sessions *session.Manager
	// bindingKeyFn is the IAM-hardening-mega-PR (ADR-076) secret used
	// to derive the session binding-hash fingerprint. nil ⇒ binding
	// is not armed (the unix-socket / cli-auth code path), in which
	// case pkg/bindinghash.Compute returns "" and the middleware
	// cross-check is a no-op. Production wires a closure that returns
	// the same AEAD key bytes the sessions.Manager uses so a stolen
	// DB blob can't pre-compute binding hashes offline.
	bindingKeyFn bindinghash.KeyFunc
	// loginTTL is how long a magic-link stays valid. Default 15m.
	loginTTL time.Duration
	// dpaPath is the on-disk path of the DPA template served by
	// GET /v1/account/dpa (spec §17 G6). Default /etc/faas/dpa.md in
	// production; the dev fallback is docs/DPA.md relative to the
	// repo root (set from FAAS_DPA_PATH or left empty to disable).
	dpaPath string
	// apiAuthLimiter is the shared per-IP bucket every /v1/* route
	// draws from (spec §11 "10/min/IP" — the budget is per-IP across
	// the whole API surface, not per (IP, endpoint)). Nil falls back
	// to a fresh bucket in authLimited for unit tests; production
	// wires it in newServer.
	apiAuthLimiter *middleware.Limiter
	// dashboardAuthLimiter is the shared per-IP bucket for the
	// dashboard auth surface (/login, /auth/verify). Separate from
	// apiAuthLimiter because the two surfaces count different
	// statuses (apiAuthLimiter counts 401; dashboardAuthLimiter
	// counts every attempt on /login to defeat anti-enumeration).
	dashboardAuthLimiter *middleware.Limiter
	// cliAuthLimiter is the per-IP bucket for the anonymous
	// /v1/cli-auth/* endpoints (spec §2.2). Separate from
	// apiAuthLimiter + dashboardAuthLimiter so a brute-force on
	// codes cannot starve the bearer-token auth surface OR the
	// dashboard /login bucket.
	cliAuthLimiter *middleware.Limiter
	// cliAuthSubmitLimiter is the per-IP bucket for POST /cli-auth
	// (the dashboard-side claim form). Separate from
	// dashboardAuthLimiter so a customer retrying `faas login` from a
	// corporate NAT does not self-DoS the magic-link /login surface —
	// the two share the 10/min/IP budget otherwise. Eager init mirrors
	// apiAuthLimiter + dashboardAuthLimiter (test-only nil-fallbacks
	// live in the chain methods).
	cliAuthSubmitLimiter *middleware.Limiter
	// dashboardExportLimiter caps /dashboard/account/export at
	// 3/min/IP (PR #83 review #14). Pulling the export touches ≥7
	// PG queries + a JSON encode; a stuck-refresh tab at a customer
	// site can pin a CPU. AuthLimit's CountEveryAttempt sentinel
	// turns the existing per-IP failure limiter into a generic
	// per-IP rate limiter without dragging in x/time/rate.
	dashboardExportLimiter *middleware.Limiter
	// adminAllowlist is the email allowlist gating /v1/compute-nodes
	// (issue #98 / ADR-028). nil = no admin access (every route
	// 403s); populated by WithAdminAllowlist from FAAS_ADMIN_EMAILS.
	adminAllowlist *adminAllowlist
	// statusCache backs GET /status/slo.json (spec §12 public status
	// page). Wired in production via WithStatusCache; nil keeps the
	// route functional but degraded (returns source=empty payload).
	statusCache *statusCache
	// promqlClient is the Prometheus HTTP client shared by the
	// statusCache and the per-app metrics endpoint (issue #273 /
	// ADR-042). Owned here so the GET /v1/apps/{slug}/metrics handler
	// reuses the same tested transport; the client is nil-safe (the
	// handler falls back to a degraded zero-valued payload when
	// Prometheus isn't configured). See pkg/promql/client.go.
	promqlClient *promql.Client
	// statusPagePath is the on-disk path of the static HTML served
	// at GET /status. Empty uses /etc/faas/statuspage/index.html.
	statusPagePath string
	// billingPortalURL is the template URL carried on CodePayment
	// 402 responses from changePlan (issue #142). May contain the
	// substring `{account_id}` which the handler substitutes with
	// acct.ID at write time; an empty template (or one with no
	// placeholder) is returned as-is so the operator can ship a
	// domain-only URL until the customer's id is wired in. Empty
	// means the 402 still goes out but BillingPortalURL is omitted.
	billingPortalURL string
	// sbomRoot is the absolute filesystem directory imaged writes
	// build SBOMs to (imaged populates build_provenance.sbom_storage_key
	// with the relative path inside this root). Empty means the
	// GET /v1/builds/{id}/sbom route 503s (issue #299 / ADR-038 Phase 3
	// — the SBOM populator hadn't landed yet so the storage key is
	// empty for every pre-PR build).
	sbomRoot string
	// billingProvider is the per-deployment Provider apid's webhook
	// + changePlan handlers dispatch through. Wired via WithBillingProvider
	// from cmd/apid/main.go::LoadProviderForAPID. nil = "Stripe path
	// stays inline" (the apid Stripe webhook uses the legacy
	// stripe.VerifySignature check + the BillingPortalURL template, so
	// no *stripe.Client is needed at the apid level). The Paddle path
	// sets this to a *paddle.Provider at boot.
	billingProvider billing.Provider
	// ops holds the per-daemon Prometheus registry. Wired via
	// WithOpsMetrics so callers (cmd/apid) control the registry
	// lifecycle. A dedicated metric observer middleware sits atop
	// the route mux so every handler emits apid_ops_total +
	// apid_op_duration_seconds without each one wrapping itself.
	// Nil = observation disabled (unit tests).
	ops *wire.OpsMetrics
	// metricsDiscoveryMetrics is bound to the same per-daemon registry as ops.
	// It records producer-side health for the loopback Prometheus HTTP-SD
	// endpoints; nil keeps tests and degraded construction paths no-op.
	metricsDiscoveryMetrics *metricsDiscoveryMetrics
	// graceWindowCache (issue #189 / IAM-5) caches the per-account
	// rotation grace override (accounts.key_grace_window_days). The
	// bearer-key auth path does NOT read it (the lazy expiry gate
	// is in pkg/state.AuthenticateKey); only rotateKey reads it,
	// and a 60 s TTL bounds the admin-update propagation latency.
	// nil-safe: the rotate handler queries the store directly when
	// the cache is nil (the dev/test boot path).
	graceWindowCache *graceWindowCache
	// rekeyRunner is the background re-seal walker (ADR-089 PR-C).
	// Wired only when FAAS_REKEY_ENABLED=true AND mfaIdentities()
	// is non-empty; nil on daemons where either condition fails.
	// The HTTP handler reads Progress() through this field; a nil
	// value produces a 503 — see rekeyRunnerOptedIn below for the
	// distinction between "feature off" (rekey_disabled) and
	// "flag set but no identities loaded" (rekey_no_identities).
	rekeyRunner *Runner
	// rekeyRunnerOptedIn tracks whether FAAS_REKEY_ENABLED=true was
	// observed at boot, regardless of whether the runner was
	// actually wired (it's skipped when identities are empty —
	// cmd/apid/main.go:944). The handler pairs this with the
	// rekeyRunner nil-check to return the right 503 code, so an
	// operator who set the flag but forgot FAAS_HOST_AGE_IDENTITY_PATH
	// sees rekey_no_identities instead of the misleading
	// "set FAAS_REKEY_ENABLED and restart" detail.
	rekeyRunnerOptedIn bool
	// dataPlacementEnabled (ADR-098 PR-B / C4) is the per-PR
	// feature flag (FAAS_DATA_PLACEMENT=1) that gates the
	// data-upstream handler family
	// (GET/PUT/DELETE /v1/apps/{slug}/upstreams[...]).
	// Default false keeps the v1 byte-for-byte posture — the
	// PR-A pg_notify trigger still fires but no handler reads
	// from it. Wired via WithDataPlacement from
	// cmd/apid/main.go::dataPlacementEnabledFromEnv.
	dataPlacementEnabled bool
	// runtimeConfig is the durable operator configuration snapshot. It is
	// deliberately in-memory for request hot paths; the admin handler writes
	// Postgres and the notification reconciler refreshes this snapshot.
	runtimeConfig *runtimeConfigManager
	// hostHashFunc (issue #957) is the test seam for forcing
	// the env-classifier into the silent-skip branch. Nil in
	// production (cmd/apid/main.go does NOT call
	// WithHostHashFunc); runEnvClassifier falls through to
	// secretbox.HashHost. The seam exists only so the
	// handlers_env_classifier_audit_test.go can drive the
	// host_hash_failed audit emit without touching the
	// host_hash_salt on disk.
	hostHashFunc func(host string) (string, error)
	// audit is the IAM-4 (ADR-035) seam that auth-relevant handlers
	// call to record a security event. The seam wraps
	// state.Store.AppendEvent with best-effort failure semantics
	// (log Warn + apid_audit_write_failures_total counter, never
	// roll back the action). nil falls back to a no-op helper so
	// older tests can build a server without an audit handle.
	audit *auditor
	// authMw is the pkg/auth.Middleware that backs the
	// s.requireMFA + s.requireScope facade methods
	// (cmd/apid/auth_facade.go). Constructed in
	// newServerWithDeps from s.store + s.audit + s.log +
	// s.apiAuthLimiter. Nil is tolerated for unit tests that
	// don't exercise the auth chain (the facade is a wrapper
	// around nil-Middleware methods, which is fine — those
	// tests don't call s.requireMFA/Scope either).
	//
	// ADR-044.
	authMw *authmw.Middleware
	// oidcHandler is the POST /v1/auth/oidc/exchange handler
	// (issue #270 / ADR-101). Wired from newServerWithDeps so
	// the mutating handler state (the JWKS cache, the trust-policy
	// store, the audit emitter) is constructed once at boot. nil
	// means the OIDC route is not mounted — unit tests that don't
	// exercise the OIDC path can build a server without wiring it.
	// The handler's deps are constructed in newServerWithDeps
	// (cmd/apid/server.go:540+); the production main.go never
	// touches this field directly.
	oidcHandler *oidcHandler
	// orgResolver is the pkg/authz.OrgResolver the LoadOrg
	// middleware reads to resolve (slug, membership) for the
	// X-Active-Org / ?org= hint. Wraps the same pkg/state.Store
	// s.store already holds — see (*server).loadOrg. nil is
	// tolerated for unit tests that don't exercise LoadOrg
	// (cmd/apid/auth_facade.go's s.loadOrg is a no-op when
	// orgResolver is nil; the route table only mounts the
	// middleware when this is non-nil, so a nil pointer never
	// reaches the middleware at runtime).
	//
	// Issue #190 / IAM-6 / ADR-061, PR 4.
	orgResolver *authz.StoreBackedResolver
	// oauthConfig is the boot-resolved sign-in OAuth provider state
	// (issue #419 / ADR-046). Computed once in cmd/apid/main.go
	// via auth.LoadSignInConfigFromEnv, passed into newServerWithDeps,
	// and read by:
	//   - handlers_google.go / handlers_github.go to gate the
	//     consent redirect on a non-empty Enabled() value;
	//   - /v1/auth/capabilities to surface the per-provider bool;
	//   - the dashboard login template (templates/login.html) via
	//     Page.Auth to suppress buttons that would 503.
	// The zero value of SignInConfig (both providers Disabled)
	// is the in-test default so unit tests that don't pass an
	// explicit config behave as if the deploy operator never set
	// any OAuth env — the per-handler 503 path is then exercised
	// on every redirect.
	oauthConfig auth.SignInConfig
	// reconcileSvc is the apid-side workload mutation primitive
	// (PR-G, repo decomposition Phase 5). Built once per daemon
	// from store + audit (actor="apid" via audit.pkgAuditor()) so
	// every reconcile-driven audit row carries the same actor
	// the apid-side audit pipeline emits under. Both scan_service
	// (dry-run via Service.Plan) and applyProject (apply via
	// Service.Reconcile) route through this seam — there is no
	// other workload-mutation path post PR-G.
	reconcileSvc *reconcile.Service
}

// anonymousAccountLabel is the literal value of the `account_id`
// label that the per-request accounting chain assigns to unauthenticated
// requests (the 401 path and the rare cases where principalFrom(r)
// succeeds but p.Acct.ID is empty). The OpsMetrics admission set
// (`accountLabelSet`, pkg/wire/metrics.go) treats this as a real id
// for cardinality purposes: it counts against the 10 000 cap and
// surfaces in /metrics as a normal series — a regression where the
// sentinel is stringified differently across call sites would split
// the anonymous traffic across N series and silently inflate the
// admission set. Kept here (cmd/apid, not pkg/wire) because the
// server-side accounting paths are the only writers; pkg/wire only
// reads the value as a normal label.
//
// goconst: extracted because the three request-accounting call sites
// (requestTotal, requestFailures, observeTopTenantRPS) all open-code
// `acct := anonymousAccountLabel` and golangci-lint's goconst rule
// would otherwise flag the duplication.
const anonymousAccountLabel = "anonymous"

// WithOpsMetrics attaches the daemon-wide Prometheus registry. The
// handler-level observe call in observeHandler hits ops; the chain
// methods stay untouched. Mirrors pkg/builderd/builderd.go's
// WithOpsMetrics (PR #124, ADR-030) and pkg/githubd/server.go's
// WithOpsMetrics (this PR).
//
// Issue #286: also wires the failed-login counter surface on the
// auditor (auditor.SetFailedOps) and starts the async-batched audit
// flusher goroutine (auditor.Start). The flush context is the daemon's
// main cancel context — passed here so the flusher can drain on
// shutdown (and so SIGTERM/abort can short-circuit any in-flight
// AppendEvent calls via contextcheck-friendly ctx propagation
// through drainFlusher/flushOne).
func (s *server) WithOpsMetrics(ctx context.Context, ops *wire.OpsMetrics) *server {
	s.ops = ops
	if ops == nil {
		s.metricsDiscoveryMetrics = nil
	} else if s.metricsDiscoveryMetrics == nil || s.metricsDiscoveryMetrics.registry != ops.Registry() {
		s.metricsDiscoveryMetrics = newMetricsDiscoveryMetrics(ops.Registry(), ops.MetricPrefix())
	}
	// Re-bind the audit counter so the IAM-4 seam can record
	// failures. If ops is nil (unit tests that don't care about
	// metrics), leave the auditor with a nil ops interface so Emit
	// silently skips the .Inc().
	if s.audit != nil {
		s.audit.setOps(ops)
		// Issue #286: bind the failed-login counter surface and
		// start the flusher. The flush context is the daemon's
		// main context — Close() is the canonical shutdown path,
		// but the context cancellation is the safety net for
		// abnormal exits (panic, OOM kill, etc.).
		s.audit.SetFailedOps(ops)
		s.audit.Start(ctx)
	}
	return s
}

// WithStatusCache wires the status-page Prometheus query cache.
// Called from main after the config has loaded the Prometheus URL;
// the route handlers are mounted regardless so a misconfigured
// prometheus URL degrades the JSON to "no source" rather than 5xx.
//
// The Prometheus HTTP client is shared with the per-app metrics
// endpoint (issue #273 / ADR-042) so both consumers use the same
// tested transport. nil promURL keeps both consumers functional but
// degraded — statusCache returns "no source", the metrics handler
// returns zeroed fields with Source="degraded".
func (s *server) WithStatusCache(promURL, htmlPath string) *server {
	s.statusCache = newStatusCache(promURL, s.log)
	if promURL != "" {
		s.promqlClient = promql.NewClient(promURL, nil)
	}
	s.statusPagePath = htmlPath
	return s
}

// WithBillingPortalURL records the operator-controlled Stripe billing
// portal template used by the changePlan handler (issue #142). The
// template may contain `{account_id}`; the handler substitutes the
// authenticated account id at write time. Empty is allowed (the 402
// still goes out, just without the BillingPortalURL extension).
func (s *server) WithBillingPortalURL(template string) *server {
	s.billingPortalURL = template
	return s
}

// WithSBOMRoot records the absolute directory imaged writes build SBOMs
// to (default /srv/fc on a single-box deploy). Read-side handler joins
// build_provenance.sbom_storage_key against this root and streams the
// blob. Empty disables the route — apid returns 503 build_sbom_unavailable
// rather than 404 so the SDK can distinguish "no SBOM yet" from "no SBOM
// ever". Issue #299 / ADR-038 Phase 3.
func (s *server) WithSBOMRoot(root string) *server {
	s.sbomRoot = root
	return s
}

// WithBillingProvider attaches the per-deployment billing.Provider.
// Called from cmd/apid/main.go after LoadProviderForAPID. When non-nil
// the /v1/webhooks/paddle route is mounted and the changePlan 402 path
// dispatches through it; when nil the apid Stripe path stays inline
// (the legacy stripe.VerifySignature + BillingPortalURL template).
func (s *server) WithBillingProvider(p billing.Provider) *server {
	s.billingProvider = p
	return s
}

// WithResendWebhookSecret attaches the Svix / Standard Webhooks
// signing secret Resend uses for bounce / complaint / delivery
// events (issue #246 acceptance item 8). When empty the
// resendWebhook handler fails closed with 503 — an unsigned
// delivery cannot bypass the suppression + dunning pipeline.
// cmd/apid/main.go resolves this from FAAS_MAIL_RESEND_WEBHOOK_SECRET.
func (s *server) WithResendWebhookSecret(secret string) *server {
	s.resendWebhookSecret = secret
	return s
}

// WithMailBounce attaches the bounce handler the resendWebhook
// route dispatches into (issue #246 acceptance item 8). In
// production this is the meterd-owned *meter.BounceHandler;
// tests inject a fake via the mailBounceHandler interface so
// the route can be exercised without standing up a full
// state.Store + audit auditor.
func (s *server) WithMailBounce(h mailBounceHandler) *server {
	s.mailBounce = h
	return s
}

// WithRekeyRunner attaches the background re-seal walker
// (ADR-089 PR-C). nil is the documented default — the
// /v1/admin/secrets/rekey-progress handler returns 503
// code="rekey_disabled" when this field is nil. The setter
// returns *server so production wiring can chain
//
//	srv := newServer(...).WithRekeyRunner(runner)
//
// matching the WithOpsMetrics / WithBillingProvider style.
func (s *server) WithRekeyRunner(r *Runner) *server {
	s.rekeyRunner = r
	return s
}

// MarkRekeyRunnerOptedIn records that FAAS_REKEY_ENABLED=true was
// observed at boot, even if the runner itself was skipped (e.g.
// because mfaIdentities() was empty). Paired with WithRekeyRunner,
// the HTTP handler returns the right 503 code:
//
//   - runner nil + optedIn=false → rekey_disabled
//   - runner nil + optedIn=true  → rekey_no_identities
//   - runner set                 → 200 + progress
//
// Wired from cmd/apid/main.go at boot, BEFORE WithRekeyRunner so
// the field is correct even if NewRunner fails.
func (s *server) MarkRekeyRunnerOptedIn() *server {
	s.rekeyRunnerOptedIn = true
	return s
}

// WithOAuthConfig attaches the sign-in OAuth provider state
// resolved at boot via auth.LoadSignInConfigFromEnv (issue #419 /
// ADR-046). Distinct from newServerWithDeps's positional args so
// the dozens of existing test call sites keep compiling — the
// setter lives after construction. Production wiring in
// cmd/apid/main.go::runWithDeps:
//
//	oauthCfg, err := auth.LoadSignInConfigFromEnv(deps.getenv)
//	if err != nil { return fmt.Errorf("...: %w", err) }
//	srv := newServer(...).WithOAuthConfig(oauthCfg).
//	    WithOpsMetrics(...).WithBillingProvider(...)
//
// Tests that don't care about OAuth pass nothing and read the
// zero value (both providers Disabled → consent routes 503).
func (s *server) WithOAuthConfig(cfg auth.SignInConfig) *server {
	s.oauthConfig = cfg
	return s
}

// WithCLIAuthURLBase attaches the public web origin used in browser URLs
// returned by the CLI device-code endpoint. The setter keeps existing
// positional server constructors source-compatible while allowing production
// config to supply a provider-specific console hostname.
func (s *server) WithCLIAuthURLBase(base string) *server {
	s.cliAuthURLBase = normalizeCLIAuthURLBase(base)
	return s
}

// WithEventsPlatform attaches the pkg/events.Platform
// (issue #517 / PR-C / ADR-064). When non-nil, the deploy
// handler emits wake.deploy_failed on the events table for
// every pre-build rejection (signature gate, image format,
// override cap, missing slug). nil opts out — pre-PR-C
// fixtures + the unit-test default. Mirrors the WithEvents
// setter pattern on pkg/sched.Engine / pkg/fcvm.VMM /
// pkg/builderd.Builderd.
func (s *server) WithEventsPlatform(p *events.Platform) *server {
	s.eventsPlatform = p
	return s
}

// WithDataPlacement (ADR-098 PR-B / C4) attaches the per-PR
// feature flag (FAAS_DATA_PLACEMENT=1) that gates the
// data-upstream handler family. Default false preserves the
// pre-PR-B byte-for-byte posture. Wired once at boot from
// cmd/apid/main.go::dataPlacementEnabledFromEnv. Mirrors the
// WithBillingProvider / WithOpsMetrics chain-method style so
// existing positional call sites in tests don't need editing.
func (s *server) WithDataPlacement(enabled bool) *server {
	s.dataPlacementEnabled = enabled
	if s.runtimeConfig == nil {
		s.runtimeConfig = newRuntimeConfigManager(nil)
	}
	_ = s.runtimeConfig.apply(runtimeConfigDataPlacement, boolJSON(enabled))
	return s
}

// WithRuntimeConfigManager replaces the default environment-seeded manager
// with the production manager using the caller's environment seam. The
// setter keeps the existing test constructors source-compatible while making
// runtime configuration injectable in boot tests.
func (s *server) WithRuntimeConfigManager(manager *runtimeConfigManager) *server {
	if manager != nil {
		s.runtimeConfig = manager
		// Apply the bootstrap HSTS value through the same side-effect path
		// used by a later hot update. This keeps the environment fallback
		// and the durable operator override on one source of truth.
		_ = s.runtimeConfig.apply(runtimeConfigHSTS, s.runtimeConfig.Value(runtimeConfigHSTS))
	}
	return s
}

func (s *server) runtimeBool(key string, fallback bool) bool {
	if s.runtimeConfig == nil {
		return fallback
	}
	return s.runtimeConfig.Bool(key, fallback)
}

func (s *server) runtimeInt(key string, fallback int) int {
	if s.runtimeConfig == nil {
		return fallback
	}
	return s.runtimeConfig.Int(key, fallback)
}

func boolJSON(value bool) []byte {
	if value {
		return []byte("true")
	}
	return []byte("false")
}

// WithHostHashFunc (issue #957) attaches a stub for the env-
// classifier's host-hash seam. Production wiring does NOT call
// this method — s.hostHashFunc stays nil and runEnvClassifier
// falls through to the canonical secretbox.HashHost (cmd/apid/
// handlers_env.go). The seam exists so handlers_env_classifier
// _audit_test.go can force the silent-skip branch
// (host_hash_failed) and assert that env.classifier_failed
// fires, without touching /etc/faas/host_hash_salt on disk.
func (s *server) WithHostHashFunc(fn func(host string) (string, error)) *server {
	s.hostHashFunc = fn
	return s
}

// WithGatewaydControlURL (ADR-093) attaches the loopback URL
// apid uses to reach gatewayd-internal's control listener
// (/v1/internal/apps/{slug}/routes). Default
// http://127.0.0.1:9090 matches gatewayd-internal's default
// control bind (see pkg/gateway/control.go ControlAddr);
// production overrides via FAAS_GATEWAYD_CONTROL_URL when the
// daemons are split across nodes (cross-box deployments will
// need the public-facing reverse-proxy to terminate mTLS before
// reaching gatewayd-internal's control mux — out of scope for
// this PR; same-box is the only supported posture today).
func (s *server) WithGatewaydControlURL(url string) *server {
	s.gatewaydControlURL = url
	return s
}

// WithInvocationCompletionWaiter attaches the process-wide completion
// fan-out used by synchronous invocation handlers. The setter preserves the
// existing server construction seams while allowing production boot to make
// the optimization optional and fail-safe.
func (s *server) WithInvocationCompletionWaiter(w *invocationCompletionWaiter) *server {
	s.invocationCompletion = w
	return s
}

// WithSpecCache attaches the in-process LRU backing the
// ?source=auto OpenAPI generation (ADR-126 / issue #975
// item #2). Production wires a *openapidiff.SpecCache from
// cmd/apid/main.go via openapidiff.NewSpecCache(); tests
// typically leave the field nil so the handler bypasses the
// cache. The subscriber (runOpenAPIDocSubscriber) flushes
// the cache per-app on either pg_notify channel —
// NotifyAppOpenAPIDocChanged and NotifyEdgeRuleChanged — so
// the entry is consistent with the on-disk doc + the
// platform-enforced rules without an explicit per-request
// freshness check.
func (s *server) WithSpecCache(cache *openapidiff.SpecCache) *server {
	s.specCache = cache
	return s
}

// billingPortalURLFor returns the rendered URL for the customer. Empty
// template yields empty string so the handler can omitempty the field.
func (s *server) billingPortalURLFor(acct state.Account) string {
	if s.billingPortalURL == "" {
		return ""
	}
	return strings.ReplaceAll(s.billingPortalURL, "{account_id}", acct.ID)
}

// billingPortalURLForProvider returns a provider-authenticated portal session when the
// active provider supports it, falling back to the operator-configured URL
// used by the legacy Stripe path. The short timeout keeps plan-change and
// billing reads from hanging on a provider outage.
func (s *server) billingPortalURLForProvider(ctx context.Context, acct state.Account) string {
	if acct.ProviderCustomerID != "" {
		if provider, ok := s.billingProvider.(billing.CustomerPortalProvider); ok {
			portalCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if portalURL, err := provider.CreateCustomerPortalSession(portalCtx, acct, ""); err == nil && portalURL != "" {
				return portalURL
			} else if err != nil {
				s.log.Warn("billing portal session unavailable", "account", acct.ID, "err", err)
			}
		}
	}
	return s.billingPortalURLFor(acct)
}

// Mailer is the slice of pkg/mail.Sender apid depends on. Kept as an
// interface so tests inject a recording stub without importing pkg/mail.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Message is the cross-component email payload — mirrors pkg/mail.Message
// without the import cycle (apid stays free of pkg/mail so daemons that
// link apid don't pull the mail deps).
//
// Headers carries RFC 8058 List-Unsubscribe etc. on the
// quota-warning template (issue #246 acceptance item 4). The
// mailAdapter in cmd/apid/main.go copies them into mail.Message
// so the transport actually reaches the wire — without this the
// bulk-sender compliance work would be silently dropped at the
// adapter boundary.
//
// MessageID, when non-empty, becomes the Idempotency-Key
// (Resend) / X-Idempotency-Key (Postmark) header so a retry that
// the upstream already accepted is deduplicated inside the
// provider's replay window instead of double-charging. The
// dunning + quota-warning templates derive a stable id from
// (account_id, template, day).
type Message struct {
	To        []string
	Subject   string
	TextBody  string
	HTMLBody  string
	Headers   map[string]string
	MessageID string
}

// Notifier is the slice of pgstore behaviour apid depends on. The production
// server uses a db-backed Notifier; tests inject a no-op so they don't need a
// running Postgres.
//
// Subscribe is added in M7.5 slice 6 to wire the SSE /v1/events
// endpoint. It hands back a buffered channel of db.Notification for the
// requested channels, plus a cancel func. The noop notifier returns an
// empty stream that closes immediately.
//
// WaitFor is the Move 2 long-poll sibling used by the event-driven
// handlers (sync invoke, queueReceive). The production notifier
// delegates to db.WaitForNotification; the test noop returns
// db.ErrWaitTimeout after the timeout so test fixtures stay
// pgx-free.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
	Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
	WaitFor(ctx context.Context, channel string, predicate func(payload string) bool, timeout time.Duration) (string, error)
}

func newServer(store state.Store, log *slog.Logger, domain string, notif Notifier) *server {
	return newServerWithDeps(store, log, domain, notif, "", nil, nil, nil, nil, 0, "")
}

// newServerWithDeps wires the full server surface including the M7
// stripe-webhook + mailer deps, the M7.5 githubd client (ADR-012),
// the dashboard session manager + login-token TTL, and the G6 DPA
// template path.
//
// Production (cmd/apid/main.go) calls this with env-loaded values;
// tests use the simpler newServer (no secret, noop mailer, stub
// githubd, nil sessions → ephemeral key, default 15m login TTL).
// The sign-in OAuth config (issue #419 / ADR-046) is wired
// separately via (*server).WithOAuthConfig after construction —
// same pattern as WithBillingProvider, WithOpsMetrics, etc. so the
// existing positional call sites in tests don't need editing.
func newServerWithDeps(
	store state.Store,
	log *slog.Logger,
	domain string,
	notif Notifier,
	stripeSecret string,
	mailer Mailer,
	githubd GithubdClient,
	sessions *session.Manager,
	bcaster *events.Broadcaster,
	loginTTL time.Duration,
	dpaPath string,
) *server {
	if domain == "" {
		domain = domainUnset
	}
	if notif == nil {
		notif = noopNotifier{}
	}
	if mailer == nil {
		mailer = noopMailer{}
	}
	if githubd == nil {
		githubd = stubGithubdClient{}
	}
	if sessions == nil {
		sessions, _ = session.NewEphemeralManager(7 * 24 * time.Hour)
	}
	if bcaster == nil {
		bcaster = events.New()
	}
	if loginTTL <= 0 {
		loginTTL = 15 * time.Minute
	}
	// Shared per-IP auth-failure bucket across every /v1/* route. Spec §11
	// "10/min/IP" is per-IP across the entire API surface, not per
	// (IP, endpoint) — a fresh limiter per route would let a brute-force
	// attack hit 10 attempts × N endpoints × 1 min and never trip any
	// single bucket. The Limiter is per-process; a restart resets it
	// (acceptable — gatewayd-public is the primary edge counter).
	apiAuthLimiter := middleware.NewLimiter(middleware.AuthLimitConfig{Log: log})
	// Dashboard auth surface (/login, /auth/verify) gets its own shared
	// bucket so the CountEveryAttempt sentinel on /login doesn't bleed
	// 200s into the API's 401-counter.
	dashboardAuthLimiter := middleware.NewLimiter(middleware.AuthLimitConfig{Log: log})
	// CLI auth surface (spec §2.2). Two buckets:
	//   * cliAuthLimiter      — anonymous /v1/cli-auth/* (mint + exchange)
	//   * cliAuthSubmitLimiter — POST /cli-auth from the dashboard form
	// The submit bucket is separate from dashboardAuthLimiter so a
	// customer retrying `faas login` from a shared NAT doesn't burn the
	// 10/min/IP budget the magic-link /login uses (review finding S2;
	// the same user can hit both surfaces).
	cliAuthLimiter := middleware.NewLimiter(middleware.AuthLimitConfig{Log: log})
	cliAuthSubmitLimiter := middleware.NewLimiter(middleware.AuthLimitConfig{Log: log})
	// PR #83 review #14: /dashboard/account/export is expensive
	// (≥7 PG queries + JSON encode) and lives behind sessionAuth,
	// not /v1/* auth — so it needs its own per-IP bucket separate
	// from the dashboard auth bucket. 3 / minute / IP is generous
	// for a human customer clicking "Download"; high enough that
	// one legitimate refresh + retry isn't 429'd.
	dashboardExportLimiter := middleware.NewLimiter(middleware.AuthLimitConfig{
		Log:           log,
		Window:        time.Minute,
		MaxFailures:   3,
		CountStatuses: []int{middleware.CountEveryAttempt},
	})
	// IAM-4 (ADR-035): the auth audit seam. Wired here so handlers
	// can call s.audit.Emit(...) without a per-request nil check.
	// ops is passed in via WithOpsMetrics after this returns; until
	// then audit.ops is nil and Emit silently skips the counter
	// increment (the production caller always sets ops before
	// serving traffic; tests can call WithOpsMetrics to opt in).
	//
	// The same auditor is the source of the inner *audit.Auditor
	// that powers reconcileSvc (PR-G, repo decomposition Phase 5).
	// Constructing a temporary here lets the field initializer below
	// pass the inner Auditor into buildReconcileService — the
	// resulting reconcile rows carry actor="apid" in events.actor.
	aud := newAuditor(store, log, nil)
	s := &server{
		store:                  store,
		log:                    log,
		domain:                 domain,
		cliAuthURLBase:         defaultCLIAuthURLBase,
		notif:                  notif,
		stripeWebhookSecret:    stripeSecret,
		mailer:                 mailer,
		githubd:                githubd,
		events:                 bcaster,
		bindingKeyFn:           func() []byte { return sessions.BindingKey() },
		sessions:               sessions,
		loginTTL:               loginTTL,
		dpaPath:                dpaPath,
		apiAuthLimiter:         apiAuthLimiter,
		dashboardAuthLimiter:   dashboardAuthLimiter,
		cliAuthLimiter:         cliAuthLimiter,
		cliAuthSubmitLimiter:   cliAuthSubmitLimiter,
		dashboardExportLimiter: dashboardExportLimiter,
		audit:                  aud,
		runtimeConfig:          newRuntimeConfigManager(nil),
		// pkg/auth.Middleware backs the s.requireMFA + s.requireScope
		// facade (cmd/apid/auth_facade.go). The auditor's Emit is
		// nil-safe so the auth.mfa_gate_hit audit row fires when the
		// auditor is wired; nil in tests disables the row. The Limiter
		// is the shared apiAuthLimiter that backs s.authLimited (the
		// API surface-level per-IP bucket per spec §11 10/min/IP).
		// ADR-044.
		authMw: authmw.New(
			storeAsAuthenticator(store),
			sessions,
			storeAsSessionLookup(store),
			auditorAsAuthAuditor(newAuditor(store, log, nil)),
			log,
			apiAuthLimiter,
			// IAM-hardening-mega-PR (ADR-076): hand the auth
			// middleware the same 32-byte key bytes the session
			// AEAD uses so the binding-hash cross-check keys off
			// the host secret. Closure pulls a fresh copy on each
			// miss (the AEAD-owned key never escapes session.Manager
			// past the constructor; we read via Manager.BindingKey).
			func() []byte { return sessions.BindingKey() },
		),
		// Issue #190 / IAM-6 / ADR-061, PR 4: org resolver backs
		// s.loadOrg (cmd/apid/auth_facade.go). Wraps the same
		// pkg/state.Store the rest of the daemon uses; PR 7 may
		// add a small LRU cache here when admission pressure
		// makes the per-request SELECT hot.
		//
		// nil when store is nil — a handful of legacy tests
		// (TestStatusJSONHandlerNoPrometheusURL and friends)
		// construct a server with no store to exercise degraded
		// paths. s.loadOrg treats a nil resolver as pass-through
		// (cmd/apid/auth_facade.go::loadOrg), so LoadOrg is
		// inert for those tests — no DB call attempted.
		orgResolver: maybeStoreBackedResolver(store),
		// oauthConfig (issue #419 / ADR-046) is left at the
		// zero value here (both providers Disabled); production
		// wires the env-resolved config via (*server).WithOAuthConfig
		// from cmd/apid/main.go, mirroring the With* setter pattern
		// used by WithBillingProvider / WithOpsMetrics so existing
		// positional callers in tests don't need editing.
		//
		// reconcileSvc (PR-G, repo decomposition Phase 5) shares
		// the apid-side *audit.Auditor constructed above. The same
		// store powers both apid audit rows and reconcile audit
		// rows, and they carry the same actor="apid" so dashboards
		// can group by source. Pre-PR-G callers used
		// store.ApplyProjectPlan directly; post-PR-G every
		// workload-mutating path goes through Service.Reconcile.
		reconcileSvc: buildReconcileService(store, aud.pkgAuditor(), log),
		// IAM-5 (issue #189): rotation grace-window cache. 60 s
		// TTL bounds the admin-update propagation latency; the
		// invalidate-on-write path closes the loop. nil in
		// legacy tests that pre-date the cache — the rotateKey
		// handler falls back to the store directly.
		graceWindowCache: newGraceWindowCache(),
	}
	// Issue #270 / ADR-101 / PR-A: build the OIDC exchange
	// handler after the struct is wired so the handler can read
	// the apiAuthLimiter / audit / store / log / sessions
	// already on s. The handler is anonymous (no session
	// required); the route is mounted via middleware.AuthLimit
	// (no requireX chain) in cmd/apid/server.go:870 below.
	s.oidcHandler = s.buildOIDCHandler()
	// ADR-120: wire the package-level appsDomainFunc seam (declared at
	// cmd/apid/dns_probes.go:125) to the server's apps base domain so
	// checkPointsToGregale has the configured Gregale apex in
	// production. Tests inject a constant via the same var (see
	// cmd/apid/dns_probes_test.go:24-38); that assignment is reverted
	// in t.Cleanup, so the production wire below wins on every real
	// daemon start (the var is reset to the server's domain, not the
	// empty default).
	appsDomainFunc = func() string { return s.domain }
	return s
}

// noopMailer drops every email. Default when the daemon hasn't wired a
// real transport (gap G4 — the M7 PR uses this everywhere).
type noopMailer struct{}

func (noopMailer) Send(_ context.Context, _ Message) error { return nil }

// noopNotifier is the test/dev default; production wires pkg/db.Notify.
type noopNotifier struct{}

func (noopNotifier) Notify(_ context.Context, _, _ string) error { return nil }

// Subscribe returns a closed channel immediately. The noop notifier
// is the test/dev default; the SSE handler sees an EOF right away
// and exits cleanly.
func (noopNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

// WaitFor is the Move 2 long-poll sibling for the noop notifier. Returns
// db.ErrWaitTimeout after the timeout so test fixtures can exercise
// the "no event during the wait" path without a real Postgres. The
// SetWaitForPayload hook (cmd/apid/handlers_ext.go) lets tests
// pre-queue a successful payload before the handler lands.
func (noopNotifier) WaitFor(_ context.Context, _ string, _ func(payload string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

// handler builds the full Appendix A route table (Go 1.22 method+wildcard).
// New routes append here; do not introduce per-feature sub-muxes.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/apps/{slug}/buckets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageListSurface...)(s.listBuckets))))
	mux.HandleFunc("POST /v1/apps/{slug}/buckets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.createBucket))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/buckets/{bucket}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.deleteBucket))))
	mux.HandleFunc("GET /v1/apps/{slug}/buckets/{bucket}/access-grants", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.listBucketAccessGrants))))
	mux.HandleFunc("PUT /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.setBucketAccessGrant))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageManageSurface...)(s.deleteBucketAccessGrant))))
	mux.HandleFunc("GET /v1/apps/{slug}/buckets/{bucket}/objects", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageReadSurface...)(s.listBucketObjects))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/buckets/{bucket}/objects", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.deleteBucketObject))))
	mux.HandleFunc("POST /v1/apps/{slug}/buckets/{bucket}/signed-url", s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin, api.ScopeStorageRead, api.ScopeStorageWrite)(s.signBucketObject))))
	mux.HandleFunc("GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.listObjectMultipartUploads))))
	mux.HandleFunc("POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.createObjectMultipartUpload))))
	mux.HandleFunc("GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.getObjectMultipartUpload))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.abortObjectMultipartUpload))))
	mux.HandleFunc("GET /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.listObjectMultipartParts))))
	mux.HandleFunc("POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts/{part}/signed-url", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.signObjectMultipartPart))))
	mux.HandleFunc("POST /v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/complete", s.authLimited(s.requireMFA(s.requireScope(api.ScopesStorageWriteSurface...)(s.completeObjectMultipartUpload))))
	// Account. The /v1/account/plan change is destructive across the
	// whole account, so it requires the admin scope; the read-only
	// /v1/account carries the method default (read or admin).
	mux.HandleFunc("GET /v1/account", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.whoami))))
	mux.HandleFunc("GET /v1/account/object-storage-usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.getObjectStorageUsage))))
	mux.HandleFunc("POST /v1/admin/object-storage/usage-reports", s.authLimited(s.requireAdminMutation(s.recordObjectStorageUsage)))
	// IAM-6 (issue #190 / ADR-061, PR 4): active-org whoami. The
	// route is undocumented in api/openapi.yaml for PR 4 — PR 5
	// adds the spec coverage + the rest of the /v1/orgs/{slug}/...
	// surface. Loads the org via s.loadOrg (the pkg/authz middleware
	// that resolves X-Active-Org / ?org=) and returns the membership
	// role. No header → {"org": null} (passthrough, pre-PR-5 routes
	// stay account-scoped).
	mux.HandleFunc("GET /v1/orgs/me", s.auth(s.loadOrg(s.whoamiActiveOrg)))

	// Orgs (ADR-061 / IAM-6 / issue #190, PR 5 + PR 7). Customer-
	// visible org CRUD + member management + invitations + ownership
	// transfer + seat-usage visibility. The full surface is mounted
	// here in one block so the route table reads top-down by resource.
	// Patterns:
	//   - account-scoped (no s.loadOrg): GET/POST /v1/orgs
	//   - invitation peek/accept (no s.loadOrg, no scope):
	//     GET /v1/invitations/{token}, POST /v1/invitations/{token}/accept
	//   - org-scoped (s.loadOrg mounted inside scope wrapper):
	//     GET/PATCH/DELETE /v1/orgs/{slug}, /v1/orgs/{slug}/members[/...],
	//     /v1/orgs/{slug}/transfer_ownership,
	//     /v1/orgs/{slug}/invitations/{token} (revoke),
	//     /v1/orgs/{slug}/seat_usage (PR 7 visibility-only)
	// PR 9 ships the per-seat billing cut-over (pricing + Stripe
	// subscription-item quantities) per ADR-061 §"Out of scope";
	// PR 8 ships SSO + invitation-accept step-up.
	mux.HandleFunc("GET /v1/orgs", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listOrgsForCaller)))
	mux.HandleFunc("POST /v1/orgs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createSharedOrg)))))
	mux.HandleFunc("GET /v1/orgs/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.loadOrg(s.getOrg)))))
	mux.HandleFunc("PATCH /v1/orgs/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.patchOrg)))))
	mux.HandleFunc("DELETE /v1/orgs/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.softDeleteOrg)))))
	mux.HandleFunc("GET /v1/orgs/{slug}/members", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.loadOrg(s.listOrgMembers)))))
	mux.HandleFunc("POST /v1/orgs/{slug}/members", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.inviteOrgMember)))))
	mux.HandleFunc("PATCH /v1/orgs/{slug}/members/{user_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.changeOrgMemberRole)))))
	mux.HandleFunc("DELETE /v1/orgs/{slug}/members/{user_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.removeOrgMember)))))
	mux.HandleFunc("POST /v1/orgs/{slug}/transfer_ownership", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.requireStepUp(5*time.Minute)(s.transferOrgOwnership))))))
	mux.HandleFunc("GET /v1/invitations/{token}", s.authLimited(s.peekInvitation))
	// PR-8 + PR-9 §4: accept-invitation sits behind requireStepUpStrict
	// (5m). PR-9 flips the bearer-key branch from "bypass" to
	// "reject" — a leaked invitation token alone is sufficient to
	// grant org membership, so the bearer path is no longer
	// step-up-equivalent. The cookie path keeps its original
	// reject-on-missing-stamp behavior. The other 8 requireStepUp
	// mounts (server.go:655, 671, 700, 892, ...) keep the v1
	// bypass until each route's threat model is re-audited.
	// Compose order mirrors POST /v1/orgs/{slug}/transfer_ownership
	// (server.go:655): authLimited → requireMFA → requireStepUpStrict.
	mux.HandleFunc("POST /v1/invitations/{token}/accept", s.authLimited(s.requireMFA(s.requireStepUpStrict(5*time.Minute)(s.acceptInvitation))))
	mux.HandleFunc("DELETE /v1/orgs/{slug}/invitations/{token}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.loadOrg(s.revokeInvitation)))))
	// PR-8 §2: list invitations surface (cursor-paginated, every role).
	mux.HandleFunc("GET /v1/orgs/{slug}/invitations", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.loadOrg(s.listOrgInvitations)))))
	mux.HandleFunc("GET /v1/orgs/{slug}/seat_usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.loadOrg(s.getOrgSeatUsage)))))
	mux.HandleFunc("PATCH /v1/account/plan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.requireVerifiedEmail(s.requireStepUp(5*time.Minute)(s.idempotent(s.changePlan)))))))
	// Issue #561 — spend cap pause-workload. Account-self-scoped
	// (mirror restoreAccount: any authenticated principal on the
	// account may set / clear the cap). MFA gate is intentional —
	// a hostile actor with a stolen API key should not be able to
	// silently disable the cap. idempotent() wrap matches rotateKey
	// and changePlan — the OpenAPI spec advertises Idempotency-Key
	// and a retry without one would emit two overage.cap_changed
	// audit rows for the same logical operation (review finding #9).
	mux.HandleFunc("POST /v1/account/overage-cap", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.raiseOverageCap)))))
	// IAM-5 (issue #189): per-account rotation grace-window
	// override. Admin-only because the rotation primitive is
	// admin-only (POST /v1/keys/{id}/rotate mirrors the same
	// scope). GET surfaces the current value (with the plan
	// default alongside so the dashboard can render "Override: 7
	// days / Plan default: 7 days"); PATCH writes the override
	// and invalidates the in-process cache so the next rotation
	// sees the new value.
	mux.HandleFunc("GET /v1/account/keys/grace_window_days", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.getGraceWindow))))
	mux.HandleFunc("PATCH /v1/account/keys/grace_window_days", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.setGraceWindow))))
	// Per-account additive budget on top of the plan's
	// apps.egress_allowlist cap (issue #679 / PR-B / ADR-082).
	// The override is admin-settable only; the customer-facing
	// validator reads the value but has no API surface to write it.
	// Mirrors the grace-window chain shape (admin + MFA + no
	// additional step-up since the cost surface is small — a typo
	// reverts by setting extra=0).
	mux.HandleFunc("GET /v1/account/egress_allowlist_extra", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.getAccountEgressAllowlistExtra))))
	mux.HandleFunc("PATCH /v1/account/egress_allowlist_extra", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.setAccountEgressAllowlistExtra))))
	// G6 account self-service (spec §17 G6, ADR-021). /v1/account/dpa
	// is intentionally mounted without s.auth — the DPA is a public
	// artefact a prospect reads before signing up. The export + delete
	// + restore paths sit behind s.auth but pass the
	// deleted_pending carve-out in isAccountScopedPath so a customer
	// can take a final export or cancel during the 30-day grace.
	// DELETE /v1/account is admin-only — losing the account is
	// irreversible.
	mux.HandleFunc("GET /v1/account/export", s.auth(s.requireScope(api.ScopesReadSurface...)(s.exportAccount)))
	mux.HandleFunc("DELETE /v1/account", s.auth(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.requireStepUp(5*time.Minute)(s.idempotent(s.deleteAccount))))))
	mux.HandleFunc("POST /v1/account/restore", s.auth(s.requireScope(api.ScopesDeployWriteSurface...)(s.restoreAccount)))
	mux.HandleFunc("GET /v1/account/dpa", s.dpaTemplate)

	// IAM-3 server-side session revocation (ADR-039, issue #187
	// + #244 merged). All four routes sit behind s.auth but
	// WITHOUT authLimited — session management is rare and
	// counting 401s would only alarm on a customer who typed
	// their password wrong twice. The MFA allowlist (see
	// mfa_middleware.go) covers these routes, so a customer
	// whose session is mfa_pending can still list and revoke
	// devices (a "lock everything else down" panic path). The
	// wildcard DELETE /v1/auth/sessions/{id} is matched by the
	// prefix-check seam in isMFAAllowlisted.
	mux.HandleFunc("POST /v1/auth/logout", s.auth(s.requireMFA(s.logout)))
	mux.HandleFunc("GET /v1/auth/sessions", s.auth(s.requireMFA(s.listSessions)))
	mux.HandleFunc("DELETE /v1/auth/sessions/{id}", s.auth(s.requireMFA(s.revokeSession)))
	mux.HandleFunc("POST /v1/auth/sessions/revoke_all", s.auth(s.requireMFA(s.revokeAllSessions)))
	mux.HandleFunc("GET /v1/auth/csrf", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.issueCSRFToken))))

	// Apps.
	// Issue #1219: Prometheus refreshes compute gateway targets from the
	// active control-plane registry. This internal route is loopback-only;
	// gatewayd-internal rejects the same path before its public /v1 proxy.
	mux.HandleFunc("GET /v1/internal/metrics/targets", s.computeMetricsDiscovery)
	// Issue #274: Promtail metrics are discovered from the same active
	// compute-node registry, but use a separate HTTP-SD endpoint so the
	// gateway and shipper jobs never scrape each other's ports.
	mux.HandleFunc("GET /v1/internal/metrics/promtail-targets", s.promtailMetricsDiscovery)
	mux.HandleFunc("GET /v1/apps", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listApps))))
	mux.HandleFunc("POST /v1/apps", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.createApp))))))
	// `gregale dev`: one stable, expiring preview app per account/project.
	// Source bytes still flow through the normal deployment endpoints; these
	// routes only own the developer-session lease.
	mux.HandleFunc("PUT /v1/dev/sessions/{project}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.upsertDevSession))))
	mux.HandleFunc("DELETE /v1/dev/sessions/{project}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.destroyDevSession))))
	mux.HandleFunc("GET /v1/apps/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getApp))))
	// Issue #273 / ADR-042 — per-app metrics endpoint. Read-only,
	// no MFA required (the primary caller is an API key with
	// ScopesReadSurface). Mirrors getApp's IDOR-safe loadApp so a
	// cross-account slug is a 404, not a 200 with another tenant's
	// data.
	mux.HandleFunc("GET /v1/apps/{slug}/metrics", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppMetrics)))
	// Per-app dashboard JSON mirror — wire-friendly emission of the
	// same shape the dashboard HTML page renders (cmd/apid/
	// handlers_dashboard.go:2548 renderAppWakeTimeline). Auth chain
	// matches /v1/apps/{slug}/metrics (read-only, no MFA, primary
	// caller is an API key with ScopesReadSurface). Plan-gated
	// Hobby+ via handlers_app_wake_timeline_json.go::getAppWakeTimeline.
	// Distinct from the per-wake-id endpoint at /v1/apps/{slug}/wakes/
	// {wake_id}/timeline below — that's issue #517 / PR-C, this is
	// the per-app rollup mirror.
	mux.HandleFunc("GET /v1/apps/{slug}/wake-timeline", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppWakeTimeline)))
	// Per-app billing usage summary (commit 4 of the per-app
	// observability PR series). Same auth chain as the other
	// Hobby+-gated surfaces (read-only, no MFA, IDOR-safe via
	// loadApp). Plan-gated Hobby+ via AppUsageSummaryAllowed —
	// same 402 contract as /metrics and /wake-timeline.
	mux.HandleFunc("GET /v1/apps/{slug}/usage", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppUsage)))
	// ADR-093: per-route observability reader. Same auth chain
	// as /v1/apps/{slug}/metrics (read-only, no MFA, primary
	// caller is an API key with ScopesReadSurface). The handler
	// reverse-proxies to gatewayd-internal's control listener
	// /v1/internal/apps/{slug}/routes via the existing
	// apidProxy hop. IDOR-safe via loadApp — cross-account slug
	// is a 404, not a 200 with another tenant's route labels
	// (a customer who shouldn't see a route set on app X
	// cannot enumerate it through this endpoint).
	mux.HandleFunc("GET /v1/apps/{slug}/routes", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppRoutes)))
	// ADR-091 D20.5 amendment / issue #881 — per-route throttle
	// recommender (Phase 1). Read-only, no MFA, primary caller is
	// an API key with ScopesReadSurface. IDOR-safe via loadApp —
	// cross-account slug is a 404, not a 200 with another tenant's
	// per-route observations. The recommender is ADVICE-ONLY — it
	// never auto-applies; customers confirm via POST
	// /v1/apps/{slug}/edge-rules.
	mux.HandleFunc("GET /v1/apps/{slug}/throttle-suggestions", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppThrottleSuggestions)))
	// ADR-102 D6 — per-app streaming-cap probe. Read-only, no MFA,
	// primary caller is an API key with ScopesReadSurface. IDOR-safe
	// via loadApp (cross-account slug → 404, not 200 with another
	// tenant's streaming-cap data — leaking cap data lets a
	// customer probe another tenant's plan tier). See
	// handlers_streaming_cap.go for the full decision tree and the
	// deliberate non-dial to gatewayd-internal.
	mux.HandleFunc("GET /v1/apps/{slug}/streaming-cap", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppStreamingCap)))
	// Account-scoped metrics rollup (issue #393). One call replaces
	// N per-app /v1/apps/{slug}/metrics calls. Same auth chain as
	// the per-app endpoint (read-only, no MFA). Cross-account
	// isolation is the SQL JOIN on apps.account_id = $1 in the
	// pgstore helper — there's no (accountID, slug) pair to load
	// because there's no slug path.
	mux.HandleFunc("GET /v1/apps/metrics", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppsMetrics)))
	// Issue #696 / ADR-082 — per-app SLO panel. Closed-set
	// windowed vocabulary (1h | 24h | 7d). Auth chain matches
	// /v1/apps/{slug}/metrics: read-only, NO MFA, primary
	// caller is an API key with ScopesReadSurface. IDOR-safe
	// via loadApp (cross-account slug → 404).
	mux.HandleFunc("GET /v1/apps/{slug}/slo", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppSLO)))
	// ADR-096 / PR-B — customer-facing automatic error
	// grouping. Read-only, no MFA, primary caller is an API key
	// with ScopesReadSurface. IDOR-safe via loadApp
	// (cross-account slug → 404, byte-identical to a real 404).
	// The three handlers delegate sqlc → wire DTO conversion to
	// handlers_app_errors_projection.go.
	mux.HandleFunc("GET /v1/apps/{slug}/errors/summary", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppErrorsSummary)))
	mux.HandleFunc("GET /v1/apps/{slug}/errors/{fingerprint}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listAppErrorRequests)))
	mux.HandleFunc("GET /v1/apps/{slug}/errors/{fingerprint}/first", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppErrorSample)))
	// Issue #696 / ADR-082 — account-scoped SLO rollup. Flat
	// scalar responses (Billing-derivable instance_hours /
	// gb_hours), so the auth chain matches /v1/usage (MFA +
	// usage:read). Cross-account isolation is the SQL JOIN
	// on apps.account_id = $1 in the pgstore helper.
	mux.HandleFunc("GET /v1/account/slo", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.getAccountSLO))))
	mux.HandleFunc("PATCH /v1/apps/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateApp))))
	mux.HandleFunc("DELETE /v1/apps/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteApp))))
	// Mega-C PR-1 / issue #961 leaf 3: one-click preview destroy
	// from a PR comment. Distinct URL from DELETE /v1/apps/{slug}
	// so production apps do not collide with the preview-specific
	// path. Same auth chain (authLimited → requireMFA →
	// requireScope(DeployWriteSurface)) as the production delete
	// — destroying a preview is just as destructive as destroying
	// a production app from the customer's POV.
	mux.HandleFunc("POST /v1/preview/{slug}/destroy", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.destroyPreview))))
	// Issue #472 / ADR-054 — admin-only signature-enforcement toggle.
	// Mounted with the admin+MFA chain (mirrors PATCH /v1/account/plan
	// at server.go:516) so a customer cannot self-onboard signature
	// enforcement on their own app. The customer PATCH surface above
	// silently drops a customer-set require_signed — see
	// handlers_ext.go::updateApp for the rationale.
	mux.HandleFunc("PATCH /v1/apps/{slug}/security", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.patchAppSecurity))))
	// Issue #472 / ADR-054 — per-app cosign trusted-publisher list
	// (admin + MFA). GET requires admin (read), PUT/DELETE require
	// admin + MFA (write). The mount chain handles both via the
	// admin scope; the same authLimited → requireMFA → requireScope
	// wrapper covers both verbs. Mirrors the env / secrets handlers
	// above for the (slug, name) resource pattern.
	mux.HandleFunc("GET /v1/apps/{slug}/trusted_signers", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.listTrustedSigners))))
	mux.HandleFunc("PUT /v1/apps/{slug}/trusted_signers/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.upsertTrustedSigner))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/trusted_signers/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.deleteTrustedSigner))))

	// ADR-119: per-app static egress IP (Scale-only, BYOIP, single-
	// node v1). Customer-scoped (no admin, no MFA) — the customer
	// owns the pin. The feature-flag check (api.StaticEgressIPEnabled)
	// runs inside each handler so a dark-launched cluster signals
	// 402 (not 404) until the operator sets FAAS_STATIC_EGRESS_IP_ENABLED.
	// Same posture as the tenant-surfaces block above.
	mux.HandleFunc("GET /v1/apps/{slug}/static-egress-ip", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getAppStaticEgressIP))))
	mux.HandleFunc("PUT /v1/apps/{slug}/static-egress-ip", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.setAppStaticEgressIP))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/static-egress-ip", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.clearAppStaticEgressIP))))

	// Issue #879 / ADR-100 PR-C — tenant surfaces (customer-facing
	// hostname routing primitive). Feature-flagged via
	// api.TenantSurfacesEnabled(); the flag check runs inside each
	// handler so the routes 402 (not 404) when the operator has
	// not yet enabled the cluster-side surface. Auth chain mirrors
	// the closest precedent (custom_domains at server.go:986):
	// authLimited → requireMFA → requireScope(deploy:write) for
	// mutators, requireScope(read) for the list/get.
	mux.HandleFunc("GET /v1/apps/{slug}/tenant-surfaces", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listTenantSurfaces))))
	mux.HandleFunc("POST /v1/apps/{slug}/tenant-surfaces", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createTenantSurface)))))
	mux.HandleFunc("GET /v1/apps/{slug}/tenant-surfaces/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getTenantSurface))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/tenant-surfaces/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteTenantSurface))))
	mux.HandleFunc("POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.addTenantHostname)))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{hostname}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.removeTenantHostname))))

	// Issue #270 / ADR-101 / PR-A: OIDC / keyless deploy auth.
	// POST /v1/auth/oidc/exchange mints a short-lived opaque bearer
	// from an IdP-issued JWT. The route is anonymous (the caller
	// has no session, no bearer — the JWT IS the auth) so it's
	// mounted via middleware.AuthLimit (per-IP rate limit, no
	// session required) on the shared apiAuthLimiter. The 10/min/IP
	// envelope is shared with the rest of the API surface so an
	// OIDC brute-force contributes to the same counter as the
	// bearer / login endpoints. The handler is built in
	// newServerWithDeps (cmd/apid/server.go:670) and stamped on
	// s.oidcHandler; nil here means a legacy test that hasn't
	// wired the OIDC surface, in which case the route is
	// intentionally not mounted.
	if s.oidcHandler != nil {
		mux.Handle("POST /v1/auth/oidc/exchange", middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{Log: s.log}, s.apiAuthLimiter)(s.oidcHandler))
	}

	// Deployments.
	mux.HandleFunc("POST /v1/apps/{slug}/deployments", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.createDeployment))))))
	mux.HandleFunc("POST /v1/apps/{slug}/deployments/dev-source", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.handleDevSourceDeploy))))))
	// ADR-117 §Production-ready follow-on, C2 — per-stage retry.
	// Same auth chain as createDeployment (authLimited → requireMFA
	// → requireScope(ScopesDeployWriteSurface)). NOT wrapped in
	// s.idempotent: every retry call creates a fresh deployments
	// row, so idempotency-key collapse would silently mask the
	// new-row creation. The closed-vocab guard lives in the
	// handler (state.IsStageName) — invalid from_stage returns 400
	// with a structured RFC 7807 problem before the storage call.
	mux.HandleFunc("POST /v1/deployments/{id}/retry", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.retryDeployment))))
	// DEPLOY-PROV-4 / ADR-092 / issue #739 — headless source-ref
	// deploy from CI. Same auth chain as the multipart sibling
	// (authLimited → requireMFA → requireScope(ScopesDeployWriteSurface)
	// → idempotent). Idempotency-Key on a CI retry collapses to the
	// same build row; handler resolves the install token via the
	// githubd gRPC bridge (cmd/apid/githubd_client.go) and streams
	// the upstream tarball straight into validateAndSpool.
	mux.HandleFunc("POST /v1/apps/{slug}/deployments/source-ref", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.handleSourceRefDeploy))))))
	// Issue #961 / Mega-A PR-1 — zero-config local-tarball deploy.
	// The CLI is the trust root on this path; apid does NOT consult
	// github_installations and does NOT attempt a server-side git
	// fetch. See docs/adr/0XX-local-tarball-deploy-trust-root.md.
	// Parallel to source-ref, not a replacement — that gate stays
	// load-bearing for `--repo X --ref SHA` semantics.
	// The legacy multipart path remains available during the resumable-upload
	// migration, but advertise the successor on every response (including
	// auth failures) so older clients can move to POST /v1/uploads.
	mux.Handle("POST /v1/apps/{slug}/deployments/source-tarball", s.withDeprecationHTTP(`</v1/uploads>; rel="successor-version"`, s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.handleSourceTarballDeploy)))))))
	// PR-1 of issue #1182 §P1 packaging follow-up — resumable
	// upload protocol. The legacy endpoint above stays active;
	// PR-2 wires the CLI to these 4 endpoints; PR-3 deprecates
	// the legacy one with RFC 8594 Sunset headers.
	//
	// upload_sessions.id IS the dedupe key for commit retries
	// (see upload_commit_outcomes companion table), so the
	// Idempotency-Key middleware is intentionally NOT wired
	// here — it would short-circuit a *new* session creation
	// that should otherwise succeed.
	mux.HandleFunc("POST /v1/uploads", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleStartUpload))))
	mux.HandleFunc("GET /v1/uploads/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleGetUpload))))
	mux.HandleFunc("PATCH /v1/uploads/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleAppendUpload))))
	mux.HandleFunc("POST /v1/uploads/{id}/commit", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleCommitUpload))))
	mux.HandleFunc("DELETE /v1/uploads/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleCancelUpload))))
	// PR-1 of the deploy-diff cluster — server-side pre-deploy
	// preview. Read-only (no DB writes, no audit row, no deployment
	// row), so the auth chain matches GET /v1/apps/{slug}/metrics
	// (server.go:785): authLimited → requireScope(ScopesReadSurface)
	// with NO requireMFA. A CI key with `apps:read` is sufficient;
	// typical deploy-write keys also pass (ScopeAdmin covers the
	// read surface). Cross-account isolation is via loadApp at
	// handlers_diff.go:diffApp.
	mux.HandleFunc("POST /v1/apps/{slug}/diff", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.diffApp)))
	// ADR-126 / issue #975 item #2 — OpenAPI Import + Auto-Generation.
	// Four app-scoped routes keyed on {slug}, distinct from the
	// deployment-keyed getOpenAPIDoc / patchOpenAPIDoc / openAPIDocDelete
	// family above (item #1, lines 1025-1027). The GET accepts
	// ?source=manual_import|auto so the dashboard can fetch either
	// the customer's uploaded doc verbatim or the platform-merged
	// auto-gen spec (cache-backed per app). Plan-tier gate is gone
	// (limits are abuse-surface, not tier — Free can import up to
	// the per-account row cap). The dry-run POST is read-only so it
	// rides the read-scope chain with no requireMFA (same posture
	// as /v1/apps/{slug}/diff just above).
	mux.HandleFunc("GET /v1/apps/{slug}/openapi", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppOpenAPI)))
	mux.HandleFunc("POST /v1/apps/{slug}/openapi", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.postAppOpenAPIImport))))
	mux.HandleFunc("POST /v1/apps/{slug}/openapi/dry-run", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.postAppOpenAPIImportDryRun)))
	mux.HandleFunc("DELETE /v1/apps/{slug}/openapi", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteAppOpenAPIImport))))
	mux.HandleFunc("GET /v1/deployments/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeploymentReadSurface...)(s.getDeployment))))
	// Per-deploy grype scan drill-down (issue #464 / ADR-055).
	// Returns the typed api.ScanResult envelope (status,
	// severity counts, vulnerabilities, error). 404 on
	// not-yet-scanned or cross-account; IDOR posture
	// identical to getDeployment above.
	mux.HandleFunc("GET /v1/deployments/{id}/scan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDeploymentScan))))
	// Per-deployment deployment_audit timeline (issue #976 /
	// ADR-122 / SAFE-RELEASES-E.2 + production-leveling Stream A).
	// Returns the same shape the dashboard's
	// deployment_detail.html audit block reads (api.ListDeploymentAuditResponse).
	// IDOR posture identical to getDeployment above — the handler
	// re-validates the deployment belongs to acct via s.store.AppByDeploymentID.
	mux.HandleFunc("GET /v1/deployments/{id}/audit", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listDeploymentAudit))))
	// ADR-122 / issue #975 item #1: per-deployment OpenAPI
	// document discovery surface. Three routes (GET, PATCH,
	// DELETE) under /v1/apps/{slug}/deployments/{deployment}/openapi.
	// The plan-tier gate lives in the handler (free → 402
	// CodePlanOpenAPIDocsNotAllowed) so a Free customer posting
	// to a non-existent slug still gets a clean 402, not a 404
	// slug leak (same pattern as createEdgeRule /
	// createAlertRule). The microVM captures the doc during
	// cold boot on every plan; the apid only SERVES the doc
	// on paid plans.
	mux.HandleFunc("GET /v1/apps/{slug}/deployments/{deployment}/openapi", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getOpenAPIDoc))))
	mux.HandleFunc("PATCH /v1/apps/{slug}/deployments/{deployment}/openapi", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.patchOpenAPIDoc))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/deployments/{deployment}/openapi", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.openAPIDocDelete))))
	// PR-A: per-deploy image-layer secret-scan audit surface.
	// Mirrors /scan — same auth chain (authLimited + requireMFA +
	// read scope), same IDOR posture (cross-account → 404), same
	// 404-on-pending drilldown shape.
	mux.HandleFunc("GET /v1/deployments/{id}/secret-scan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDeploymentSecretScan))))
	mux.HandleFunc("GET /v1/deployments/{id}/logs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.streamDeploymentLogs))))
	// Per-deployment closed-stage summary (issue #985 follow-up).
	// Companion to /logs — same auth chain (authLimited + requireMFA +
	// read scope), same IDOR posture (cross-account → 404). The body
	// is the raw deployments.stage_state jsonb re-emitted verbatim;
	// the closed-6-stage vocabulary is enforced by the migration's
	// CHECK constraint, so the handler does no Go-side decoding.
	mux.HandleFunc("GET /v1/deployments/{id}/stages", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDeploymentStages))))
	// Issue #976 / ADR-122 / SAFE-RELEASES-C.2 — per-deployment
	// preview URL read seam. Same auth chain as the sibling
	// /stages / /scan / /secret-scan routes (authLimited +
	// requireMFA + read scope). Cross-account probes return 404,
	// never 403; non-preview-active rows return 200 with Alive=
	// false so the dashboard renders the closed-state chip
	// without a second round-trip.
	mux.HandleFunc("GET /v1/deployments/{id}/url", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDeploymentURL))))
	// Issue #557 closure / ADR-072 — PATCH the per-deployment floor
	// (MinInstances). Reuses the deploy-write scope (the only mutable
	// field is the floor; image / digest / overrides / sidecars stay
	// immutable post-create).
	mux.HandleFunc("PATCH /v1/deployments/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateDeploymentMinInstances))))
	// Issue #556 PR-A — PATCH the per-deployment traffic-split
	// weight. Dedicated route (not a sibling field on the
	// /v1/deployments/{id} PATCH) because the request shape differs
	// (traffic_percent mandatory) and the plan gate is Pro+ only.
	// Same deploy-write scope; the row mutation is at least as
	// powerful as the min_instances PATCH (Σ rebalance affects
	// sibling live rows in the same app).
	mux.HandleFunc("PATCH /v1/deployments/{id}/traffic", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateDeploymentTraffic))))
	// Issue #976 / ADR-122 — meterd's single-step canary write seam.
	// The endpoint is idempotent and compare-and-swap guarded; APID
	// derives the next stage from persisted state and the store commits
	// traffic, canary state, rollout completion, and audit atomically.
	mux.HandleFunc("POST /v1/deployments/{id}/canary/advance", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.advanceCanary)))))
	// ADR-124 deployment queue controls. Four routes; cancel +
	// clear-obsolete (Free-allowed); reorder + clear-obsolete's
	// plan-gated path use ScopeDeployWrite + Plan.QueueControlsAllowed.
	// The {slug} form on cancel lets us honour the same loadApp
	// IDOR gate that POST /v1/apps/{slug}/deployments uses; the
	// id-only form on the other three mirrors the existing PATCH
	// /v1/deployments/{id} posture (DeploymentByID → AppByID
	// account check inside the handler).
	mux.HandleFunc("POST /v1/apps/{slug}/deployments/{id}/cancel", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleCancelDeployment))))
	mux.HandleFunc("POST /v1/deployments/{id}/reorder", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleReorderDeployment))))
	mux.HandleFunc("DELETE /v1/deployments/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleClearDeployment))))
	// clear-obsolete is app-scoped (the actual bulk soft-delete
	// operator), not deployment-scoped. Mounted under
	// /v1/apps/{slug}/deployments/clear-obsolete so the slug
	// gate naturally scopes the call. The router reserves
	// "clear-obsolete" as the literal sub-path segment (not a
	// {id} placeholder) so there is no conflict with the
	// sibling {id}-bearing routes above.
	mux.HandleFunc("POST /v1/apps/{slug}/deployments/clear-obsolete", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handleClearObsoleteDeployments))))
	// Builds (DEPLOY-PROV-6 / ADR-089, issue #741). Lifecycle
	// surface — returns status, timestamps, failure_class,
	// server-computed duration_seconds for a build id. Companion
	// to the ADR-038 /v1/builds/{id}/provenance (post-mortem
	// export) and /v1/builds/{id}/sbom (post-mortem blob) routes;
	// this one is what CI scripts call to fail-fast on build
	// error without scraping SSE. Same auth (api.ScopesReadSurface)
	// + same IDOR-safe Build → Deployment → App → AccountID check
	// as the sibling routes. The status field is the existing
	// 4-state enum (queued|running|succeeded|failed); see ADR-089
	// §1 for why 'cancelled' is out of scope.
	mux.HandleFunc("GET /v1/builds/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getBuild)))
	// Builds list (DEPLOY-PROV-6 follow-up / ADR-091, issue #741
	// close-out). Companion to GET /v1/builds/{id} — same auth +
	// scope chain (intentionally NO requireMFA per ADR-089 §6;
	// GET /v1/deployments does use requireMFA but the builds
	// family deliberately does not — see ADR-089). The IDOR
	// chain for ?app=<slug> is AppBySlug + App.AccountID == acct.ID
	// (cross-account slug → 404 app_not_found). Cursor pagination
	// + status filter mirror GET /v1/deployments.
	mux.HandleFunc("GET /v1/builds", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listBuilds)))
	// Builds (ADR-038). The provenance route is the only /v1/builds
	// surface today; deployments.id remains the parent resource.
	// Build:read scope (api.ScopesReadSurface) gates the read.
	mux.HandleFunc("GET /v1/builds/{id}/provenance", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getBuildProvenance)))
	// PR-D / issue #791: GET /v1/cron-fire-now-requests/{request_id} is
	// the customer-visible read surface for the row that
	// POST /v1/crons/{id}/run inserts. IDOR-safe byte-identical-404:
	// bad UUID, no row, cross-account row all return the same
	// not_found Problem body — no existence oracle. Same auth +
	// scope as the cron-run write (ScopesReadSurface).
	mux.HandleFunc("GET /v1/cron-fire-now-requests/{request_id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getFireCronRequest)))
	// Builds (issue #299 / ADR-038 Phase 3). The /sbom route is the
	// ADR-038 Phase-3 companion to /provenance — same auth, same
	// scoping, same IDOR-safe Build → Deployment → App → AccountID
	// check; the handler streams the CycloneDX JSON file from
	// FAAS_STORAGE_ROOT/<sbom_storage_key> rather than rendering it
	// through writeJSON. Returns build_sbom_unavailable (503) when
	// the imaged syft populator hasn't run for this build.
	mux.HandleFunc("GET /v1/builds/{id}/sbom", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getBuildSbom)))
	mux.HandleFunc("POST /v1/apps/{slug}/rollback", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.rollbackApp)))))
	// SAFE-RELEASES-R (issue #976 / ADR-122): the operator
	// manual-rollout-recovery escape hatch. The CLI subcommand
	// `gregale rollouts recover <slug>` is the canonical caller;
	// the route is here for scripting + the rare on-call
	// operator who hits the apid directly. State-machine
	// guards (advance/promote/abort) live in
	// store.RecoverRollout and the closed-set error mapping
	// lives in cmd/apid/handlers_rollouts.go.
	mux.HandleFunc("POST /v1/apps/{slug}/rollouts/recover", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.recoverRollout)))))
	mux.HandleFunc("POST /v1/apps/{slug}/park", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.parkApp))))
	mux.HandleFunc("POST /v1/apps/{slug}/wake", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.wakeApp))))
	mux.HandleFunc("POST /v1/apps/{slug}/restart", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.restartApp)))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/cache", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.purgeAppCache))))
	mux.HandleFunc("POST /v1/apps/{slug}/rename", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.renameApp)))))

	// Instances (read-only here; schedd is the writer).
	mux.HandleFunc("GET /v1/apps/{slug}/instances", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listInstances))))
	// Account-scoped instances (issue #393). Cursor: ?before=
	// (instances.id UUIDv7). Default limit 25, max 100 (strict 400
	// on bad input via api.ParseLimit). Additive to the per-app
	// endpoint — dashboards opt in. Per-account rate limit
	// (ADR-040) fires at the gatewayd-internal edge; this route charges 1
	// token via authLimited just like every other /v1/* call.
	mux.HandleFunc("GET /v1/instances", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listInstancesForAccount))))

	// Custom domains.
	mux.HandleFunc("GET /v1/domains", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listDomains))))
	mux.HandleFunc("POST /v1/domains", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createDomain)))))
	mux.HandleFunc("DELETE /v1/domains/{domain}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteDomain))))
	// Issue #961 / Mega-A PR-3: per-domain verify + show surfaces for
	// `gregale domains verify | show`. GET /v1/domains/{domain} returns
	// the row + cert (NotAfter, SANs); POST /v1/domains/{domain}/verify
	// re-runs the DNS + cert walk and returns the result. Both mirror
	// the existing custom-domains auth gates (write for verify, read for
	// show).
	mux.HandleFunc("POST /v1/domains/{domain}/verify", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.verifyDomain)))))
	mux.HandleFunc("GET /v1/domains/{domain}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDomain))))
	// ADR-120: per-domain doctor surface for `gregale domains doctor`.
	// Read-only; returns the latest observation row from
	// domain_doctor_observations plus a synchronous re-probe if
	// the row is older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS. The
	// route stays registered when FAAS_DOMAIN_DOCTOR_ENABLED is
	// unset; the handler returns 503 CodeDoctorDisabled in that
	// case so the CLI can render a clear "doctor is dark-launched"
	// message rather than a generic 404 (matches the pre-#911
	// pattern in api/flags.go).
	mux.HandleFunc("GET /v1/domains/{domain}/doctor", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getDomainDoctor))))

	// Crons.
	mux.HandleFunc("GET /v1/crons", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listCrons))))
	mux.HandleFunc("POST /v1/crons", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createCron)))))
	mux.HandleFunc("PATCH /v1/crons/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateCron))))
	mux.HandleFunc("DELETE /v1/crons/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteCron))))
	// Single-cron read (issue #791 PR-E / ADR-090 closure). Backs
	// `gregale crons info <id>` and any dashboard drill-down. Read
	// surface plus requireMFA — keeps the crons family consistent:
	// listCrons, listCronRuns, fireCronNow, createCron, updateCron,
	// deleteCron all use requireMFA, so getCron is the lone exception
	// if we skip it. code-review finding A4.
	mux.HandleFunc("GET /v1/crons/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getCron))))
	// Per-cron execution history (issue #791). Read surface, so
	// ScopesReadSurface and no idempotency wrapper.
	mux.HandleFunc("GET /v1/crons/{id}/runs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listCronRuns))))
	// Manual fire-now (issue #791 PR-C / ADR-090). Deploy-write scope
	// (no new cron:write constant per ADR-090 §Sub-decisions 1).
	// idempotent is INNERMOST so the replay lookup happens AFTER
	// auth/scope — a duplicate (account, Idempotency-Key) request
	// returns the stored 202 without inserting a second row.
	mux.HandleFunc("POST /v1/crons/{id}/run", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.fireCronNow)))))

	// Triggers (issue #757 / ADR-0NN; commit #6). Same authLimited +
	// requireMFA + requireScope shape as the cron family. POST routes
	// are idempotent-wrapped so retries are safe (the createTrigger
	// handler is the only state-mutating POST that needs the wrap;
	// pause/resume/records/{rid}/{retry,drop} are operator verbs and
	// run without idempotency — they are tuned for human action, not
	// at-least-once delivery).
	mux.HandleFunc("GET /v1/triggers", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listTriggers))))
	mux.HandleFunc("POST /v1/triggers", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createTrigger)))))
	mux.HandleFunc("GET /v1/triggers/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getTrigger))))

	// Jobs (issue #1184 Workstream A / ADR-099). Same
	// authLimited + requireMFA + requireScope shape as the
	// cron family. POST routes (createJob + createJobRun) are
	// idempotent-wrapped so retries are safe; PATCH/DELETE
	// are NOT — they're tuned for human action, not at-least-
	// once delivery. Cancel is also NOT idempotent-wrapped: a
	// second cancel after the first is a 200 (the run is
	// already cancelled) but a duplicate Idempotency-Key
	// would skip the second run-cancel audit emit.
	//
	// Plan-tier gate (Free → 402 CodeJobsNotAllowed) lives in
	// createJob / updateJob / deleteJob / createJobRun /
	// cancelJobRun — see handlers_jobs.go::buildJob. The
	// read endpoints (listJobs / getJob / listJobRuns /
	// getJobRun / listJobRunTasks / getJobTaskLogs) return
	// empty lists on Free (read gate is not in the plan).
	mux.HandleFunc("GET /v1/jobs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listJobs))))
	mux.HandleFunc("POST /v1/jobs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createJob)))))
	mux.HandleFunc("GET /v1/jobs/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getJob))))
	mux.HandleFunc("PATCH /v1/jobs/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateJob))))
	mux.HandleFunc("DELETE /v1/jobs/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteJob))))
	mux.HandleFunc("POST /v1/jobs/{name}/runs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createJobRun)))))
	mux.HandleFunc("GET /v1/jobs/{name}/runs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listJobRuns))))
	mux.HandleFunc("GET /v1/jobs/{name}/runs/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getJobRun))))
	mux.HandleFunc("POST /v1/jobs/{name}/runs/{id}/cancel", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.cancelJobRun))))
	mux.HandleFunc("GET /v1/jobs/{name}/runs/{id}/tasks", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listJobRunTasks))))
	mux.HandleFunc("GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getJobTaskLogs))))

	// Workflows (ADR-081)
	mux.HandleFunc("POST /v1/apps/{slug}/workflows/{name}/runs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.createWorkflowRun))))
	mux.HandleFunc("GET /v1/apps/{slug}/workflows/runs", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listWorkflowRuns))))
	mux.HandleFunc("GET /v1/workflows/runs/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getWorkflowRun))))
	mux.HandleFunc("GET /v1/workflows/runs/{id}/steps", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listWorkflowSteps))))
	mux.HandleFunc("POST /v1/workflows/runs/{id}/events", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.injectWorkflowEvent))))
	mux.HandleFunc("POST /v1/workflows/runs/{id}/cancel", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.cancelWorkflowRun))))
	mux.HandleFunc("PATCH /v1/triggers/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateTrigger))))
	mux.HandleFunc("DELETE /v1/triggers/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteTrigger))))
	mux.HandleFunc("POST /v1/triggers/{id}/pause", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.pauseTrigger))))
	mux.HandleFunc("POST /v1/triggers/{id}/resume", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.resumeTrigger))))
	mux.HandleFunc("GET /v1/triggers/{id}/records", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listTriggerRecords))))
	mux.HandleFunc("POST /v1/triggers/{id}/records/{rid}/retry", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.retryTriggerRecord))))
	mux.HandleFunc("POST /v1/triggers/{id}/records/{rid}/drop", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dropTriggerRecord))))
	mux.HandleFunc("GET /v1/triggers/{id}/dlq", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listTriggerDeadLetter))))
	mux.HandleFunc("GET /v1/triggers/{id}/metrics", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getTriggerMetrics))))
	// Batch-create (POST /v1/triggers:batch_create) accepts an inline
	// gregale.yaml blob. Distinct route path (":batch_create" suffix,
	// not /v1/triggers/{id} with wildcard id) so the mux pattern
	// matcher doesn't conflict with the {id} getTrigger route.
	mux.HandleFunc("POST /v1/triggers:batch_create", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.batchCreateTrigger)))))

	// Projects (ADR-050, Phase 3). Two routes — /scan is dry-run
	// (no writes), / is the transactional apply. Both are deploy-
	// write scoped; / is wrapped in s.idempotent so retries are
	// safe. The middleware order is the same as POST /v1/crons so
	// a Free plan customer gets the same 402/403 surfaces for
	// over-quota on /apply.
	mux.HandleFunc("POST /v1/projects/scan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.scanProject))))
	mux.HandleFunc("POST /v1/projects", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.applyProject)))))

	// ADR-124 code-review fix #2 — operator escape hatch. The
	// CLI's `gregale deployments exclude clear --slug=NAME
	// --project-slug=SLUG` calls this when a persisted exclusion
	// is stale (the workload was renamed or deleted in a future
	// commit) and blocking deploys via exclude_unknown_slug.
	// Idempotent at the store level — DELETE on no row returns
	// 404 with code="scope_exclusion_not_found" so the CLI can
	// render "already clear" instead of a hard error. Requires
	// deploy:write because mutating the persisted exclusion set
	// changes which workloads auto-exclude on subsequent
	// deploys.
	mux.HandleFunc("DELETE /v1/projects/{slug}/exclusions/{slug2}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteDeploymentScopeExclusion))))

	// Alert rules (ADR-045 / issue #396 PR 3).
	// CRUD surface under /v1/apps/{slug}/alerts. The rotate-secret
	// action verb is the literal `/rotate-secret` segment (Go 1.22+
	// mux wildcard ends at `}` so a colon-form verb would panic —
	// same precedent as the queues/{id}/ack block at line 642).
	// Plan-tier gate (Free→402) lives inside createAlertRule /
	// listAlertRules so a Free customer posting to a non-existent
	// slug gets a clean 402, not a 404 that would leak the slug
	// (PR review finding F4).
	mux.HandleFunc("GET /v1/apps/{slug}/alerts", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAlertRules))))
	mux.HandleFunc("POST /v1/apps/{slug}/alerts", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createAlertRule)))))
	mux.HandleFunc("GET /v1/apps/{slug}/alerts/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getAlertRule))))
	// ADR-123 PR-D: listAlertRuleDeliveries — operator pane for
	// recent alert_deliveries rows. ?include_test=true toggles the
	// IsTest discriminator (default false; production-only rows).
	// Same auth scope as getAlertRule so a customer cannot probe
	// another account's ledger by guessing rule IDs.
	mux.HandleFunc("GET /v1/apps/{slug}/alerts/{id}/deliveries", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAlertRuleDeliveries))))
	mux.HandleFunc("PATCH /v1/apps/{slug}/alerts/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateAlertRule))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/alerts/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteAlertRule))))
	mux.HandleFunc("POST /v1/apps/{slug}/alerts/{id}/rotate-secret", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.rotateAlertRuleSecret))))

	// Alert presets (ADR-123 / issue #1233). Catalog R/O for
	// customers; instantiate-from-preset reuses the alert-rules
	// create path (handlers_alert_presets.go). The enable route
	// is idempotent so SDK retries are safe — the duplicate POST
	// returns 409 "Preset already enabled" so the caller can
	// branch on a stale POST without silently no-op'ing. Plan-tier
	// gate (catalog row's minimum_plan → 402) and disabled-row
	// gate (enabled_in_catalog=false → 400 alert_preset_disabled)
	// both fire BEFORE loadApp for the same slug-leak reason
	// createAlertRule gates on the per-plan cap (PR review F4).
	mux.HandleFunc("GET /v1/alert-presets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAlertPresets))))
	mux.HandleFunc("POST /v1/apps/{slug}/alert-presets/{name}/enable", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.enableAlertPreset)))))
	// Issue #1233 / ADR-123 PR-C commit 2 — "Send test alert" button.
	// Same auth chain as /enable (authLimited → requireMFA → requireScope)
	// but NOT wrapped in `idempotent` because every POST is a fresh
	// dispatch attempt with a fresh delivery_id (replay safety: a
	// client retry that hits idempotent's de-dup table would mask
	// the legitimate second test fire). The 404-on-no-rule gate runs
	// first inside the handler so a stale POST to a card the customer
	// removed still returns a clean 404, not a 5xx.
	mux.HandleFunc("POST /v1/apps/{slug}/alert-presets/{name}/test", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.sendTestAlertPreset))))

	// Edge rules (ADR-089, planned). Customer-facing resource that
	// runs in pkg/gateway BEFORE host→app resolution. Mirrors the
	// alert-rules decorator chain: authLimited → requireMFA →
	// requireScope (read|deploy-write); POST is also wrapped in
	// idempotent so SDK retries are safe. Plan-kind gate
	// (jwt|ip → 402) and per-app quota (402) live inside
	// createEdgeRule so a Free customer posting to a non-existent
	// slug still gets a clean 402, not a 404 slug leak (same
	// pattern as createAlertRule). The two list routes cover the
	// two read surfaces: account-wide overview for the dashboard
	// pane, app-scoped for the per-app config view.
	mux.HandleFunc("GET /v1/edge-rules", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listEdgeRules))))
	mux.HandleFunc("GET /v1/apps/{slug}/edge-rules", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listEdgeRulesForApp))))
	mux.HandleFunc("POST /v1/apps/{slug}/edge-rules", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createEdgeRule)))))
	mux.HandleFunc("GET /v1/edge-rules/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getEdgeRule))))
	mux.HandleFunc("PATCH /v1/edge-rules/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateEdgeRule))))
	mux.HandleFunc("DELETE /v1/edge-rules/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteEdgeRule))))

	// Traffic mirroring (issue #72 / ADR-125 PR-A2). Six routes
	// under /v1/apps/{slug}/mirrors. The path is slug-scoped (not
	// deployment-id-scoped) so the IDOR guard is the cheaper
	// s.loadApp path — loadApp resolves to (app, true) only when
	// app.AccountID == acct.ID. The {id} segment is the mirror
	// rule's UUID; loadMirrorRuleIfOwned applies the second
	// cross-account check so a stolen API key cannot probe rule
	// existence across accounts. PR-A2 ships CRUD only; the
	// runtime mirror goroutine (PR-A3) listens on the
	// deployment_changed pg_notify channel for the kind="mirror"
	// discriminant.
	mux.HandleFunc("POST /v1/apps/{slug}/mirrors", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.createMirrorRule))))
	mux.HandleFunc("GET /v1/apps/{slug}/mirrors", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listMirrorRules))))
	mux.HandleFunc("GET /v1/apps/{slug}/mirrors/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getMirrorRule))))
	mux.HandleFunc("PATCH /v1/apps/{slug}/mirrors/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateMirrorRule))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/mirrors/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteMirrorRule))))
	mux.HandleFunc("GET /v1/apps/{slug}/mirrors/{id}/summary", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getMirrorRuleSummary))))

	// CORS presets (issue #975 #4 / PR-B / ADR-129). The
	// account-wide + app-scoped read surface is a single
	// /v1/cors-presets endpoint with an optional app_id query
	// parameter; the per-id CRUD is the standard
	// GET/PATCH/DELETE shape. The create + patch + delete
	// paths go through CreateCorsPresetIfUnderQuota /
	// UpdateCorsPreset / DeleteCorsPreset on the Store; the
	// pgstore trigger fires pg_notify('cors_preset_changed',
	// account_id) after every write so the gatewayd-internal
	// listener reloads the affected account's preset overlay
	// (ADR-129 D4). The plan-tier gate fires INSIDE the
	// create handler (402 plan_cors_presets_not_allowed for
	// Free) so the gate precedes the loadStore call.
	mux.HandleFunc("GET /v1/cors-presets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listCorsPresets))))
	mux.HandleFunc("POST /v1/cors-presets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createCorsPreset)))))
	mux.HandleFunc("GET /v1/cors-presets/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getCorsPreset))))
	mux.HandleFunc("PATCH /v1/cors-presets/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.patchCorsPreset))))
	mux.HandleFunc("DELETE /v1/cors-presets/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteCorsPreset))))

	// Outbound webhooks (issue #476 / ADR-076). CRUD surface under
	// /v1/apps/{slug}/webhooks mirrors /v1/apps/{slug}/alerts: same
	// plan-tier gate (Free → 402 plan_webhooks_not_allowed), same
	// idempotent + authLimited wrapper, same secretbox.SealBytes
	// + masked-constant response shape. Two extra endpoints:
	// GET /deliveries (visibility) and POST /deliveries/{did}/retry
	// (manual DLQ escape hatch — issue #476 acceptance gate 4).
	mux.HandleFunc("GET /v1/apps/{slug}/webhooks", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAppWebhooks))))
	mux.HandleFunc("POST /v1/apps/{slug}/webhooks", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createAppWebhook)))))
	mux.HandleFunc("GET /v1/apps/{slug}/webhooks/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getAppWebhook))))
	mux.HandleFunc("PATCH /v1/apps/{slug}/webhooks/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateAppWebhook))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/webhooks/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteAppWebhook))))
	mux.HandleFunc("POST /v1/apps/{slug}/webhooks/{id}/rotate-secret", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.rotateAppWebhookSecret))))
	mux.HandleFunc("GET /v1/apps/{slug}/webhooks/{id}/deliveries", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAppWebhookDeliveries))))
	mux.HandleFunc("POST /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.retryAppWebhookDelivery))))

	// Move 2: event-driven surface (handlers_invocations.go).
	// Charged routes take idempotent so retries are safe; the long-poll
	// ones take authLimited (no idempotent — a network jitter on a
	// long-poll is the customer's retry, not the SDK's).
	//
	// Path note: Go 1.22+ ServeMux requires "{id}" wildcards to end at
	// "}" — the colon-form verbs (`invocations:send`) break the
	// parser. The Move 2 routes use the `action` segment form so the
	// mux can register them. The wire shape matches the spec-level
	// intent (`queueSend`, `queueReceive`, `queueAck` handlers).
	//
	// IAM-1 (ADR-034): every Move 2 route gets the same per-method
	// scope default as the rest of /v1/* — invoke/send/ack/create are
	// write, receive/get/list are read. None are admin-only because
	// they're the customer-facing event surface; the existing
	// adminAllows email gate still narrows /v1/compute-nodes separately.
	mux.HandleFunc("POST /v1/apps/{slug}/invoke", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.invokeApp))))
	mux.HandleFunc("POST /v1/apps/{slug}/invoke/async", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.invokeAppAsync)))))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/send", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.queueSend)))))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/receive", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.queueReceive))))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/{id}/ack", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.queueAck)))))
	// Issue #394 — queue introspection. Read-only endpoints under
	// the same mount family. No lease is acquired; no row is mutated.
	mux.HandleFunc("GET /v1/apps/{slug}/queues/state", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queueState))))
	mux.HandleFunc("GET /v1/apps/{slug}/queues/peek", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queuePeek))))
	mux.HandleFunc("GET /v1/apps/{slug}/queues/dead_letter", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.queueDeadLetter))))
	// ADR-134 PR-C: replay a dead_letter queue row back to
	// pending. Idempotent-wrapped because a retried POST after a
	// network blip must not double-enqueue; the SDK mints
	// Idempotency-Key automatically on POST.
	mux.HandleFunc("POST /v1/apps/{slug}/queues/dead_letter/{id}/replay", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.queueDeadLetterReplay)))))
	mux.HandleFunc("POST /v1/apps/{slug}/delayed-tasks", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.delayedTaskCreate)))))
	mux.HandleFunc("GET /v1/invocations", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listInvocations))))
	mux.HandleFunc("GET /v1/invocations/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getInvocation))))
	// Issue #315 / tier-2 DX: replay a failed or dead_letter
	// invocation. POST + write scope (mirrors the write side of
	// async_invoke at line 888). Idempotent-wrapped because a
	// retried POST after a network blip must not double-enqueue —
	// the customer's CI / dashboard may issue the same replay
	// twice. The SDK adds Idempotency-Key automatically on POST
	// (client.go:146) and the apid wrapper stores it on the
	// request's first response.
	mux.HandleFunc("POST /v1/invocations/{id}/replay", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.replayInvocation)))))
	mux.HandleFunc("GET /v1/delayed-tasks/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.delayedTaskGet))))
	mux.HandleFunc("DELETE /v1/delayed-tasks/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.delayedTaskCancel))))

	// ADR-127 production debugger — read-only slice in PR-A. The
	// write-side (publisher → gRPC IncrementRequestTelemetry → apid
	// receiver → sqlc INSERT) lands in PR-B; this GET exists so
	// customers can already hit the endpoint and see rows once a
	// row source is configured. Plan-gated by DebugTelemetryEnabled.
	mux.HandleFunc("GET /v1/apps/{slug}/analytics/timeseries", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppRequestAnalyticsTimeseries)))
	mux.HandleFunc("GET /v1/apps/{slug}/analytics", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAppRequestAnalytics)))
	mux.HandleFunc("GET /v1/apps/{slug}/debug/requests", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.debugTelemetryListHandler))))
	mux.HandleFunc("GET /v1/apps/{slug}/debug/requests/{req_id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.debugTelemetryGetHandler))))
	// ADR-127 PR-B: regression banner feed (dashboard + CLI).
	mux.HandleFunc("GET /v1/apps/{slug}/debug/regressions", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.debugRegressionsHandler))))
	// ADR-127 PR-B: deployment-vs-deployment compare (POST body
	// holds the two deployment_ids + optional route filter).
	mux.HandleFunc("POST /v1/apps/{slug}/debug/compare", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.debugCompareHandler))))
	// ADR-127 PR-B: replay (PR-A2 of issue #72 wires the
	// mirror invocation; PR-B returns a stable "queued"
	// status so customer tooling can wire once).
	mux.HandleFunc("POST /v1/apps/{slug}/debug/requests/{req_id}/replay", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.debugReplayHandler))))

	// API keys. Minting and revoking keys are admin-only — a leaked
	// write-scoped key must not be able to grant itself more scopes.
	// Listing is read.
	mux.HandleFunc("GET /v1/keys", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listKeys))))
	mux.HandleFunc("POST /v1/keys", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.createKey))))
	mux.HandleFunc("DELETE /v1/keys/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.deleteKey))))
	// IAM-5 (issue #189): rotation endpoint. Admin-only because
	// rotation mints a new key that retains the predecessor's
	// scopes — limiting to admin matches the existing key-mint
	// gate (POST /v1/keys) and avoids a non-admin scope-expanding
	// via the rotation path.
	mux.HandleFunc("POST /v1/keys/{id}/rotate", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.requireStepUp(5*time.Minute)(s.rotateKey)))))

	// PR 6 (issue #190 / IAM-6 / ADR-061) — org-scoped API key surface.
	// Compose s.loadOrg (resolves X-Active-Org / ?org= to a membership)
	// followed by s.authLimited. No requireMFA / no requireScope: the
	// OrgAction closed-vocab (OrgActionCreateApiKey, OrgActionRevokeApiKey)
	// is the deny gate, and the membership's role (owner + admin) is
	// already stricter than api.ScopeAdmin. Cross-org probes collapse
	// to 404 in the Store layer (IDOR-safe). operationIds match the Go
	// SDK method names 1:1 so cmd/sdk-coverage compiles cleanly.
	mux.HandleFunc("GET /v1/orgs/{slug}/keys", s.authLimited(s.loadOrg(s.listOrgAPIKeys)))
	mux.HandleFunc("POST /v1/orgs/{slug}/keys", s.authLimited(s.requireMFA(s.loadOrg(s.requireStepUp(5*time.Minute)(s.createOrgAPIKey)))))
	mux.HandleFunc("GET /v1/orgs/{slug}/keys/{id}", s.authLimited(s.loadOrg(s.getOrgAPIKey)))
	mux.HandleFunc("DELETE /v1/orgs/{slug}/keys/{id}", s.authLimited(s.loadOrg(s.revokeOrgAPIKey)))
	mux.HandleFunc("POST /v1/orgs/{slug}/keys/{id}/rotate", s.authLimited(s.requireMFA(s.loadOrg(s.requireStepUp(5*time.Minute)(s.rotateOrgAPIKey)))))

	// Operator-only billing surface (issue #279). The admin allowlist
	// is enforced inside the handler (adminAllows), not just at the
	// middleware — a leaked admin key from a non-operator account
	// would otherwise be able to issue credits.
	mux.HandleFunc("POST /v1/admin/accounts/{id}/credits",
		s.authLimited(s.requireAdminMutation(s.issueCredit)))
	// Operator refund surface. Refunds move money, so this route uses the
	// strict operator-session + recent step-up policy. The handler applies the
	// FAAS_ADMIN_EMAILS allowlist and binds the Polar order to the target
	// account through the local invoice projection before calling the provider.
	mux.HandleFunc("POST /v1/admin/accounts/{id}/refunds",
		s.authLimited(s.requireAdminMutation(s.refundAccount)))

	// PR-D / ADR-012 §7 amendment: per-tenant GitHub App webhook
	// secret rotation. Same two-layer gate as issueCredit (scope +
	// email allowlist inside the handler) so a leaked admin key
	// from a non-operator account cannot rotate another tenant's
	// webhook secret.
	mux.HandleFunc("POST /v1/admin/github-webhook-secrets",
		s.authLimited(s.requireAdminMutation(s.handleSetGithubWebhookSecret)))

	// PR-P3: operator-facing billing surface. Same two-layer gate as
	// issueCredit above (scope + email allowlist inside the handler).
	// The Paddle catalog handlers type-assert to paddle.OpProvider; on
	// Stripe the assertion fails and the handler renders a uniform 501.
	// The reconcile endpoint works against any provider that
	// implements billing.Provider.ReconcileUsage (Stripe today; Paddle
	// returns ErrNotImplemented and maps to 501 via the handler).
	mux.HandleFunc("GET /v1/admin/billing-paddle-catalog",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.listPaddleCatalog)))
	mux.HandleFunc("POST /v1/admin/billing-paddle-catalog/sync",
		s.authLimited(s.requireAdminMutation(s.syncPaddleCatalog)))
	mux.HandleFunc("DELETE /v1/admin/billing-paddle-catalog",
		s.authLimited(s.requireAdminMutation(s.resetPaddleCatalog)))
	mux.HandleFunc("POST /v1/admin/billing-reconcile/{id}",
		s.authLimited(s.requireAdminMutation(s.reconcileAccount)))
	mux.HandleFunc("GET /v1/admin/billing-paddle-overage/preflight",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.paddleOveragePreflight)))

	// Operator observability backend (issue #777 / ADR-091). The
	// /v1/admin/obs/* surface gives the platform owner read-only
	// visibility into fleet state (users, apps, nodes, heartbeats).
	// Same two-layer gate as the rest of /v1/admin/* (admin scope
	// + email allowlist, the second enforced inside each handler
	// via s.adminAllows), plus MFA — the obs view exposes
	// secret-adjacent metadata (MFA enrollment, account email) and
	// the cost is a one-time TOTP per 24h session thanks to
	// step-up elsewhere (ADR-091 §"Two-layer gate confirmed").
	// All five routes are GETs; no s.idempotent wrapper needed
	// (matches /v1/compute-nodes read precedent).
	mux.HandleFunc("GET /v1/admin/obs/overview",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsOverview))))
	mux.HandleFunc("GET /v1/admin/obs/tenants",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsListTenants))))
	mux.HandleFunc("GET /v1/admin/obs/capacity",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsCapacity))))
	mux.HandleFunc("GET /v1/admin/obs/tenants/{id}/360",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsTenant360))))
	mux.HandleFunc("GET /v1/admin/obs/tenants/{id}",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsGetTenant))))
	mux.HandleFunc("GET /v1/admin/obs/tenants/{id}/activity",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsTenantActivity))))
	mux.HandleFunc("GET /v1/admin/obs/apps/{id}",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsAppDetail))))
	mux.HandleFunc("GET /v1/admin/obs/nodes",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsListNodes))))
	mux.HandleFunc("GET /v1/admin/obs/nodes/{name}/detail",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsNodeDetail))))
	mux.HandleFunc("GET /v1/admin/obs/nodes/{name}/heartbeats",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsNodeHeartbeats))))
	// PR #4 (ADR-092 §3.6) — per-node wake-latency quantiles.
	// Literal path /wake-latency sits before the SSE /events
	// route; Go 1.22+ mux disambiguates by exact match.
	mux.HandleFunc("GET /v1/admin/obs/nodes/wake-latency",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsNodeWakeLatency))))
	// PR #2 endpoints (ADR-091 §3.5 + §3.6). Same two-layer gate +
	// MFA as PR #1. Anomalies reads usage_minutes only; rate-limits
	// reads events + the in-process s.apiAuthLimiter snapshot.
	mux.HandleFunc("GET /v1/admin/obs/anomalies",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsAnomalies))))
	// PR #3 endpoints (ADR-091 §3.7). audit-log/search reads the
	// FK-free audit_log table (the regulator-grade evidence path);
	// events reads the live events table (the live diagnostic path);
	// nodes/events is the SSE mirror of the (deprecated) old
	// /v1/compute-nodes/events path. All three routes inherit the
	// same two-layer gate + MFA chain as PR #1 + PR #2.
	mux.HandleFunc("GET /v1/admin/obs/audit-log/search",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsAuditLogSearch))))
	mux.HandleFunc("GET /v1/admin/obs/events",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsEvents))))
	mux.HandleFunc("GET /v1/admin/obs/nodes/events",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsNodesEventsSSE))))
	mux.HandleFunc("GET /v1/admin/obs/rate-limits",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsRateLimits))))
	// P5 (operator-side observability mega-PR / Commit 7) —
	// builderd fleet heartbeat + build-queue depth. Today's row
	// count is zero: the underlying writer (pkg/builderd/heartbeat.go)
	// is deferred per the Commit 7 PR risk list — builderd does
	// not currently self-register a compute_nodes row at startup.
	mux.HandleFunc("GET /v1/admin/obs/builder-heartbeats",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsBuilderHeartbeats))))
	// Obs-Meta + Trace-IDs Mega-PR / C7 — meta-obs health
	// endpoint. Answers the operator's "is the obs stack itself
	// healthy?" question: a JSON snapshot of the audit_log write
	// counters, the operator_intent outcome-missing count, the
	// trace_id completeness ratio, and the Prometheus alert
	// firing count. Same two-layer gate (admin scope + MFA) as
	// the rest of /v1/admin/obs/*; no MFA bypass for this one
	// because the snapshot exposes alert-state metadata.
	mux.HandleFunc("GET /v1/admin/obs/health",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.obsHealthHandler))))

	// P2a + P2b + P2d — operator recovery primitives. All three
	// routes mount under requireScope(admin-only) so the admin
	// allowlist (s.adminAllows at compute_nodes.go:74-86) is the
	// only structural gate. The handlers themselves additionally
	// require ?confirm=true as a tripwire against operator
	// fat-fingering (matches the force-drain --yes ack at
	// commands_compute_nodes.go:249).
	mux.Handle("POST /v1/admin/instances/{id}/force-park",
		middleware.TraceID(s.authLimited(s.requireAdminMutation(s.postForcePark))))
	mux.Handle("POST /v1/admin/apps/{slug}/force-cold-boot",
		middleware.TraceID(s.authLimited(s.requireAdminMutation(s.postForceColdBoot))))
	// P2d — operator recovery primitive: force-restart kills a
	// wedged live instance + flips the deployment's latest warm +
	// init snapshots stale so the next Wake takes the cold-boot
	// branch. PR #1105 follow-on to PR #1099. Same auth posture
	// (admin scope + MFA + allowlist) and ?confirm=true tripwire
	// as force-park / force-cold-boot above. The TraceID middleware
	// wraps the chain on the same axis as the two above so every
	// inbound force-action carries the same observability trace_id.
	mux.Handle("POST /v1/admin/instances/{id}/force-restart",
		middleware.TraceID(s.authLimited(s.requireAdminMutation(s.postForceRestart))))
	// PR #1099 P2 redesign: polling endpoint for the
	// operator_intents rows. NO MFA — mirrors getFireCronRequest
	// at cmd/apid/handlers_fire_cron_request.go:38-83 because the
	// operator is already authenticated via the initial POST; MFA
	// at the polling endpoint would re-auth without operational
	// benefit. Admin scope + s.adminAllows are the only access
	// controls (the admin scope gate is the load-bearing IDOR
	// closure — fleet-level intents with account_id=NULL are
	// visible to any admin).
	mux.HandleFunc("GET /v1/admin/operator-intents/{id}",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.getOperatorIntent)))
	// P2c (reclaim-stuck-build) — fleet-level sweep that calls
	// state.Store.SweepStuckRunningBuilds directly (per user
	// decision, NO builderd gRPC server). ?older_than= is
	// clamped to [1m, 60m] so a fat-fingered "1ns" cannot sweep
	// in-flight builds.
	mux.HandleFunc("POST /v1/admin/builds/sweep-stuck",
		s.authLimited(s.requireAdminMutation(s.postSweepStuckBuilds)))
	// Compute-node lifecycle controls. They are deliberately separate from
	// the read-only /obs namespace and require both MFA and confirm=true.
	mux.HandleFunc("POST /v1/admin/ops/accounts/{id}/suspend",
		s.authLimited(s.requireAdminMutation(s.postObsAccountSuspend)))
	mux.HandleFunc("POST /v1/admin/ops/accounts/{id}/restore",
		s.authLimited(s.requireAdminMutation(s.postObsAccountRestore)))
	mux.HandleFunc("POST /v1/admin/ops/accounts/{id}/revoke-sessions",
		s.authLimited(s.requireAdminMutation(s.postObsAccountRevokeSessions)))
	mux.HandleFunc("POST /v1/admin/ops/nodes/{name}/drain",
		s.authLimited(s.requireAdminMutation(s.postObsNodeDrain)))
	mux.HandleFunc("POST /v1/admin/ops/nodes/{name}/force-drain",
		s.authLimited(s.requireAdminMutation(s.postObsNodeForceDrain)))
	mux.HandleFunc("POST /v1/admin/ops/nodes/{name}/activate",
		s.authLimited(s.requireAdminMutation(s.postObsNodeActivate)))

	// ADR-132 — typed runtime configuration. GET is MFA-gated; PATCH and
	// rollback use the strict operator-session policy
	// because the catalog includes security-adjacent deployment posture. Hot
	// values apply in the request; graceful values become durable operations.
	// Filesystem/bootstrap settings remain visible as deployment-managed
	// entries.
	mux.HandleFunc("GET /v1/admin/config",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.adminRuntimeConfigList))))
	mux.HandleFunc("PATCH /v1/admin/config/{key}",
		s.authLimited(s.requireAdminMutation(s.adminRuntimeConfigPatch)))
	mux.HandleFunc("POST /v1/admin/config/{key}/rollback",
		s.authLimited(s.requireAdminMutation(s.adminRuntimeConfigRollback)))
	mux.HandleFunc("GET /v1/admin/config-operations/{id}",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.adminRuntimeConfigOperationGet)))
	mux.HandleFunc("GET /v1/admin/config/{key}/revisions",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.adminRuntimeConfigRevisions)))

	// IAM-4 (ADR-035) — auth audit log surface. Read-only; the
	// events table is append-only (spec §5). Scope gating: session
	// cookie (implicitly admin) or any API key carrying {admin,
	// apps:read} (api.ScopesReadSurface). Compute-node admin-only
	// routes are gated separately below.
	mux.HandleFunc("GET /v1/audit-events", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAuditEvents))))
	mux.HandleFunc("GET /v1/audit-events/{id}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getAuditEvent))))

	// Issue #755 / PR-6 — audit_log dashboard surface. Reads the
	// FK-free audit_log table (migrations/00163_audit_log.sql),
	// distinct from /v1/audit-events which reads the live events
	// table. Two routes by audience:
	//
	//   - /v1/audit-log: customer-scoped, MFA + apps:read; pinned
	//     to the calling account's ID inside the handler. Same
	//     scope posture as /v1/audit-events.
	//   - /v1/audit-log/all: operator-only, admin scope; can read
	//     across accounts and opt into account_id IS NULL rows
	//     with ?include_anonymous=true. No MFA gate — admin
	//     sessions and admin API keys are already MFA-gated upstream
	//     at session issue time.
	mux.HandleFunc("GET /v1/audit-log", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listAuditLog))))
	mux.HandleFunc("GET /v1/audit-log/all", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.listAuditLogAll)))

	// ADR-089 PR-C — background re-seal progress (operator-only,
	// FAAS_REKEY_ENABLED opt-in). Same gate as /v1/audit-log/all
	// (admin scope, no MFA — the operator session is already
	// MFA-gated upstream). The handler returns 503
	// code="rekey_disabled" when the runner is nil (the flag is
	// unset on this host), so the route stays mounted and the
	// operator's dashboard distinguishes "feature is off" from
	// "feature is on and the table is empty". See
	// handlers_rekey.go for the handler body.
	mux.HandleFunc("GET /v1/admin/secrets/rekey-progress",
		s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.getRekeyProgress)))

	// Issue #517 / PR-C / ADR-064: customer-facing wake-timeline
	// surface. Sub-resource of /v1/apps/{slug} — same auth chain
	// as the rest of the /v1/apps/* read surface, same §12
	// per-app rate-limit budget. The query keys on the partial
	// index events_wake_id_idx (migrations/00113) for O(frames)
	// latency regardless of events table size.
	mux.HandleFunc("GET /v1/apps/{slug}/wakes/{wake_id}/timeline", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listWakeTimeline))))

	// Customer secrets (spec §11/G2). Plaintext VALUE flows through PUT
	// over TLS; sealed server-side by handlers_secrets.go.
	mux.HandleFunc("GET /v1/apps/{slug}/secrets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listSecrets))))
	// Account-scoped sealed-secret list (issue #393). Each row
	// carries the owning app's id and slug so the dashboard can
	// render "foo-app / DATABASE_URL" without a parallel /v1/apps
	// round-trip. Cursor: ?before= is the (app_slug, key) pair
	// encoded as "<slug>|<key>" (the SQL splits it back via
	// split_part). Plaintext NEVER appears in this handler's
	// output (same invariant the per-app handler upholds).
	mux.HandleFunc("GET /v1/secrets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listSecretsForAccount))))
	mux.HandleFunc("PUT /v1/apps/{slug}/secrets/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.setSecret))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/secrets/{key}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.deleteSecret))))
	// Per-secret rotation (ADR-089 PR-B). Distinct verb from PUT so
	// dashboards filtering on audit kind='secret.rotated' see
	// value-replacement events but not first-time sets. Same
	// scope + MFA posture as secrets:write — losing the new
	// plaintext is the loss-bearing case; the old plaintext was
	// never in a loss position because the row being overwritten
	// is already in the customer's hand.
	mux.HandleFunc("POST /v1/apps/{slug}/secrets/{key}/rotate", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.rotateAppSecret))))

	// Per-app private-registry Basic Auth (issue #461 / ADR-062).
	// Password is sealed server-side under namespace "registry_creds";
	// the same host.age recipient apid loads for app secrets is
	// reused — same key file, same in-process lifetime. imaged
	// unseals transiently in the pull path; password NEVER leaves
	// the store plaintext. MFA-gated because the credential can
	// unlock a deploy pipeline.
	mux.HandleFunc("GET /v1/apps/{slug}/registry-credentials",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesRegistryCredentialsReadSurface...)(s.listRegistryCredentials))))
	mux.HandleFunc("PUT /v1/apps/{slug}/registry-credentials",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesRegistryCredentialsWriteSurface...)(s.setRegistryCredential))))
	mux.HandleFunc("DELETE /v1/apps/{slug}/registry-credentials",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesRegistryCredentialsWriteSurface...)(s.deleteRegistryCredential))))

	// Customer env vars (issue #395 / ADR-045). Plaintext VALUE flows
	// through PUT over TLS; persisted as-is (no seal step) by
	// handlers_env.go. env:write is NOT MFA-gated because env vars are
	// explicitly non-sensitive runtime config — the secret surface is
	// the credential store and stays MFA-locked.
	mux.HandleFunc("GET /v1/apps/{slug}/env", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listEnv)))
	mux.HandleFunc("PUT /v1/apps/{slug}/env/{key}", s.authLimited(s.requireScope(api.ScopesEnvWriteSurface...)(s.setEnv)))
	mux.HandleFunc("DELETE /v1/apps/{slug}/env/{key}", s.authLimited(s.requireScope(api.ScopesEnvWriteSurface...)(s.deleteEnv)))

	// ADR-117 PR-C: env-diff matrix endpoint. Read-only; not
	// MFA-gated because the surface emits no secret plaintext
	// (only present / value_hash / value-for-env signals). The
	// same ScopesReadSurface gate as listEnv.
	mux.HandleFunc("GET /v1/apps/{slug}/env-diff", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.envDiff)))

	// Data upstreams (ADR-098 §D4 / PR-B). The full handler family is
	// gated on s.dataPlacementEnabled (FAAS_DATA_PLACEMENT=1); when
	// the flag is off, each handler returns 402 plan_feature_gated so
	// pre-PR-B callers see the exact same wire shape as before. The
	// read endpoints (GET) use the read scope; the write endpoints
	// (PUT/DELETE) use ScopesUpstreamWriteSurface, mirroring the env
	// surface's split.
	mux.HandleFunc("GET /v1/apps/{slug}/upstreams", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listUpstreams)))
	mux.HandleFunc("GET /v1/apps/{slug}/upstreams/history", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getUpstreamHistory)))
	mux.HandleFunc("GET /v1/apps/{slug}/upstreams/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getUpstream)))
	mux.HandleFunc("PUT /v1/apps/{slug}/upstreams", s.authLimited(s.requireScope(api.ScopesUpstreamWriteSurface...)(s.createUpstream)))
	mux.HandleFunc("DELETE /v1/apps/{slug}/upstreams/{id}", s.authLimited(s.requireScope(api.ScopesUpstreamWriteSurface...)(s.deleteUpstream)))

	// Usage.
	// Usage endpoints are narrower than the read surface — a deploy-write
	// CI key doesn't need them. usage:read is the right knob here.
	mux.HandleFunc("GET /v1/usage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.getUsage))))
	mux.HandleFunc("GET /v1/usage/summary", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.usageSummary))))
	mux.HandleFunc("GET /v1/usage/daily", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.usageDaily))))
	mux.HandleFunc("GET /v1/usage/storage", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.usageStorage))))
	// Billing history (issue #259). Listing one's own invoices is the
	// same access tier as usage/summary — usage:read is enough.
	// Wrapped in requireMFA for consistency with the other
	// session-cookie routes (IAM-2 / issue #186).
	mux.HandleFunc("GET /v1/invoices", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.listInvoices))))

	// Billing portal link (issue #253). Read-only — the URL itself
	// does not mutate anything; the customer-facing mutations live
	// inside the provider-hosted portal that the URL points to. Same
	// access tier as usage/invoices (usage:read scope) but NO MFA
	// gate: the portal is a read in HTTP terms, but it creates an
	// authenticated Stripe session where the customer can mutate billing.
	// Email verification therefore applies before the redirect leaves Gregale.
	mux.HandleFunc("GET /v1/billing/portal", s.authLimited(s.requireScope(api.ScopesUsageReadSurface...)(s.requireVerifiedEmail(s.getBillingPortal))))

	// Billing retry (issue #242). Closes the customer-trust lie in
	// pkg/mail/account.go:107,150 (the dunning email promises
	// `faas billing retry`; this is what it calls). Session-cookie
	// auth + usage:read scope; the destructive nature of retry is
	// bounded (a single charge attempt against the saved card).
	// MFA-gated for parity with changePlan — retry touches money.
	mux.HandleFunc("POST /v1/billing/retry", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.requireVerifiedEmail(s.postBillingRetry)))))

	// Billing cancel (issue #242). Sets cancel_at_period_end on
	// the account's subscription; account keeps running until
	// period end then downgrades to Free (spec §4.7). Destructive
	// but session-cookie-only — the CLI front-loads the typed-
	// confirm gate from PR #782 ("cancel subscription"). Headless
	// callers can wire their own confirm. MFA-gated for parity
	// with changePlan.
	mux.HandleFunc("POST /v1/billing/cancel", s.authLimited(s.requireMFA(s.requireScope(api.ScopesUsageReadSurface...)(s.requireVerifiedEmail(s.postBillingCancel)))))

	// Credit consumption reducer (issue #279 PR-C). Admin-only +
	// MFA-gated — operator action that mutates money (spec §11). The
	// `idempotent` middleware replays a prior 200 on duplicate
	// Idempotency-Key; the reducer itself is also idempotent at the DB
	// level via the partial unique index on credit_ledger
	// (provider_invoice_id, credit_id) — migration
	// 00058_credit_consumption.sql.
	mux.HandleFunc("POST /v1/invoices/{id}/consume-credits",
		s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.consumeInvoiceCredits)))))

	// Account-scoped deployments list (M7.5 dashboard).
	mux.HandleFunc("GET /v1/deployments", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listDeployments))))

	// MFA (IAM-2, issue #186). Five POST endpoints; all on the
	// admin-only scope set because the dashboard never exposes
	// them to non-admin keys. /enroll is NOT wrapped in
	// s.idempotent because the secret + QR + recovery codes
	// must be returned exactly once; replaying a cached response
	// would re-reveal plaintexts the customer already consumed.
	// The /confirm, /verify, /recover, /disable routes ARE
	// idempotent (no body-shape side effect).
	mux.HandleFunc("POST /v1/account/mfa/enroll", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.mfaEnroll))))
	mux.HandleFunc("POST /v1/account/mfa/confirm", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.mfaConfirm)))))
	mux.HandleFunc("POST /v1/account/mfa/verify", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.mfaVerify)))))
	mux.HandleFunc("POST /v1/account/mfa/recover", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.mfaRecover)))))
	mux.HandleFunc("POST /v1/account/mfa/disable", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.mfaDisable)))))

	// Stripe webhook (no auth — Stripe signs requests; for M5 we accept
	// unsigned and trust the network boundary; ADR-007 hardening later).
	mux.HandleFunc("POST /v1/webhooks/stripe", s.stripeWebhook)
	// Paddle webhook (no auth — Paddle signs requests; same trust
	// model as the Stripe path). The handler returns 503 if
	// FAAS_BILLING_PROVIDER != paddle so a misrouted POST from Paddle
	// to a Stripe-only box is visible in logs rather than silently
	// 200'd. Mounted unconditionally so the same apid binary works
	// on either provider; the handler's 503 covers the wrong-provider
	// case.
	mux.HandleFunc("POST /v1/webhooks/paddle", s.paddleWebhook)
	// Polar webhook (no auth — Standard Webhooks signs requests). Mounted
	// unconditionally so one apid binary can serve any configured provider.
	mux.HandleFunc("POST /v1/webhooks/polar", s.polarWebhook)

	// Resend bounce/complaint/delivery webhook ingress (issue #246
	// acceptance item 8). Mounted next to the Paddle route — both
	// are HMAC-authenticated public POSTs and the fail-closed 503
	// pattern is identical. The handler reads the raw body, verifies
	// the Svix envelope, replay-checks against webhookdedupe, and
	// dispatches to meter.BounceHandler. No auth middleware — the
	// HMAC *is* the trust boundary.
	mux.HandleFunc("POST /v1/webhooks/resend", s.resendWebhook)

	// Operator admin surface (issue #98 / ADR-028). Auth lives in
	// s.adminAllows (email allowlist via FAAS_ADMIN_EMAILS); handlers
	// 403 every request when the allowlist is empty. The scope
	// check is layered on top so a non-admin key never reaches the
	// adminAllows handler. authLimited wraps the per-IP bucket so
	// the routes share the spec §11 10/min/IP budget with the rest
	// of /v1/* — a brute-force on admin routes costs the attacker
	// the same budget they'd burn trying customer keys.
	mux.HandleFunc("GET /v1/compute-nodes", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.listComputeNodes))))
	mux.HandleFunc("POST /v1/compute-nodes", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.createOrUpdateComputeNode)))))
	mux.HandleFunc("DELETE /v1/compute-nodes/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.deleteComputeNode))))
	// Workstream B (issue #1184 / ADR-137): drain handler.
	// POST /drain CAS-transitions the node's lifecycle to
	// 'draining' (the recovery arbiter owns the actual
	// instance migration / recreation); GET /drain returns
	// progress (lifecycle + timestamps + drained count).
	// ?wait=1 on POST blocks up to 50s for completion. Auth
	// chain mirrors the rest of /v1/compute-nodes (admin +
	// MFA). The handler lives in handlers_compute_nodes_drain.go
	// — keeping it next to listComputeNodes et al. is the
	// §4 ownership invariant (the apid customer-facing admin
	// CRUD plane, not the legacy /v1/admin/ops/* operator-tools
	// plane).
	mux.HandleFunc("POST /v1/compute-nodes/{name}/drain", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.postComputeNodeDrain))))
	mux.HandleFunc("GET /v1/compute-nodes/{name}/drain", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.getComputeNodeDrainProgress))))
	// CP-1: heartbeat history (schedd Heartbeat.Tick writes; the
	// endpoint reads from the append-only compute_node_heartbeats
	// table). Auth chain mirrors the rest of /v1/compute-nodes.
	mux.HandleFunc("GET /v1/compute-nodes/{name}/heartbeats", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.listComputeNodeHeartbeats))))
	// CP-1: SSE stream on compute_node_changed. Operator-only,
	// unfiltered (no per-account scoping — operators want raw
	// fleet upserts, not the dashboard's mixed-workload feed).
	mux.HandleFunc("GET /v1/compute-nodes/events", s.authLimited(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.withDeprecation(s.computeNodeEventsHandler)))))

	// M7.5 SSE live-update (ADR-011). Handles session-cookie OR
	// API-key auth itself — the cookie path is for the dashboard,
	// the Bearer path for the CLI. NOT mounted behind s.auth so the
	// cookie flow works without an API-key round trip.
	mux.Handle("GET /v1/events", s.dashboardChain(s.eventsHandler(s.log)))

	// Google OAuth 2.0 Auth (issue #165 PR #2, ADR-032). Routes
	// share the dashboard auth bucket so the §11 10/min/IP budget
	// applies to the OAuth flow as well as the password form.
	mux.Handle("GET /v1/auth/google", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.renderGoogleAuthRedirect)))
	mux.Handle("GET /v1/auth/google/callback", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.handleGoogleOAuthCallback)))

	// GitHub OAuth dashboard login (issue #165 PR #2, ADR-032).
	// Not the same as /oauth/callback (the GitHub App install-bind
	// flow at handlers_oauth.go). Shares the dashboard auth bucket
	// with the other auth entrypoints so brute-force can't isolate
	// one provider's flow.
	mux.Handle("GET /v1/auth/github", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.renderGitHubAuthRedirect)))
	mux.Handle("GET /v1/auth/github/callback", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.handleGitHubOAuthCallback)))
	// Issue #419 / ADR-046: sign-in OAuth capability signal for the
	// dashboard. Behind sessionAuth so the response doesn't reveal
	// the configured-provider set to random scanners; behind
	// dashboardChain (not dashboardAuthChain) because the route
	// only returns a 200 with the per-provider enabled flag — it
	// isn't a brute-force surface, and not counting it keeps the
	// §11 auth bucket honest. s.renderAuthCapabilities reads
	// s.oauthConfig (loaded once at boot via (*server).WithOAuthConfig).
	mux.Handle("GET /v1/auth/capabilities", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.renderAuthCapabilities))))

	// Dashboard surface (M7.5, ADR-011). Lives behind gatewayd-public's
	// /dashboard/* reverse-proxy (spec §11 single-public-listener).
	//
	// Slice 3 wires the magic-link auth flow:
	//   GET  /login            — render the email form
	//   POST /login            — mint token + email it
	//   GET  /auth/verify      — consume token, set session cookie
	//   POST /logout           — clear cookie
	//
	// All other /dashboard/* sit behind sessionAuth → handlers_dashboard.
	auth := &authHandlers{srv: s, log: s.log, loginTTL: s.loginTTL, mailer: s.mailer, domain: s.domain}
	mux.Handle("GET /login", s.dashboardAuthChain(middleware.AuthLimitConfig{
		// CountEveryAttempt: /login returns 200 even for unknown emails
		// (anti-enumeration, see handlers_auth.go), so a 401-only limiter
		// would miss the brute-force signal. Count every attempt instead.
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(auth.renderLoginForm)))
	// PR #2 (issue #165) replaces the X-Dashboard-Key fallback path
	// with email + password (Argon2id). The new postLoginEmail wires
	// the spec §11 anti-enumeration pad against the same
	// dashboardAuthLimiter as the rest of the auth surface — a
	// brute-force on /login (password path) burns the same bucket as
	// /signup, /login/forgot, /v1/auth/google, and /v1/auth/github.
	mux.Handle("POST /login", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postLoginEmail)))
	mux.Handle("POST /signup", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postSignup)))
	mux.Handle("POST /login/forgot", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postForgotPassword)))
	mux.Handle("GET /auth/reset", s.dashboardAuthChain(middleware.AuthLimitConfig{
		// 410 on invalid/expired token; count every attempt so an
		// attacker can't enumerate token shapes faster than the
		// §11 budget.
		CountStatuses: []int{middleware.CountEveryAttempt, http.StatusGone},
	}, http.HandlerFunc(s.renderResetForm)))
	mux.Handle("POST /auth/reset", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt, http.StatusGone},
	}, http.HandlerFunc(s.postReset)))
	// POST /dashboard/account/set-password is the authed opt-in for
	// OAuth-only customers and the change-password path for everyone
	// else. Behind sessionAuth so the call is anchored to a known
	// account. NOT behind requireStepUpHandler (ADR-140): the only
	// writer of a step-up stamp is TOTP verify, so a blanket gate
	// locked out the OAuth-only, no-MFA customers the route exists
	// for. The handler picks the proof itself — fresh step-up,
	// current_password, or a 403 when an explicit MFA policy is
	// pending or the account has enrolled MFA. Because
	// current_password is a credential check, the
	// route shares the dashboard's per-IP failure bucket with /login
	// (§11: 10/min/IP, counting the 401s) so a stolen session is not
	// a free oracle for guessing the password.
	mux.Handle("POST /dashboard/account/set-password", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{http.StatusUnauthorized},
	}, s.sessionAuth(http.HandlerFunc(s.postSetPassword))))
	mux.Handle("GET /auth/verify", s.dashboardAuthChain(middleware.AuthLimitConfig{
		// /auth/verify 401s on unknown tokens AND 410s on consumed tokens;
		// count both so an attacker can't cycle through one-time tokens
		// faster than the spec §11 10/min/IP budget.
		CountStatuses: []int{http.StatusUnauthorized, http.StatusGone},
	}, http.HandlerFunc(auth.verify)))
	mux.Handle("GET /v1/auth/verify-email", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt, http.StatusGone},
	}, http.HandlerFunc(s.verifyEmail)))
	// Programmatic auth surface (issue #311). JSON-only, bearer-key
	// CLI path — three endpoints that share the spec §11 10/min/IP
	// authlimit bucket with the rest of the cookie auth surface.
	// Distinct from the dashboard /login|/signup cookie surface by
	// response shape (ProgrammaticAuthResponse carries api_key, no
	// Set-Cookie header) and by being behind the same authlimit
	// counter so a brute-force on /v1/auth/login burns the same
	// bucket as /login.
	mux.Handle("POST /v1/auth/signup", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postV1AuthSignup)))
	mux.Handle("POST /v1/auth/login", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postV1AuthLogin)))
	mux.Handle("POST /v1/auth/signup/magic-link", s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(s.postV1AuthSignupMagicLink)))
	mux.Handle("POST /logout", s.dashboardChain(http.HandlerFunc(auth.logout)))
	// /oauth/callback is the GitHub App install redirect target
	// (review finding #1+#2 closure for the M7.5 OAuth path).
	// Behind sessionAuth so the bind row is anchored to the
	// logged-in account; behind dashboardChain so it shares the
	// §11 middleware stack with the rest of the cookie-bearing
	// surface. NOT behind s.auth — that's API-key auth, not
	// session-cookie auth, and the redirect URL is hit by a
	// browser.
	mux.Handle("GET "+oauthCallbackPath, s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.renderOAuthCallback))))
	mux.Handle("GET /dashboard/", s.dashboardChain(s.sessionAuth(s.dashboardHandler(s.log))))
	mux.Handle("GET /dashboard", s.dashboardChain(s.sessionAuth(s.dashboardHandler(s.log))))

	// PR-B bind picker UX (handlers_install_github.go). Both routes
	// are cookie-session-authenticated (NOT API-key auth — the
	// dashboard renders them, no Bearer token is in scope) and
	// share the §11 middleware stack via dashboardChain. They live
	// under /v1/* so cmd/gatewayd-internal/proxy.go:isApidPath already
	// forwards them; the §11 anti-takeover proof (session.github_login
	// == install.account.login) is enforced in the handlers.
	mux.Handle("POST /v1/install/repos/list", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.listInstallableRepos))))
	mux.Handle("POST /v1/apps/{slug}/install/bind", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.bindAppToRepo))))
	// Issue #961 / Mega-B PR-3 — GET /v1/templates is the dashboard's
	// source of truth for the template catalog (handlers_templates.go).
	// Mirrors cmd/gregale/templates.Names without importing the CLI's
	// main package; the dashboard and the CLI read the same
	// 15-entry list through independent paths.
	mux.Handle("GET /v1/templates", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.listTemplates))))

	// PR-C: /oauth/code-callback is the user-to-server OAuth callback
	// (the "Connect GitHub" button flow). Sibling of /oauth/callback
	// (the GitHub App install callback). Like /oauth/callback, it
	// sits behind sessionAuth + dashboardChain — cookie-scoped,
	// no Bearer token, same §11 middleware stack. See
	// handlers_oauth_code_callback.go for the full rationale on
	// why this is a separate route from /oauth/callback.
	mux.Handle("GET "+oauthCodeCallbackPath, s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.renderOAuthCodeCallback))))
	// /dashboard/install/connect mints the CSRF state cookie and
	// redirects the dashboard's "Connect GitHub" button to GitHub's
	// authorize URL. POST so it can't be triggered by an opportunistic
	// <img src=…> (defense-in-depth against CSRF-on-GET).
	mux.Handle("POST /dashboard/install/connect", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.startConnectGitHub))))

	// G6 dashboard delete/restore (spec §17 G6, ADR-021). Both POSTs
	// require the confirm_token form field (validated inside the
	// handler) and sit behind sessionAuth so the call is anchored to
	// the logged-in account. The handlers reuse scheduleDeletion /
	// cancelDeletion from handlers_account.go so audit, email, and
	// notification side-effects match the REST API path bit-for-bit.
	mux.Handle("POST /dashboard/account/delete", s.dashboardChain(s.sessionAuth(s.requireStepUpHandler(5*time.Minute)(http.HandlerFunc(s.dashboardDelete)))))
	mux.Handle("POST /dashboard/account/restore", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardRestore))))
	// Issue #248 slice A: revoke an account-owned API key from the
	// dashboard. The handler verifies its dedicated named CSRF cookie and
	// a typed key-prefix confirmation before calling the REST revocation core.
	mux.Handle("POST /dashboard/account/keys/{id}/delete", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardDeleteKey))))
	// Issue #248 slice B: route account-page plan changes through the
	// provider-confirmed checkout or portal flow. The local plan changes
	// only after the provider webhook confirms the operation.
	mux.Handle("POST /dashboard/account/plan", s.dashboardChain(s.sessionAuth(s.requireVerifiedEmailHandler(s.requireStepUpHandler(5*time.Minute)(http.HandlerFunc(s.dashboardAccountPlan))))))
	// Issue #561 — spend cap self-service form. Same CSRF-envelope
	// envelope shape as dashboardDelete (see cmd/apid/dashboard_delete.go).
	// Step-up gate matches dashboardDelete: a hostile actor with a
	// stolen post-MFA-clear browser session (PR #653 threat change 6,
	// ADR-077) cannot silently disable the customer's cap. The 5-minute
	// TTL is the standard sensitive-op window (review finding #10).
	mux.Handle("POST /dashboard/raise-overage-cap", s.dashboardChain(s.sessionAuth(s.requireStepUpHandler(5*time.Minute)(http.HandlerFunc(s.dashboardRaiseOverageCap)))))
	// Free → paid hosted-checkout hand-off (dashboard_upgrade.go). Same
	// step-up posture as PATCH /v1/account/plan: starting a checkout is
	// a billing mutation even though the plan only flips on the webhook.
	mux.Handle("POST /dashboard/upgrade", s.dashboardChain(s.sessionAuth(s.requireVerifiedEmailHandler(s.requireStepUpHandler(5*time.Minute)(http.HandlerFunc(s.dashboardUpgrade))))))
	// Issue #791 PR-E / ADR-090 closure — fire-now from the
	// dashboard's cron section. Same CSRF-envelope shape as the
	// form POSTs above (the form-render path in renderAppDetail
	// mints the token via IssueForAuthenticated; the handler
	// verifies via VerifyAuthenticated). No requireStepUp — a
	// fire-now is the same intent as a cron firing on schedule,
	// so the MFA-then-fire posture is correct, not step-up.
	// Parses the cron id out of the URL slug inside the handler
	// (Go 1.22+ mux needs concrete segment counts; the
	// /crons/{id}/fire-now suffix is the path tail).
	mux.Handle("POST /dashboard/apps/{slug}/crons/{id}/fire-now", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardFireCron))))
	// Issue #248 slice C: app-detail rollback form. It uses a dedicated
	// named CSRF cookie and the same rollback core as the REST endpoint.
	mux.Handle("POST /dashboard/apps/{slug}/rollback", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardRollback))))
	// ADR-117 §Production-ready follow-on, C4 — dashboard-side
	// retry form handler. The form is <form method="POST"> (not
	// XHR), so the endpoint takes the CSRF sealed-envelope path
	// instead of the v1 Bearer-key envelope. The handler does the
	// same two-step IDOR probe as cmd/apid/handlers_retry.go and
	// calls s.store.RetryDeploymentFromStage; on success it
	// redirects to /dashboard/apps/{slug}/deployments/<new-id>
	// so the customer's next page-load sees the live SSE stream
	// for the fresh row.
	mux.Handle("POST /dashboard/apps/{slug}/deployments/{id}/retry", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardRetryDeployment))))
	// Issue #1233 / ADR-123 — alert-preset enable from the
	// dashboard's preset grid. Same CSRF-envelope shape as
	// dashboardFireCron (form-encoded body, action=
	// "enable_alert_preset"). The handler delegates to
	// enableAlertPresetFromForm after CSRF + auth so the JSON
	// path (POST /v1/apps/{slug}/alert-presets/{name}/enable)
	// and the dashboard path share a single guard order.
	mux.Handle("POST /dashboard/apps/{slug}/alert-presets/{name}/enable", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardEnablePreset))))
	// Issue #1233 / ADR-123 PR-C commit 2 — "Send test alert" form
	// twin of the JSON route at line 1418. dashboardChain wraps
	// the cookie-auth middleware + CSRF envelope verifier, then
	// sessionAuth confirms the cookie. The handler delegates to
	// sendTestAlertPresetCore after CSRF so the JSON path and the
	// dashboard path share a single guard order (slug + name regex
	// gating, then catalog row gate, then rule lookup, then
	// dispatch). Mirrors the enable-path dashboardChain structure
	// at line 2148.
	mux.Handle("POST /dashboard/apps/{slug}/alert-presets/{name}/test", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardSendTestAlertPreset))))
	// GET /dashboard/account/export is the session-authenticated twin
	// of the REST /v1/account/export. The dashboard template's "Download
	// JSON export" link points here because the REST endpoint requires
	// a Bearer API key the dashboard never has. The handler in
	// dashboard_delete.go reuses gatherExport so the wire shape is
	// identical to the REST path.
	//
	// PR #83 review #14: cap at 3/min/IP via the dedicated
	// dashboardExportLimiter. /v1/* routes share apiAuthLimiter
	// (401-counter); this route lives outside /v1/* AND outside
	// the dashboard auth surface, so it needs its own bucket —
	// dashboardAuthLimiter counts auth-failures, not every attempt
	// here, and using it would either under-or-over-share. The
	// limiter sits OUTSIDE sessionAuth so a 4th hit doesn't waste
	// a cookie round-trip; the 429 body is the same plain-text
	// shape AuthLimit emits so dashboards handle it identically.
	mux.Handle("GET /dashboard/account/export", s.dashboardChain(
		middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{
			Log:           s.log,
			Window:        time.Minute,
			MaxFailures:   3,
			CountStatuses: []int{middleware.CountEveryAttempt},
		}, s.dashboardExportLimiter)(s.sessionAuth(http.HandlerFunc(s.dashboardExport)))))
	// Session-authed twin of GET /v1/account/dpa. The dashboard chrome
	// is the right surface when a customer reads the DPA in context
	// (vs. the public route, which streams raw markdown for prospects
	// and pre-signup browsing). Same file, different envelope.
	mux.Handle("GET /dashboard/account/dpa", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardDPA))))

	// ADR-124: dashboard affected-workloads preview. Two POST routes
	// (multipart) + one GET route inside the dashboardHandler
	// dispatcher (handlers_dashboard.go:158). The preview template
	// posts back to /preview (re-render with the partition populated)
	// or to /preview/apply (commit with the exclusion list). CSRF
	// envelopes are minted at GET time (handlers_dashboard_project_preview.go).
	// Note: these routes accept multipart bodies up to the same
	// 100 MB / 250 MB cap the v1 /v1/projects/scan path enforces
	// (pkg/api/limits.go) — the apid request envelope carries that
	// limit so a curl-upload bypass of the dashboard form still hits
	// the same cap.
	mux.Handle("POST /dashboard/projects/{slug}/preview", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.submitProjectPreviewDispatch))))
	mux.Handle("POST /dashboard/projects/{slug}/preview/apply", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.applyProjectPreviewDispatch))))

	// Status page (spec §12 public status page). Unauthenticated by
	// design — prospects read it before sign-up, customers during
	// incidents. Carries no tenant data; only fleet-wide SLIs. Mounted
	// on the public mux so the operator's HTTPS path serves it.
	mux.HandleFunc("GET /status", s.statusHandler)
	mux.HandleFunc("GET /status/slo.json", s.statusJSONHandler)

	// CLI auth device-code flow (spec §2.2). Code minting and exchange
	// are anonymous because the CLI has no credential yet; the browser
	// claim is deliberately session-authenticated so it cannot choose an
	// arbitrary email/account. The public console forwards /cli-auth to
	// this listener, keeping the browser on the frontend origin.
	cli := &cliAuthHandlers{srv: s, log: s.log, domain: s.domain, urlBase: s.cliAuthURLBase}
	mux.Handle("POST /v1/cli-auth/code", s.cliAuthChain(http.HandlerFunc(cli.mintCliAuthCode)))
	mux.Handle("POST /v1/cli-auth/exchange", s.cliAuthChain(http.HandlerFunc(cli.exchangeCliAuthCode)))
	// Dashboard-side GET and POST both require a normal session. GET
	// redirects to /login?next=… when the browser is signed out; the
	// login redirect preserves the code query so the authorization page
	// can resume after sign-in. POST uses its own bucket so a customer
	// retrying `faas login` from a shared NAT doesn't burn the /login
	// budget.
	mux.Handle("GET "+cliAuthPath, s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, s.sessionAuth(http.HandlerFunc(cli.renderCliAuthPage))))
	mux.Handle("POST "+cliAuthPath, s.cliAuthSubmitChain(s.sessionAuth(http.HandlerFunc(cli.postCliAuthPage))))

	// Loopback infra probe (issue #85). gatewayd-internal forwards /healthz to
	// apid through the apidProxy chain, so this is what the
	// deploy/digitalocean CD smoke test and the v1
	// deploy/digitalocean/bootstrap.sh health check (RETIRED
	// 2026-08-15 by issue #911 / PR-1; v2 path is PR-X `gregale
	// secrets init`) actually hit on the public listener. No auth,
	// no DB call — the daemon process being up is what we're
	// asserting; richer readiness semantics (DB ping, etc.) belong
	// in /readyz later. Mirrors pkg/gateway/control.go::ControlMux.
	mux.HandleFunc("GET /healthz", s.healthz)
	// Dependency-aware readiness probe (testing PostgreSQL pool ping).
	// Note: this is the CUSTOMER-side /readyz (cookie-auth path) and
	// returns a rich JSON body. The OPERATOR-side /readyz on the
	// metrics mux (cmd/apid/main.go:~1518, issue #571 PR-A2) uses
	// pkg/wire.ControlMuxLite + NewPGPingSignal and returns a short
	// ASCII body for the LB scrape. The two share the same source
	// of truth (the same pgxpool) but different auth shapes.
	mux.HandleFunc("GET /readyz", s.readyz)

	// Spec hosting (anonymous; see pkg/apid/openapi_handler.go).
	// /v1/openapi.yaml and /v1/openapi.json let SDK codegen and
	// `curl` reach the same spec the repo CI gate (make spec-check)
	// keeps the code aligned to.
	mux.HandleFunc("GET /v1/openapi.yaml", apid.ServeOpenAPISpec)
	mux.HandleFunc("GET /v1/openapi.json", apid.ServeOpenAPISpecJSON)

	// observeWrap (the outermost layer) feeds apid_ops_total +
	// apid_op_duration_seconds. It's last so it sees the final
	// status code from every chain (auth → idempotent → handler).
	// Nil s.ops (no metrics wired) = no-op passthrough. Includes
	// the spec routes above so SDK codegen hits show up on the
	// §12 dashboard's per-route latency panel.
	//
	// Issue #249 / spec §11: security response headers sit OUTERMOST
	// (above observeWrap) so every status code carries them — even
	// the ones observeWrap synthesizes on panic. httpsec.Static sets
	// the five static headers; httpsec.Nonce mints a per-request CSP
	// nonce and stamps it on the context that dashboard.Render reads
	// to mark up <script>/<style> tags. apid serves only dashboard
	// + JSON so the gate is unconditionally true.
	return httpsec.Static(httpsec.Nonce(
		func(*http.Request) bool { return true },
		s.observeWrap(mux),
	))
}

// observeWrap returns the mux wrapped in an observe middleware that
// records apid_ops_total{op,code} and apid_op_duration_seconds{op}
// per request. nil-safe — returns the inner handler untouched when
// s.ops is nil (unit tests that don't care about metrics).
//
// op label = the route template (e.g. "GET /v1/apps/{slug}"), the
// same shape Go's http.ServeMux uses for the route pattern, so
// cardinality stays bounded by the route table (not by parameter
// values — that would explode the label set).
//
// code label is "ok" on 2xx / 3xx and "err" on anything else;
// 4xx quota/auth codes are the dominant traffic and the §12
// dashboard's "rejected traffic" panel reads this column.
//
// Unmatched routes (r.Pattern == "" — the 404 path a URL scanner
// hits, e.g. "/wp-login.php", "/.env") are recorded under a single
// fixed op="unmatched" label. Recording under r.URL.Path would let
// a scanner explode the label set unbounded (review finding #2 on
// PR #132); the unmatched bucket still surfaces scanner traffic as
// one series per code so the §12 dashboard can alert on it.
func (s *server) observeWrap(h http.Handler) http.Handler {
	if s.ops == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &observeWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		op := r.Pattern
		if op == "" {
			op = "unmatched"
		}
		s.ops.Observe(op, time.Since(start), observeErrFromStatus(rec.status))
		// Issue #303 / ADR-039: feed the per-customer request-total
		// counter on every request (success and failure). The counter
		// is the per-request total — paired with requestFailures
		// (status >= 400 only) for the error-rate view. The §12
		// traffic-anomaly recording rules (faas_apid_request_rate_5m,
		// _error_rate_5m, _3d_baseline, _ratio) read from this
		// counter plus the code label. The code label is derived
		// observeErrFromStatus-style: 2xx/3xx → "ok", 4xx/5xx → "err".
		//
		// account_id resolution reuses the same principalFrom(r)
		// chain as RequestFailureFor below — empty resolves to
		// "anonymous" via the bounded admission set; ids past the cap
		// collapse to "__other__". The two counters share the same
		// accountLabelSet so a customer is represented by their real
		// id in both, or by "__other__" in both.
		{
			acct := anonymousAccountLabel
			if p, ok := principalFrom(r); ok && p.Acct.ID != "" {
				acct = p.Acct.ID
			}
			s.ops.RequestTotalFor(r, rec.status, acct).Inc()
		}
		// Issue #278: also feed the per-customer request-failure
		// counter when the response status indicates a client or
		// server error. 4xx/5xx are the signal; 1xx-3xx never count.
		// The route label is r.Pattern (or "unmatched") so the
		// cardinality stays bounded by the route table, not by the
		// URL path — same precedent as apid_ops_total.
		//
		// account_id resolution: principalFrom(r) succeeds when the
		// inner chain (s.auth) mutated *r to carry the principal in
		// r.Context() — see s.auth at server.go:962/998. For the
		// unauthenticated 401 branch, principalFrom returns ok=false
		// and the account resolves to "anonymous" via the bounded
		// admission set inside OpsMetrics.RequestFailureFor. Id past
		// the cap → "__other__".
		//
		// RequestFailureFor is preferred over the string-form
		// RequestFailure so the route label's extraction from r.Pattern
		// lives next to the cardinality contract — a future caller
		// cannot accidentally pass a raw URL path and explode the
		// label set (review finding #1 on PR #332).
		if rec.status >= http.StatusBadRequest {
			acct := anonymousAccountLabel
			if p, ok := principalFrom(r); ok && p.Acct.ID != "" {
				acct = p.Acct.ID
			}
			s.ops.RequestFailureFor(r, acct).Inc()
		}
		// Issue #300: feed the per-account rolling count that the
		// 5s topNSampler reads to drive apid_top_tenant_rps. The
		// sampler is the ONLY writer to the gauge; this per-request
		// bump is the cheap path (mutex + map incr). Account_id
		// resolution mirrors the requestTotal branch above so the
		// two views agree on whose request they're attributing.
		{
			acct := anonymousAccountLabel
			if p, ok := principalFrom(r); ok && p.Acct.ID != "" {
				acct = p.Acct.ID
			}
			s.ops.ObserveTopTenantRPS(acct)
		}
	})
}

// observeWriter tees the response so observeWrap can read the final
// status after the chain finishes. WriteHeader is called by every
// handler in the project (no implicit-200 paths), so capturing it is
// sufficient.
type observeWriter struct {
	http.ResponseWriter
	status int
}

func (o *observeWriter) WriteHeader(s int) {
	o.status = s
	o.ResponseWriter.WriteHeader(s)
}

// observeErrFromStatus maps a route's terminal status code to an
// error sentinel. observeWrap uses the non-nil sentinel to drive
// apid_ops_total{code="err"}; the sentinel's text isn't on the wire.
func observeErrFromStatus(status int) error {
	if status >= 200 && status < 400 {
		return nil
	}
	return errors.New("apid: http " + http.StatusText(status))
}

// healthz is the loopback-friendly liveness probe. Returns 200 with
// a tiny JSON body so CD pipelines can assert the response shape.
// Intentionally cheap — no DB, no auth — so a healthy /healthz does
// not imply the daemon is ready to serve traffic. See /readyz (TODO)
// for that.
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// dashboardChain wraps a dashboard handler in the §11 middleware
// (RequestID + Recovery; slice 3 adds sessionAuth). The full chain is:
//
//	RequestID → Recovery → handler
//
// Order matters: RequestID must come first so even Recovery's 500
// response carries the id, and Recovery must wrap the inner handler
// so a template panic returns 500 instead of taking the daemon down.
//
// Use dashboardAuthChain (below) for /login and /auth/verify — those
// routes need AuthLimit wrapped between Recovery and the handler.
func (s *server) dashboardChain(h http.Handler) http.Handler {
	// http.HandlerFunc is also http.Handler so middleware.RequestID
	// accepts it directly. Build inside-out.
	h = middleware.RequestID(h)
	h = middleware.Recovery(s.log)(h)
	return h
}

// dashboardAuthChain wraps a dashboard handler in the §11 middleware
// plus an AuthLimit limiter. The full chain is:
//
//	RequestID → Recovery → AuthLimit → handler
//
// AuthLimit comes AFTER Recovery so a panic inside the handler still
// returns 500 (limiter sees 500, not 429). AuthLimit comes BEFORE
// the handler so it can 429 without ever invoking the inner logic.
// Spec §11: "rate limit auth failures (10/min/IP)".
func (s *server) dashboardAuthChain(cfg middleware.AuthLimitConfig, h http.Handler) http.Handler {
	h = s.dashboardChain(h)
	if s.dashboardAuthLimiter == nil {
		s.dashboardAuthLimiter = middleware.NewLimiter(cfg)
	}
	h = middleware.AuthLimitWithLimiter(cfg, s.dashboardAuthLimiter)(h)
	return h
}

// cliAuthChain wraps the anonymous /v1/cli-auth/* endpoints in the
// §11 middleware plus an AuthLimit limiter on its own bucket
// (s.cliAuthLimiter, separate from s.apiAuthLimiter and
// s.dashboardAuthLimiter). The full chain is:
//
//	RequestID → Recovery → AuthLimit → handler
//
// Why a separate bucket: a brute-force on codes shouldn't lock out
// the customer's bearer-token flow, and the dashboard's /login
// bucket shouldn't burn from anonymous CLI traffic either. Count
// 429 + 400 so an attacker cycling on shape-rejected bodies still
// hits the limit. (A successful 200 mint happens once per real CLI,
// so it would never naturally exhaust the budget; we don't need to
// count it.)
func (s *server) cliAuthChain(h http.Handler) http.Handler {
	h = middleware.RequestID(h)
	h = middleware.Recovery(s.log)(h)
	h = middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{
		CountStatuses: []int{http.StatusTooManyRequests, http.StatusBadRequest},
		Log:           s.log,
	}, s.cliAuthLimiter)(h)
	return h
}

// cliAuthSubmitChain wraps POST /cli-auth (dashboard-side claim
// form). Same shape as cliAuthChain but a different bucket
// (s.cliAuthSubmitLimiter) so the dashboard's /login bucket is not
// burned by a customer retrying `faas login`. Counts every attempt
// — the form is the brute-force surface (a bot can submit known
// codes + emails without the dashboard ever rendering).
func (s *server) cliAuthSubmitChain(h http.Handler) http.Handler {
	h = middleware.RequestID(h)
	h = middleware.Recovery(s.log)(h)
	h = middleware.AuthLimitWithLimiter(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
		Log:           s.log,
	}, s.cliAuthSubmitLimiter)(h)
	return h
}

// accountHandler is a handler that has already resolved the caller's account.
//
// The principal (Account + APIKey + scopes) is stashed in the request
// context by s.auth; middlewares that need to inspect scopes (the
// requireScope wrapper) read it back via principalFrom. Handlers
// themselves only need the Account and continue to receive it as the
// third argument, so this change is invisible to the 38 existing
// accountHandler bodies. See ADR-034.
//
// Pointer mutation (issue #278): s.auth mutates *r in place via
// `*r = *r.WithContext(...)` so the request seen by the outer
// observeWrap carries the principal. Without this, principalFrom
// would always return ok=false from inside observeWrap and the
// per-customer request-failure counter would be useless (every
// authenticated call would land in the "anonymous" bucket).
type accountHandler func(w http.ResponseWriter, r *http.Request, acct state.Account)

// principal is the authenticated caller. Key is nil when the caller
// authenticated via the dashboard session cookie (in which case the
// caller is implicitly treated as having the "admin" scope by
// requireScope). See ADR-034.
type principal struct {
	Acct state.Account
	Key  *state.APIKey
}

// principalFrom returns the principal stashed in r.Context() by s.auth.
// Returns ok=false if s.auth did not run (e.g. tests that wire a
// handler directly to httptest). Bridges to pkg/auth so that both
// cmd/apid's observeWrap and pkg/auth.RequireScope read the same
// stamped value — without the bridge, the two surfaces would each
// look at their own (separate) ctx key and requireScope would
// 500/CodeCapacity on every apid route (PR-1 pkg/auth extraction,
// ADR-044).
func principalFrom(r *http.Request) (principal, bool) {
	acct, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return principal{}, false
	}
	return principal{Acct: acct, Key: key}, true
}

// principalHasScope reports whether the principal carries at least one
// of the allowed scopes. Session-cookie principals (Key == nil) are
// implicitly admin. An empty allowed set is a no-op (the caller didn't
// ask for any scope check, e.g. internal routes).
//
// INVARIANT: this helper is called only from requireScope, which
// guarantees the principal was populated by s.auth. The Key==nil branch
// relies on that — it short-circuits before consulting allowed, so a
// direct call here without going through the auth middleware would let
// unauthenticated requests reach the handler. Callers other than
// requireScope must guard with principalFrom(...) != (principal{}, true)
// first.
func principalHasScope(p principal, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	if p.Key == nil {
		// Session-cookie auth = human at the dashboard = full access.
		return true
	}
	for _, want := range allowed {
		for _, have := range p.Key.Scopes {
			if have == want {
				return true
			}
		}
	}
	return false
}

// auth authenticates by API-key hash and rejects inactive accounts (spec §11).
//
// Carve-out for G6 (spec §17 G6, ADR-021): while an account is in
// deleted_pending, the customer still needs to reach
//   - GET    /v1/account          (Whoami — read-only status probe)
//   - GET    /v1/account/export   (final export during grace)
//   - DELETE /v1/account          (idempotent re-DEL)
//   - POST   /v1/account/restore  (cancel the deletion)
//
// All other routes still 402 with CodeBillingPastDue during grace
// because the work surface (deploy, build, park live instances) is
// already torn down.
//
// On success, the resolved principal{Acct, Key} is stashed in r.Context()
// via withPrincipal. The accountHandler that wraps s.auth reads the
// Account out of the principal; the requireScope wrapper reads the
// scopes. Session-cookie auth produces a principal with Key == nil
// (treated as implicit admin by the scope check). See ADR-034.
// auth is the cmd/apid-side facade. The body lives in
// pkg/auth (cmd/apid/auth_facade.go::auth is the bridge).
// Behaviour matches the pre-extraction shape because pkg/auth lifts
// the bearer + cookie branch + debounce + detached-touch machinery
// verbatim. ADR-046.

// requireScope returns a middleware that enforces the API-key scope
// vocabulary on the route. The principal must have at least one of the
// allowed scopes; session-cookie callers are implicitly admin. The
// middleware runs after s.auth (which stashes the principal) so it
// composes inside the auth wrappers as:
//
//	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.handler)))
//
// Pick the named shape that matches the route
// (api.ScopesAdminOnly / api.ScopesReadSurface /
// api.ScopesDeployWriteSurface / api.ScopesSecretsWriteSurface /
// api.ScopesUsageReadSurface) — admin is in every set, so a session
// cookie or admin key always satisfies. See ADR-034 rev2.

// requireScope is the cmd/apid-side facade. The body lives in
// pkg/auth (cmd/apid/auth_facade.go::requireScope is the bridge).
// Behaviour matches the pre-extraction shape because pkg/auth lifts
// the predicate principalHasScope and the audit path verbatim. ADR-044.

// authLimited wraps an accountHandler in s.auth + AuthLimit (spec §11:
// 10 failed auth attempts per IP per minute). The /v1/* API-key surface
// uses this everywhere; only /login, /auth/verify, and /dashboard/* use
// the cookie-based dashboardAuthChain instead.
//
// Counts ONLY 401s — the inner handler is responsible for any 429
// emission (e.g. quota). CountStatuses=[401] is the explicit default
// (the middleware's nil-means-401 fallback also covers this; we set it
// explicitly for clarity at the wire boundary).
//
// The bucket is s.apiAuthLimiter — shared across every /v1/* route so
// spec §11 "10/min/IP" is enforced across the whole surface, not per
// route. Tests inject a fresh limiter via apiAuthLimiter so each test
// gets an isolated bucket; the nil-fallback keeps the daemon booting
// in dev environments that bypass newServerWithDeps.
// authLimited is the cmd/apid-side facade. The body lives in
// pkg/auth (cmd/apid/auth_facade.go::authLimited is the bridge).
// Behaviour matches the pre-extraction shape — pkg/auth.RequireLimited
// wraps RequireSession in pkg/middleware.AuthLimitWithLimiter with the
// shared s.apiAuthLimiter bucket (spec §11 "10/min/IP"). ADR-046.

// idempotent replays a stored response for a repeated Idempotency-Key, or runs
// the handler and stores its response (spec §4.2: kept 24 h). Without the header
// it is a passthrough.
func (s *server) idempotent(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r, acct)
			return
		}
		if status, body, err := s.store.GetIdempotent(r.Context(), acct.ID, key); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		cap := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next(cap, r, acct)
		_ = s.store.PutIdempotent(r.Context(), acct.ID, key, cap.status, cap.body.Bytes())
	}
}

// requireIdempotency is the stricter companion used by provider-admin
// mutations. A missing key is rejected instead of silently allowing a retry
// to repeat a money or fleet-state operation; the existing idempotent wrapper
// then provides the replay cache for valid keys.
func (s *server) requireIdempotency(next accountHandler) accountHandler {
	idempotent := s.idempotent(next)
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || len(key) > 255 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Idempotency-Key required", "provider-admin mutations require an Idempotency-Key of 1..255 characters"))
			return
		}
		idempotent(w, r, acct)
	}
}

// captureWriter tees the response so idempotent() can persist it.
//
// Every handler that writes through this wrapper either sets
// application/json/problem+json (api.WriteProblem / explicit
// Content-Type at server.go:1132 / :1361) or is the dashboard HTML path
// which renders through html/template (templates/*.html). CodeQL cannot
// see through the ResponseWriter wrapper so it conservatively flags any
// Write as a possible HTML sink; the upstream content type and renderer
// make that unreachable. See the // lgtm[go/reflected-xss] false-positive
// suppression directly above the Write method.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

// lgtm[go/reflected-xss] false-positive: captureWriter is a pass-through; upstream content type + renderer make the XSS sink unreachable. See captureWriter doc-comment.
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
	// lgtm[go/reflected-xss] false-positive: captureWriter is a pass-through; upstream content type + renderer make the XSS sink unreachable. See captureWriter doc-comment.
	return c.ResponseWriter.Write(b)
}

// --- helpers ---------------------------------------------------------------

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// decodeJSONSized mirrors decodeJSON but with a caller-supplied
// body byte cap. Used by handlers that need a tighter cap than the
// 1 MiB default (e.g. handlers_registry_auth.go caps at 1 MiB
// anyway — but the seam lets future handlers pass a smaller cap
// without re-implementing the DisallowUnknownFields dance).
func decodeJSONSized(r *http.Request, v any, maxBytes int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// notFound writes a 404 problem, distinguishing missing rows.
func (s *server) notFound(w http.ResponseWriter, what string) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", what))
}

// loadApp resolves a slug to an account-scoped App, collapsing cross-account
// lookups to 404 per the handler convention. Returns the resolved app or
// writes the error and returns false.
// loadApp is the cmd/apid-side facade. The body lives in
// pkg/auth (cmd/apid/auth_facade.go::loadApp is the bridge).
// Behaviour matches the pre-extraction shape — pkg/auth.LoadApp
// does the IDOR-safe slug→App lookup with the ownership predicate
// (app.AccountID == acct.ID) and the 404 "no such app" response on
// a cross-account or missing row. ADR-046.
