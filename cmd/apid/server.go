package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
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
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
	Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
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
	return &server{
		store:               store,
		log:                 log,
		domain:              domain,
		notif:               notif,
		stripeWebhookSecret: stripeSecret,
		mailer:              mailer,
		githubd:             githubd,
		events:              bcaster,
		sessions:            sessions,
		loginTTL:            loginTTL,
		dpaPath:             dpaPath,
	}
}

// noopMailer drops every email. Default when the daemon hasn't wired a
// real transport (gap G4 — the M7 PR uses this everywhere).
type noopMailer struct{}

func (noopMailer) Send(_ context.Context, _ Message) error { return nil }

// newLogMailer returns a Mailer that writes every message to slog. Used
// until gap G4 (Postmark/Resend) lands; cmd/apid wires this on startup.
func newLogMailer(log *slog.Logger) Mailer {
	return &logMailer{log: log}
}

type logMailer struct{ log *slog.Logger }

func (l *logMailer) Send(_ context.Context, msg Message) error {
	l.log.Info("mail.send",
		"to", msg.To, "subject", msg.Subject,
		"has_html", msg.HTMLBody != "", "text_bytes", len(msg.TextBody))
	return nil
}

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

// handler builds the full Appendix A route table (Go 1.22 method+wildcard).
// New routes append here; do not introduce per-feature sub-muxes.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	// Account.
	mux.HandleFunc("GET /v1/account", s.auth(s.whoami))
	mux.HandleFunc("PATCH /v1/account/plan", s.auth(s.idempotent(s.changePlan)))

	// G6 account self-service (spec §17 G6, ADR-021). /v1/account/dpa
	// is intentionally mounted without s.auth — the DPA is a public
	// artefact a prospect reads before signing up. The export + delete
	// + restore paths sit behind s.auth but pass the
	// deleted_pending carve-out in isAccountScopedPath so a customer
	// can take a final export or cancel during the 30-day grace.
	mux.HandleFunc("GET /v1/account/export", s.auth(s.exportAccount))
	mux.HandleFunc("DELETE /v1/account", s.auth(s.idempotent(s.deleteAccount)))
	mux.HandleFunc("POST /v1/account/restore", s.auth(s.restoreAccount))
	mux.HandleFunc("GET /v1/account/dpa", s.dpaTemplate)

	// Apps.
	mux.HandleFunc("GET /v1/apps", s.auth(s.listApps))
	mux.HandleFunc("POST /v1/apps", s.auth(s.idempotent(s.createApp)))
	mux.HandleFunc("GET /v1/apps/{slug}", s.auth(s.getApp))
	mux.HandleFunc("PATCH /v1/apps/{slug}", s.auth(s.updateApp))
	mux.HandleFunc("DELETE /v1/apps/{slug}", s.auth(s.deleteApp))

	// Deployments.
	mux.HandleFunc("POST /v1/apps/{slug}/deployments", s.auth(s.idempotent(s.createDeployment)))
	mux.HandleFunc("GET /v1/deployments/{id}", s.auth(s.getDeployment))
	mux.HandleFunc("GET /v1/deployments/{id}/logs", s.auth(s.streamDeploymentLogs))
	mux.HandleFunc("POST /v1/apps/{slug}/rollback", s.auth(s.idempotent(s.rollbackApp)))
	mux.HandleFunc("POST /v1/apps/{slug}/park", s.auth(s.parkApp))
	mux.HandleFunc("POST /v1/apps/{slug}/wake", s.auth(s.wakeApp))

	// Instances (read-only here; schedd is the writer).
	mux.HandleFunc("GET /v1/apps/{slug}/instances", s.auth(s.listInstances))
	mux.HandleFunc("GET /v1/apps/{slug}/logs", s.auth(s.streamAppLogs))

	// Custom domains.
	mux.HandleFunc("GET /v1/domains", s.auth(s.listDomains))
	mux.HandleFunc("POST /v1/domains", s.auth(s.idempotent(s.createDomain)))
	mux.HandleFunc("DELETE /v1/domains/{domain}", s.auth(s.deleteDomain))

	// Crons.
	mux.HandleFunc("GET /v1/crons", s.auth(s.listCrons))
	mux.HandleFunc("POST /v1/crons", s.auth(s.idempotent(s.createCron)))
	mux.HandleFunc("PATCH /v1/crons/{id}", s.auth(s.updateCron))
	mux.HandleFunc("DELETE /v1/crons/{id}", s.auth(s.deleteCron))

	// API keys.
	mux.HandleFunc("GET /v1/keys", s.auth(s.listKeys))
	mux.HandleFunc("POST /v1/keys", s.auth(s.createKey))
	mux.HandleFunc("DELETE /v1/keys/{id}", s.auth(s.deleteKey))

	// Customer secrets (spec §11/G2). Plaintext VALUE flows through PUT
	// over TLS; sealed server-side by handlers_secrets.go.
	mux.HandleFunc("GET /v1/apps/{slug}/secrets", s.auth(s.listSecrets))
	mux.HandleFunc("PUT /v1/apps/{slug}/secrets/{key}", s.auth(s.setSecret))
	mux.HandleFunc("DELETE /v1/apps/{slug}/secrets/{key}", s.auth(s.deleteSecret))

	// Usage.
	mux.HandleFunc("GET /v1/usage", s.auth(s.getUsage))
	mux.HandleFunc("GET /v1/usage/summary", s.auth(s.usageSummary))

	// Account-scoped deployments list (M7.5 dashboard).
	mux.HandleFunc("GET /v1/deployments", s.auth(s.listDeployments))

	// Stripe webhook (no auth — Stripe signs requests; for M5 we accept
	// unsigned and trust the network boundary; ADR-007 hardening later).
	mux.HandleFunc("POST /v1/webhooks/stripe", s.stripeWebhook)

	// M7.5 SSE live-update (ADR-011). Handles session-cookie OR
	// API-key auth itself — the cookie path is for the dashboard,
	// the Bearer path for the CLI. NOT mounted behind s.auth so the
	// cookie flow works without an API-key round trip.
	mux.Handle("GET /v1/events", s.dashboardChain(s.eventsHandler(s.log)))

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
	mux.Handle("GET /login", s.dashboardChain(http.HandlerFunc(auth.renderLoginForm)))
	mux.Handle("POST /login", s.dashboardChain(http.HandlerFunc(auth.postLogin)))
	mux.Handle("GET /auth/verify", s.dashboardChain(http.HandlerFunc(auth.verify)))
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

	return mux
}

// dashboardChain wraps a dashboard handler in the §11 middleware
// (RequestID + Recovery; slice 3 adds sessionAuth; AuthLimit on
// /login). The full chain is:
//
//	RequestID → Recovery → handler
//
// Order matters: RequestID must come first so even Recovery's 500
// response carries the id, and Recovery must wrap the inner handler
// so a template panic returns 500 instead of taking the daemon down.
func (s *server) dashboardChain(h http.Handler) http.Handler {
	// http.HandlerFunc is also http.Handler so middleware.RequestID
	// accepts it directly. Build inside-out.
	h = middleware.RequestID(h)
	h = middleware.Recovery(s.log)(h)
	return h
}

// accountHandler is a handler that has already resolved the caller's account.
type accountHandler func(w http.ResponseWriter, r *http.Request, acct state.Account)

// auth authenticates by API-key hash and rejects inactive accounts (spec §11).
//
// Carve-out for G6 (spec §17 G6, ADR-021): while an account is in
// deleted_pending, the customer still needs to reach
//   - GET    /v1/account/export  (final export during grace)
//   - DELETE /v1/account         (idempotent re-DEL)
//   - POST   /v1/account/restore (cancel the deletion)
// All other routes still 402 with CodeBillingPastDue during grace
// because the work surface (deploy, build, park live instances) is
// already torn down.
func (s *server) auth(next accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if !api.ValidAPIKeyFormat(tok) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "provide a valid API key as a Bearer token"))
			return
		}
		acct, err := s.store.AccountByKeyHash(r.Context(), api.HashAPIKey(tok))
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "unknown API key"))
			return
		}
		if !acct.Active() {
			if !(acct.Status == state.AccountDeletedPending && isAccountScopedPath(r.URL.Path)) {
				api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
					"Account suspended", "resolve billing to continue: https://DOMAIN/billing"))
				return
			}
		}
		next(w, r, acct)
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
