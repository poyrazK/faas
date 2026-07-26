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
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/middleware"
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
	store  state.Store
	log    *slog.Logger
	domain string // apps base domain for URLs
	notif  Notifier
	// stripeWebhookSecret is the endpoint signing secret Stripe uses
	// for the v1 HMAC. Empty disables signature verification (dev mode).
	stripeWebhookSecret string
	// mailer emits the dunning + quota-warning emails. nil falls back
	// to the noop sender so callers never need to nil-check.
	mailer Mailer
	// githubd is apid's handle to the githubd daemon (ADR-012). Never nil:
	// slice 1 default is stubGithubdClient; slice 7 swaps for a live dial.
	githubd GithubdClient
	// events is the in-process broadcaster the SSE handlers read from
	// (slice 5/6). nil falls back to a fresh one so callers can defer
	// initialization in unit tests.
	events *events.Broadcaster
	// sessions seals + verifies dashboard cookies. nil falls back to an
	// ephemeral manager (so the daemon still boots in dev with no
	// /etc/faas/secrets/session.key) — see cmd/apid/main.go.
	sessions *session.Manager
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
	// audit is the IAM-4 (ADR-035) seam that auth-relevant handlers
	// call to record a security event. The seam wraps
	// state.Store.AppendEvent with best-effort failure semantics
	// (log Warn + apid_audit_write_failures_total counter, never
	// roll back the action). nil falls back to a no-op helper so
	// older tests can build a server without an audit handle.
	audit *auditor
	// touchDebounce coalesces per-key last_used_at updates (PR #232
	// review #7). Without this, every successful bearer auth would
	// spawn a goroutine issuing an UPDATE api_keys SET last_used_at =
	// now(). Hot keys (gateway pinned under one key) would amplify
	// into WAL+lock pressure. keyTouchWindow below is the
	// minimum interval — recent touches within the window drop.
	// Reads are lock-free via sync.Map.Load; the Store path is
	// LoadOrStore so the first writer wins. A periodic janitor
	// (pkg/grace) evicts old entries to bound memory.
	touchDebounce sync.Map // map[string]time.Time
}

// keyTouchWindow is the minimum interval between per-key last_used_at
// stamps. PR #232 review pinned this at 30s — observability doesn't need
// sub-second resolution, and a long-enough window keeps WAL amplification
// bounded even under sustained 1k RPS on one key.
const keyTouchWindow = 30 * time.Second

// shouldTouchKey reports whether the key's last_used_at is stale enough
// to warrant an UPDATE. Atomic-ish via sync.Map.LoadOrStore so two
// concurrent first-time callers don't both schedule a touch; the
// second caller observes the freshly-stored timestamp and skips.
func (s *server) shouldTouchKey(keyID string, now time.Time) bool {
	last, loaded := s.touchDebounce.LoadOrStore(keyID, now)
	if !loaded {
		return true // first touch wins
	}
	if now.Sub(last.(time.Time)) < keyTouchWindow {
		return false // recent touch — drop
	}
	// stale entry; CAS to refresh.
	s.touchDebounce.Store(keyID, now)
	return true
}

// WithOpsMetrics attaches the daemon-wide Prometheus registry. The
// handler-level observe call in observeHandler hits ops; the chain
// methods stay untouched. Mirrors pkg/builderd/builderd.go's
// WithOpsMetrics (PR #124, ADR-030) and pkg/githubd/server.go's
// WithOpsMetrics (this PR).
func (s *server) WithOpsMetrics(ops *wire.OpsMetrics) *server {
	s.ops = ops
	// Re-bind the audit counter so the IAM-4 seam can record
	// failures. If ops is nil (unit tests that don't care about
	// metrics), leave the auditor with a nil ops interface so Emit
	// silently skips the .Inc().
	if s.audit != nil {
		s.audit.ops = ops
	}
	return s
}

// WithStatusCache wires the status-page Prometheus query cache.
// Called from main after the config has loaded the Prometheus URL;
// the route handlers are mounted regardless so a misconfigured
// prometheus URL degrades the JSON to "no source" rather than 5xx.
func (s *server) WithStatusCache(promURL, htmlPath string) *server {
	s.statusCache = newStatusCache(promURL, s.log)
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

// WithBillingProvider attaches the per-deployment billing.Provider.
// Called from cmd/apid/main.go after LoadProviderForAPID. When non-nil
// the /v1/webhooks/paddle route is mounted and the changePlan 402 path
// dispatches through it; when nil the apid Stripe path stays inline
// (the legacy stripe.VerifySignature + BillingPortalURL template).
func (s *server) WithBillingProvider(p billing.Provider) *server {
	s.billingProvider = p
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

// Mailer is the slice of pkg/mail.Sender apid depends on. Kept as an
// interface so tests inject a recording stub without importing pkg/mail.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Message is the cross-component email payload — mirrors pkg/mail.Message
// without the import cycle (apid stays free of pkg/mail so daemons that
// link apid don't pull the mail deps).
type Message struct {
	To       []string
	Subject  string
	TextBody string
	HTMLBody string
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
		domain = "DOMAIN"
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
	// (acceptable — gatewayd is the primary edge counter).
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
	return &server{
		store:                  store,
		log:                    log,
		domain:                 domain,
		notif:                  notif,
		stripeWebhookSecret:    stripeSecret,
		mailer:                 mailer,
		githubd:                githubd,
		events:                 bcaster,
		sessions:               sessions,
		loginTTL:               loginTTL,
		dpaPath:                dpaPath,
		apiAuthLimiter:         apiAuthLimiter,
		dashboardAuthLimiter:   dashboardAuthLimiter,
		cliAuthLimiter:         cliAuthLimiter,
		cliAuthSubmitLimiter:   cliAuthSubmitLimiter,
		dashboardExportLimiter: dashboardExportLimiter,
		audit:                  newAuditor(store, log, nil),
	}
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
	// Account. The /v1/account/plan change is destructive across the
	// whole account, so it requires the admin scope; the read-only
	// /v1/account carries the method default (read or admin).
	mux.HandleFunc("GET /v1/account", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.whoami)))
	mux.HandleFunc("PATCH /v1/account/plan", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.changePlan))))
	// G6 account self-service (spec §17 G6, ADR-021). /v1/account/dpa
	// is intentionally mounted without s.auth — the DPA is a public
	// artefact a prospect reads before signing up. The export + delete
	// + restore paths sit behind s.auth but pass the
	// deleted_pending carve-out in isAccountScopedPath so a customer
	// can take a final export or cancel during the 30-day grace.
	// DELETE /v1/account is admin-only — losing the account is
	// irreversible.
	mux.HandleFunc("GET /v1/account/export", s.auth(s.requireScope(api.ScopesReadSurface...)(s.exportAccount)))
	mux.HandleFunc("DELETE /v1/account", s.auth(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.deleteAccount))))
	mux.HandleFunc("POST /v1/account/restore", s.auth(s.requireScope(api.ScopesDeployWriteSurface...)(s.restoreAccount)))
	mux.HandleFunc("GET /v1/account/dpa", s.dpaTemplate)

	// Apps.
	mux.HandleFunc("GET /v1/apps", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listApps)))
	mux.HandleFunc("POST /v1/apps", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createApp))))
	mux.HandleFunc("GET /v1/apps/{slug}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getApp)))
	mux.HandleFunc("PATCH /v1/apps/{slug}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateApp)))
	mux.HandleFunc("DELETE /v1/apps/{slug}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteApp)))

	// Deployments.
	mux.HandleFunc("POST /v1/apps/{slug}/deployments", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createDeployment))))
	mux.HandleFunc("GET /v1/deployments/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getDeployment)))
	mux.HandleFunc("GET /v1/deployments/{id}/logs", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.streamDeploymentLogs)))
	// Builds (ADR-038). The provenance route is the only /v1/builds
	// surface today; deployments.id remains the parent resource.
	// Build:read scope (api.ScopesReadSurface) gates the read.
	mux.HandleFunc("GET /v1/builds/{id}/provenance", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getBuildProvenance)))
	mux.HandleFunc("POST /v1/apps/{slug}/rollback", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.rollbackApp))))
	mux.HandleFunc("POST /v1/apps/{slug}/park", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.parkApp)))
	mux.HandleFunc("POST /v1/apps/{slug}/wake", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.wakeApp)))
	mux.HandleFunc("POST /v1/apps/{slug}/rename", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.renameApp))))

	// Instances (read-only here; schedd is the writer).
	mux.HandleFunc("GET /v1/apps/{slug}/instances", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listInstances)))
	mux.HandleFunc("GET /v1/apps/{slug}/logs", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.streamAppLogs)))

	// Custom domains.
	mux.HandleFunc("GET /v1/domains", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listDomains)))
	mux.HandleFunc("POST /v1/domains", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createDomain))))
	mux.HandleFunc("DELETE /v1/domains/{domain}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteDomain)))

	// Crons.
	mux.HandleFunc("GET /v1/crons", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listCrons)))
	mux.HandleFunc("POST /v1/crons", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.createCron))))
	mux.HandleFunc("PATCH /v1/crons/{id}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateCron)))
	mux.HandleFunc("DELETE /v1/crons/{id}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteCron)))

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
	mux.HandleFunc("POST /v1/apps/{slug}/invoke", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.invokeApp)))
	mux.HandleFunc("POST /v1/apps/{slug}/invoke/async", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.invokeAppAsync))))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/send", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.queueSend))))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/receive", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.queueReceive)))
	mux.HandleFunc("POST /v1/apps/{slug}/queues/{id}/ack", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.queueAck))))
	mux.HandleFunc("POST /v1/apps/{slug}/delayed-tasks", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.delayedTaskCreate))))
	mux.HandleFunc("GET /v1/invocations", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listInvocations)))
	mux.HandleFunc("GET /v1/invocations/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getInvocation)))
	mux.HandleFunc("GET /v1/delayed-tasks/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.delayedTaskGet)))
	mux.HandleFunc("DELETE /v1/delayed-tasks/{id}", s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.delayedTaskCancel)))

	// API keys. Minting and revoking keys are admin-only — a leaked
	// write-scoped key must not be able to grant itself more scopes.
	// Listing is read.
	mux.HandleFunc("GET /v1/keys", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listKeys)))
	mux.HandleFunc("POST /v1/keys", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.createKey)))
	mux.HandleFunc("DELETE /v1/keys/{id}", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.deleteKey)))

	// IAM-4 (ADR-035) — auth audit log surface. Read-only; the
	// events table is append-only (spec §5). Scope gating: session
	// cookie (implicitly admin) or any API key carrying {admin,
	// apps:read} (api.ScopesReadSurface). Compute-node admin-only
	// routes are gated separately below.
	mux.HandleFunc("GET /v1/audit-events", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listAuditEvents)))
	mux.HandleFunc("GET /v1/audit-events/{id}", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.getAuditEvent)))

	// Customer secrets (spec §11/G2). Plaintext VALUE flows through PUT
	// over TLS; sealed server-side by handlers_secrets.go.
	mux.HandleFunc("GET /v1/apps/{slug}/secrets", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listSecrets)))
	mux.HandleFunc("PUT /v1/apps/{slug}/secrets/{key}", s.authLimited(s.requireScope(api.ScopesSecretsWriteSurface...)(s.setSecret)))
	mux.HandleFunc("DELETE /v1/apps/{slug}/secrets/{key}", s.authLimited(s.requireScope(api.ScopesSecretsWriteSurface...)(s.deleteSecret)))

	// Usage.
	// Usage endpoints are narrower than the read surface — a deploy-write
	// CI key doesn't need them. usage:read is the right knob here.
	mux.HandleFunc("GET /v1/usage", s.authLimited(s.requireScope(api.ScopesUsageReadSurface...)(s.getUsage)))
	mux.HandleFunc("GET /v1/usage/summary", s.authLimited(s.requireScope(api.ScopesUsageReadSurface...)(s.usageSummary)))

	// Account-scoped deployments list (M7.5 dashboard).
	mux.HandleFunc("GET /v1/deployments", s.authLimited(s.requireScope(api.ScopesReadSurface...)(s.listDeployments)))

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

	// Operator admin surface (issue #98 / ADR-028). Auth lives in
	// s.adminAllows (email allowlist via FAAS_ADMIN_EMAILS); handlers
	// 403 every request when the allowlist is empty. The scope
	// check is layered on top so a non-admin key never reaches the
	// adminAllows handler. authLimited wraps the per-IP bucket so
	// the routes share the spec §11 10/min/IP budget with the rest
	// of /v1/* — a brute-force on admin routes costs the attacker
	// the same budget they'd burn trying customer keys.
	mux.HandleFunc("GET /v1/compute-nodes", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.listComputeNodes)))
	mux.HandleFunc("POST /v1/compute-nodes", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.idempotent(s.createOrUpdateComputeNode))))
	mux.HandleFunc("DELETE /v1/compute-nodes/{name}", s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.deleteComputeNode)))

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

	// Dashboard surface (M7.5, ADR-011). Lives behind gatewayd's
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
	// OAuth-only customers. Behind sessionAuth so the call is anchored
	// to a known account. NOT behind the auth-bucket — the call only
	// succeeds when the customer already holds a session.
	mux.Handle("POST /dashboard/account/set-password", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.postSetPassword))))
	mux.Handle("GET /auth/verify", s.dashboardAuthChain(middleware.AuthLimitConfig{
		// /auth/verify 401s on unknown tokens AND 410s on consumed tokens;
		// count both so an attacker can't cycle through one-time tokens
		// faster than the spec §11 10/min/IP budget.
		CountStatuses: []int{http.StatusUnauthorized, http.StatusGone},
	}, http.HandlerFunc(auth.verify)))
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

	// G6 dashboard delete/restore (spec §17 G6, ADR-021). Both POSTs
	// require the confirm_token form field (validated inside the
	// handler) and sit behind sessionAuth so the call is anchored to
	// the logged-in account. The handlers reuse scheduleDeletion /
	// cancelDeletion from handlers_account.go so audit, email, and
	// notification side-effects match the REST API path bit-for-bit.
	mux.Handle("POST /dashboard/account/delete", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardDelete))))
	mux.Handle("POST /dashboard/account/restore", s.dashboardChain(s.sessionAuth(http.HandlerFunc(s.dashboardRestore))))
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

	// Status page (spec §12 public status page). Unauthenticated by
	// design — prospects read it before sign-up, customers during
	// incidents. Carries no tenant data; only fleet-wide SLIs. Mounted
	// on the public mux so the operator's HTTPS path serves it.
	mux.HandleFunc("GET /status", s.statusHandler)
	mux.HandleFunc("GET /status/slo.json", s.statusJSONHandler)

	// CLI auth device-code flow (spec §2.2). Anonymous on purpose —
	// the CLI hasn't logged in yet. Anti-enumeration limiter is its
	// own bucket (s.cliAuthLimiter) so brute-force on /v1/cli-auth/*
	// doesn't burn the API-key auth budget at the top of this file.
	cli := &cliAuthHandlers{srv: s, log: s.log, domain: s.domain}
	mux.Handle("POST /v1/cli-auth/code", s.cliAuthChain(http.HandlerFunc(cli.mintCliAuthCode)))
	mux.Handle("POST /v1/cli-auth/exchange", s.cliAuthChain(http.HandlerFunc(cli.exchangeCliAuthCode)))
	// Dashboard-side GET shares the dashboard auth bucket (it renders
	// the same form for every state, so attempts are not the
	// brute-force surface). POST uses its own bucket
	// (cliAuthSubmitChain) so a customer retrying `faas login` from a
	// shared NAT doesn't burn the magic-link /login budget.
	mux.Handle("GET "+cliAuthPath, s.dashboardAuthChain(middleware.AuthLimitConfig{
		CountStatuses: []int{middleware.CountEveryAttempt},
	}, http.HandlerFunc(cli.renderCliAuthPage)))
	mux.Handle("POST "+cliAuthPath, s.cliAuthSubmitChain(http.HandlerFunc(cli.postCliAuthPage)))

	// Loopback infra probe (issue #85). gatewayd forwards /healthz to
	// apid through the apidProxy chain, so this is what the
	// deploy/digitalocean CD smoke test and deploy/digitalocean/
	// bootstrap.sh health check actually hit on the public listener.
	// No auth, no DB call — the daemon process being up is what we're
	// asserting; richer readiness semantics (DB ping, etc.) belong
	// in /readyz later. Mirrors pkg/gateway/control.go::ControlMux.
	mux.HandleFunc("GET /healthz", s.healthz)

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
	return s.observeWrap(mux)
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
type accountHandler func(w http.ResponseWriter, r *http.Request, acct state.Account)

// principal is the authenticated caller. Key is nil when the caller
// authenticated via the dashboard session cookie (in which case the
// caller is implicitly treated as having the "admin" scope by
// requireScope). See ADR-034.
type principal struct {
	Acct state.Account
	Key  *state.APIKey
}

type ctxKey int

const principalCtxKey ctxKey = iota

// principalFrom returns the principal stashed in r.Context() by s.auth.
// Returns ok=false if s.auth did not run (e.g. tests that wire a
// handler directly to httptest).
func principalFrom(r *http.Request) (principal, bool) {
	v := r.Context().Value(principalCtxKey)
	if v == nil {
		return principal{}, false
	}
	p, ok := v.(principal)
	return p, ok
}

// withPrincipal returns a context carrying the principal.
func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalCtxKey, p)
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
func (s *server) auth(next accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if api.ValidAPIKeyFormat(tok) {
			acct, key, err := s.store.AuthenticateKey(r.Context(), api.HashAPIKey(tok))
			if err == nil {
				if !acct.Active() {
					if acct.Status != state.AccountDeletedPending || !isAccountScopedPath(r.URL.Path) {
						api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
							"Account suspended", "resolve billing to continue: https://DOMAIN/billing"))
						return
					}
				}
				r = r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: &key}))
				// TouchKeyLastUsed is observability, not auth — a slow or
				// failing PG must not block the user's request. Detached
				// context so a request that cancels client-side (tab close,
				// SSE disconnect) still stamps the key; bounded timeout
				// so a hung pool is reclaimed in 2 s. No pkg/grace.Recover:
				// a panic in a 5-line goroutine implies a code bug worth
				// surfacing in the daemon logs. Pass r.Context() to satisfy
				// contextcheck even though the goroutine's detached ctx
				// ignores it. Per-key debounce (keyTouchWindow,
				// server.touchDebounce) keeps hot keys from amplifying
				// into 1k+ UPDATEs/sec under sustained load (PR #232
				// review #7).
				if s.shouldTouchKey(key.ID, time.Now()) {
					//nolint:contextcheck // see touchKeyLastUsed — detached is load-bearing
					go func(parent context.Context, id string) {
						s.touchKeyLastUsed(parent, id)
					}(r.Context(), key.ID)
				}
				next(w, r, acct)
				return
			}
		}

		// Session cookie authentication (faas_sid) for Web Dashboard
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if env, err := s.sessions.Verify(c.Value); err == nil {
				if acct, err := s.store.AccountByID(r.Context(), env.AccountID); err == nil {
					if !acct.Active() {
						if acct.Status != state.AccountDeletedPending || !isAccountScopedPath(r.URL.Path) {
							api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
								"Account suspended", "resolve billing to continue: https://DOMAIN/billing"))
							return
						}
					}
					// Key stays nil; session cookie = implicit admin.
					r = r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: nil}))
					next(w, r, acct)
					return
				}
			}
		}

		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "provide a valid API key as a Bearer token or sign in via session cookie"))
	}
}

// requireScope returns a middleware that enforces the API-key scope
// vocabulary on the route. The principal must have at least one of the
// allowed scopes; session-cookie callers are implicitly admin. The
// middleware runs after s.auth (which stashes the principal) so it
// composes inside the auth wrappers as:
//
//	s.authLimited(s.requireScope(api.ScopesDeployWriteSurface...)(s.handler))
//
// Pick the named shape that matches the route
// (api.ScopesAdminOnly / api.ScopesReadSurface /
// api.ScopesDeployWriteSurface / api.ScopesSecretsWriteSurface /
// api.ScopesUsageReadSurface) — admin is in every set, so a session
// cookie or admin key always satisfies. See ADR-034 rev2.

// touchKeyLastUsed bumps last_used_at on the given api_key after a
// successful bearer auth. Observability only — never auth. Detached
// context + 2 s timeout means a slow or hung PG cannot block the user's
// request, and a canceled client (tab close, SSE disconnect) still
// leaves a stamp. An error is logged at WARN; the next successful
// request will overwrite a stale value anyway. Accepts a parent ctx
// (unused — the goroutine detaches) so it matches contextcheck's
// "function takes ctx" guidance; the touch never respects its parent
// because it MUST outlive the request.
//
//nolint:contextcheck // detached-context is the load-bearing behavior for fire-and-forget observability
func (s *server) touchKeyLastUsed(_ context.Context, keyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.store.TouchKeyLastUsed(ctx, keyID); err != nil {
		s.log.Warn("api_key last_used_at touch failed", "key_id", keyID, "error", err.Error())
	}
}

// requireScope(allowed ...string) accepts the route's allowed scope set.
// The HTTP method is implicit in the closure below — handlers chain
// requireScope before calling the real handler, so method is no longer
// a separate parameter. Routes pick the named shape that matches the
// HTTP verb (api.ScopesDeployWriteSurface for POST/PATCH/DELETE,
// api.ScopesReadSurface for GET, etc.) — admin is in every set, so a
// session cookie or admin key always satisfies.
//
// Pick the named shape that matches the route
// (api.ScopesAdminOnly / api.ScopesReadSurface /
// api.ScopesDeployWriteSurface / api.ScopesSecretsWriteSurface /
// api.ScopesUsageReadSurface) — admin is in every set, so a session
// cookie or admin key always satisfies. See ADR-034 rev2.
func (s *server) requireScope(allowed ...string) func(accountHandler) accountHandler {
	return func(next accountHandler) accountHandler {
		return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			p, ok := principalFrom(r)
			if !ok {
				// requireScope must run AFTER s.auth. If the principal is
				// missing, the route wiring is broken — fail closed.
				api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity,
					"auth-context missing", "requireScope must be wrapped inside s.auth / s.authLimited"))
				return
			}
			if !principalHasScope(p, allowed) {
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
					"Insufficient scope", "this endpoint requires one of: "+strings.Join(allowed, ",")))
				return
			}
			next(w, r, acct)
		}
	}
}

// isAccountScopedPath returns true for the paths that must remain
// reachable while an account is in the deletion grace window. Keep
// this list short and explicit — every entry is a deliberate
// exception to the spec §11 "inactive account = 402" rule.
func isAccountScopedPath(p string) bool {
	switch p {
	case "/v1/account", "/v1/account/export", "/v1/account/restore":
		return true
	}
	return false
}

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
func (s *server) authLimited(next accountHandler) http.HandlerFunc {
	h := s.auth(next)
	cfg := middleware.AuthLimitConfig{
		CountStatuses: []int{http.StatusUnauthorized},
		Log:           s.log,
	}
	if s.apiAuthLimiter == nil {
		s.apiAuthLimiter = middleware.NewLimiter(cfg)
	}
	return middleware.AuthLimitWithLimiter(cfg, s.apiAuthLimiter)(h).ServeHTTP
}

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

// captureWriter tees the response so idempotent() can persist it.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
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

// notFound writes a 404 problem, distinguishing missing rows.
func (s *server) notFound(w http.ResponseWriter, what string) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", what))
}

// ctx is a tiny helper to keep handler signatures clean.
func ctx(r *http.Request) context.Context { return r.Context() }

// loadApp resolves a slug to an account-scoped App, collapsing cross-account
// lookups to 404 per the handler convention. Returns the resolved app or
// writes the error and returns false.
func (s *server) loadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	app, err := s.store.AppBySlug(ctx(r), slug)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return state.App{}, false
	}
	return app, true
}
