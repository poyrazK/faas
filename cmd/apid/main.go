// Command apid — control API (spec §4.2).
//
// apid is the public REST API, the auth boundary, and the ONLY writer to
// customer-intent tables (accounts, apps, deployments, domains). It validates
// plan quotas before any work happens and never calls vmmd/builderd directly —
// it writes rows and notifies owners via pg_notify (spec §Component ownership).
//
// M5+: apid uses the pgx-backed state.PgStore against the same Postgres
// cluster schedd/imaged share; queries.sql is the SQL source of truth and
// pgstore.go adapts the result shape to the domain types. The CLI exercises
// apid through FAAS_DEV_TOKEN for local dev (memstore seed path stays for
// tests).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedDevAccount creates a Free account whose API key is the given token.
func seedDevAccount(ctx context.Context, store state.Store, token string) error {
	if !api.ValidAPIKeyFormat(token) {
		return fmt.Errorf("FAAS_DEV_TOKEN is not a valid API key (want %s… format)", api.APIKeyPrefix)
	}
	acct, err := store.AccountByEmail(ctx, "dev@local")
	if errors.Is(err, state.ErrNotFound) {
		acct, err = store.CreateAccount(ctx, "dev@local", api.PlanFree)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = store.CreateAPIKey(ctx, acct.ID, api.HashAPIKey(token), "dev")
	if err != nil && !errors.Is(err, state.ErrConflict) {
		return err
	}
	return nil
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// listenAddr is the bind address for apid. Behind gatewayd; not a public
// listener. Overridable via FAAS_APID_LISTEN so the e2e harness can pick a
// free port without colliding with a dev daemon on 8081.
var listenAddr = envOr("FAAS_APID_LISTEN", "127.0.0.1:8081")

// metricsAddr is the bind address for the apid /metrics listener
// (separate from the main listener so a port collision can't take the
// daemon down). Defaults to 127.0.0.1:9101 so an operator typo (or
// a missing env var in prod) can't accidentally expose the internal
// registry to the public network — series like apid_ops_total{op,code}
// leak auth-rejection rates and per-route traffic shape (review
// finding #1 on PR #132). Loopback bind is safe because the local
// Prometheus scrapes from the box itself. Set FAAS_APID_METRICS_ADDR=
// to disable the listener (unit tests that don't want a port reserved).
// Mirrors cmd/builderd/main.go's MetricsAddr pattern (PR #124).
var metricsAddr = envOr("FAAS_APID_METRICS_ADDR", "127.0.0.1:9101")

// runDeps is the DI seam for run — same pattern as vmmd / gatewayd so we can
// exercise the listener lifecycle without binding :8081 from tests.
type runDeps struct {
	listen   func(network, addr string) (net.Listener, error)
	store    func() state.Store
	notif    func() Notifier
	getenv   func(string) string
	newSrv   func(addr string, h http.Handler) *http.Server
	bgBefore func(ctx context.Context, log *slog.Logger, srv *server) // optional pre-listen hook (e.g. DNS poller)
	loginTTL time.Duration                                            // dashboard magic-link expiry
	// mailer is the outbound email sender (gap G4). Nil means "pick
	// from env at startup" via mail.SenderFromEnv — same pattern meterd
	// uses (cmd/meterd/main.go:82-87). Tests inject a stub.
	mailer mail.Sender
}

func defaultDeps() runDeps {
	return runDeps{
		listen: net.Listen,
		store:  func() state.Store { return state.NewMemStore() },
		notif:  func() Notifier { return noopNotifier{} },
		getenv: os.Getenv,
		newSrv: func(addr string, h http.Handler) *http.Server {
			return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		},
		loginTTL: 15 * time.Minute,
	}
}

func main() {
	wire.Daemon("apid", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("apid: open db: %w", err)
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return fmt.Errorf("apid: migrate: %w", err)
	}

	deps := defaultDeps()
	deps.store = func() state.Store { return state.NewPgStore(pool) }
	deps.notif = func() Notifier { return pgNotifier{pool: pool} }
	deps.bgBefore = func(ctx context.Context, log *slog.Logger, srv *server) {
		startDNSPoller(ctx, srv, log)
		// G6 grace timer (spec §17 G6, ADR-021): the 30-day deletion
		// grace sweep lives in apid (not meterd) because the write
		// side (DELETE /v1/account, POST /v1/account/restore) is here
		// and meterd owns quotas/billing only. Default Interval 60s
		// matches the grace-side precision we need; sweep is a
		// ListAllAccounts walk so it stays bounded by the customer
		// count on the one box.
		graceLoop := grace.New(grace.Params{
			Store:    srv.store,
			Mailer:   graceSenderAdapter{m: srv.mailer},
			Log:      log,
			Interval: graceIntervalFromEnv(log),
			Notif: func(ctx context.Context, ch, payload string) error {
				return srv.notif.Notify(ctx, ch, payload)
			},
		})
		go func() { _ = graceLoop.Run(ctx) }()
	}
	return runWithDeps(ctx, log, deps)
}

// graceSenderAdapter bridges apid's Mailer (which sends the apid
// Message struct) to pkg/grace.Sender (which takes primitive args).
// Kept inline so the production apid binary doesn't pull the apid
// Message type into pkg/grace — pkg/grace's signature is intentionally
// narrow so it has no apid dependency.
type graceSenderAdapter struct{ m Mailer }

func (g graceSenderAdapter) Send(ctx context.Context, to []string, subject, body string) error {
	return g.m.Send(ctx, Message{To: to, Subject: subject, TextBody: body})
}

// mailAdapter bridges pkg/mail.Sender (the cross-daemon outbound-email
// seam) to apid's internal Mailer interface. Same shape as
// graceSenderAdapter above but in the opposite direction: the apid
// Message type stays free of pkg/mail so daemons that link apid don't
// pull the mail deps transitively. Gap G4 closure: the production
// wire-up in runWithDeps wraps mail.SenderFromEnv(...)
// (Resend/Postmark/Log/Noop) in this adapter so magic-link + dunning +
// quota-warning + deletion-pending emails actually reach the customer.
type mailAdapter struct{ s mail.Sender }

// newMailerAdapter wraps a pkg/mail.Sender so it satisfies apid's
// Mailer interface. Returns noopMailer{} for a nil sender so callers
// never need to nil-check (matches newServerWithDeps's nil → noop
// convention).
func newMailerAdapter(s mail.Sender) Mailer {
	if s == nil {
		return noopMailer{}
	}
	return mailAdapter{s: s}
}

func (a mailAdapter) Send(ctx context.Context, m Message) error {
	return a.s.Send(ctx, mail.Message{
		To:       m.To,
		Subject:  m.Subject,
		TextBody: m.TextBody,
		HTMLBody: m.HTMLBody,
	})
}

// graceIntervalFromEnv reads FAAS_GRACE_INTERVAL to let the e2e test
// accelerate the sweep (default 60s is correct for production; a CI
// test sets it to a few hundred ms so the 30-day "grace expired"
// case runs in seconds, not minutes). Returns 0 to let pkg/grace
// fall back to its 60s default.
func graceIntervalFromEnv(log *slog.Logger) time.Duration {
	v := os.Getenv("FAAS_GRACE_INTERVAL")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		if log != nil {
			log.Warn("FAAS_GRACE_INTERVAL unparseable, using default",
				"value", v, "err", err)
		}
		return 0
	}
	return d
}

// dpaPathFromEnv resolves the DPA template path. Production wires an
// explicit FAAS_DPA_PATH pointing at the installed /etc/faas/dpa.md;
// when that's unset, fall back to <cwd>/docs/DPA.md if that file
// exists, so `go run ./cmd/apid` from the repo root serves the dev
// template without a setup step. When neither is set the handler
// returns 503 — a misconfigured production deploy is observable
// rather than silently empty (see handlers_account.go::dpaTemplate).
func dpaPathFromEnv(getenv func(string) string) string {
	if p := getenv("FAAS_DPA_PATH"); p != "" {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cwd, "docs", "DPA.md")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	store := deps.store()

	// Dev-only: seed a Free account bound to $FAAS_DEV_TOKEN so the CLI can be
	// exercised end-to-end without the (browser-paste) signup flow. Never set in
	// production — the Postgres store + real login supersede this.
	if tok := deps.getenv("FAAS_DEV_TOKEN"); tok != "" {
		if err := seedDevAccount(ctx, store, tok); err != nil {
			return err
		}
		log.Warn("dev account seeded from FAAS_DEV_TOKEN — do not use in production")
	}

	// M7: pass the Stripe webhook secret (env-loaded) and the mailer
	// (log-only until gap G4 is closed). Empty secret = dev mode (the
	// webhook accepts unsigned payloads; never deploy this way).
	stripeSecret := deps.getenv("STRIPE_WEBHOOK_SECRET")
	// Gap G4 closure (PR): wire the env-driven mail factory so prod
	// boots with FAAS_MAIL_TRANSPORT=resend and emails go out for real.
	// Tests + dev can keep mailer nil and the factory returns a log
	// sender — behaviour matches the pre-PR newLogMailer(log) wiring.
	m := deps.mailer
	if m == nil {
		m = mail.SenderFromEnv(deps.getenv, log)
	}
	mailer := newMailerAdapter(m)
	// M7.5: githubd socket path (ADR-012). Empty = stub client (every
	// method returns api.Problem{Code:"githubd_not_ready"}), which is
	// fine until githubd is actually deployed on this host.
	githubd := newGithubdClient(ctx, deps.getenv("FAAS_GITHUBD_SOCKET"), nil, log)
	// M7.5: dashboard session manager. Loads the 32-byte key from
	// FAAS_SESSION_KEY (hex-encoded); empty in dev = ephemeral key +
	// warning so the daemon still boots for local testing. Production
	// MUST set this to the contents of /etc/faas/secrets/session.key
	// (root:root 0400, spec §11).
	sessions, sessionsWarn := loadSessionManager(deps.getenv, log)
	if sessionsWarn != "" {
		log.Warn("session manager in dev mode; sessions reset on restart", "warning", sessionsWarn)
	}
	srv := newServerWithDeps(store, log, deps.getenv("FAAS_APPS_DOMAIN"), deps.notif(), stripeSecret, mailer, githubd, sessions, nil, deps.loginTTL, dpaPathFromEnv(deps.getenv))

	// Issue #98 / ADR-028: admin allowlist for /v1/compute-nodes.
	// Empty in dev = all admin routes 403 with code admin_required;
	// production sets FAAS_ADMIN_EMAILS to the operator team's
	// comma-separated addresses. The allowlist is read at startup,
	// so a config change requires a restart — acceptable for the
	// tiny operator surface that exists today.
	srv.WithAdminAllowlist(deps.getenv("FAAS_ADMIN_EMAILS"))

	// Prometheus registry + ops observer middleware (this PR).
	// Built unconditionally so /metrics works even with FAAS_APID_METRICS_ADDR
	// unset (the daemon stays up; only the listener is skipped below).
	ops := wire.NewOpsMetrics("apid")
	srv.WithOpsMetrics(ops)

	// Status page (spec §12 public surface). The Prometheus URL is
	// the local box's Prometheus installed by deploy/ansible/roles/
	// prometheus (default :9090 on the bridge). The HTML path defaults
	// to /etc/faas/statuspage/index.html; a dev override
	// (FAAS_STATUSPAGE_PATH) lets us point at deploy/statuspage/
	// index.html without installing.
	srv.WithStatusCache(
		deps.getenv("FAAS_PROMETHEUS_URL"),
		deps.getenv("FAAS_STATUSPAGE_PATH"),
	)

	// G2: load the host age recipient so the secrets PUT handler can seal.
	// vmmd owns the private half; we only need the public recipient string.
	// The recipient path is opt-in via FAAS_HOST_AGE_RECIPIENT_PATH — set in
	// production (and by the e2e harness) to the file vmmd writes
	// (/etc/faas/secrets/host.age.pub by default). When the env var is unset,
	// the var stays nil and PUT /secrets returns 503 — a loud, observable
	// signal that the box is misconfigured rather than a silent accept-and-
	// drop of plaintext. The unit tests don't set the var because the
	// handlers they're checking don't exercise the seal path.
	if recipientPath := deps.getenv("FAAS_HOST_AGE_RECIPIENT_PATH"); recipientPath != "" {
		r, err := secretbox.LoadRecipient(recipientPath)
		if err != nil {
			return fmt.Errorf("apid: load host age recipient %q: %w", recipientPath, err)
		}
		setSecretRecipient = func() *age.X25519Recipient { return r }
		log.Info("host age recipient loaded", "path", recipientPath)
	} else {
		log.Warn("FAAS_HOST_AGE_RECIPIENT_PATH unset — secrets PUT will return 503")
	}

	// Optional pre-listen hook (DNS poller in production; nil in tests).
	if deps.bgBefore != nil {
		deps.bgBefore(ctx, log, srv)
	}

	httpSrv := deps.newSrv(listenAddr, srv.handler())

	l, err := deps.listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	// Optional /metrics listener (this PR). Sits on its own bind
	// address so a port collision can't take the daemon down. Empty
	// FAAS_APID_METRICS_ADDR = no listener (the scrape observer is
	// still wired into the main mux via observeWrap; only the
	// listener is skipped). Mirrors cmd/builderd/main.go:146-157.
	var metricsSrv *http.Server
	if metricsAddr != "" {
		metricsSrv = &http.Server{
			Addr:              metricsAddr,
			Handler:           ops.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		mLis, err := net.Listen("tcp", metricsAddr)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: metrics listen %q: %w", metricsAddr, err)
		}
		go func() {
			log.Info("apid /metrics listening", "addr", metricsAddr)
			if err := metricsSrv.Serve(mLis); err != nil && err != http.ErrServerClosed {
				log.Error("apid /metrics serve", "err", err)
			}
		}()
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("apid listening", "addr", listenAddr)
		if err := httpSrv.Serve(l); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
		_ = httpSrv.Shutdown(shutdownCtx)
		if metricsSrv != nil {
			//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
			_ = metricsSrv.Shutdown(shutdownCtx)
		}
		return nil
	}
}

// pgNotifier is the production Notifier — it just delegates to db.Notify.
type pgNotifier struct {
	pool *pgxpool.Pool
}

func (p pgNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.pool, channel, payload)
}

// Subscribe hands the SSE handler a live channel stream from the
// Postgres pool. Returns immediately if no channels are requested.
func (p pgNotifier) Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error) {
	return db.Subscribe(ctx, p.pool, channels)
}
