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
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedDevAccount creates a Free account whose API key is the given token.
func seedDevAccount(ctx context.Context, store state.Store, token string) error {
	if !api.ValidAPIKeyFormat(token) {
		return fmt.Errorf("FAAS_DEV_TOKEN is not a valid API key (want %s… format)", api.APIKeyPrefix)
	}
	acct, err := store.CreateAccount(ctx, "dev@local", api.PlanFree)
	if err != nil {
		return err
	}
	_, err = store.CreateAPIKey(ctx, acct.ID, api.HashAPIKey(token), "dev")
	return err
}

// listenAddr is the bind address for apid. Behind gatewayd; not a public
// listener.
const listenAddr = "127.0.0.1:8081"

// runDeps is the DI seam for run — same pattern as vmmd / gatewayd so we can
// exercise the listener lifecycle without binding :8081 from tests.
type runDeps struct {
	listen   func(network, addr string) (net.Listener, error)
	store    func() state.Store
	notif    func() Notifier
	getenv   func(string) string
	newSrv   func(addr string, h http.Handler) *http.Server
	bgBefore func(ctx context.Context, log *slog.Logger, srv *server) // optional pre-listen hook (e.g. DNS poller)
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
	}
	return runWithDeps(ctx, log, deps)
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
	mailer := newLogMailer(log)
	// M7.5: githubd socket path (ADR-012). Empty = stub client (every
	// method returns api.Problem{Code:"githubd_not_ready"}), which is
	// fine until githubd is actually deployed on this host.
	githubd := newGithubdClient(deps.getenv("FAAS_GITHUBD_SOCKET"), log)
	srv := newServerWithDeps(store, log, deps.getenv("FAAS_APPS_DOMAIN"), deps.notif(), stripeSecret, mailer, githubd)

	// Optional pre-listen hook (DNS poller in production; nil in tests).
	if deps.bgBefore != nil {
		deps.bgBefore(ctx, log, srv)
	}

	httpSrv := deps.newSrv(listenAddr, srv.handler())

	l, err := deps.listen("tcp", listenAddr)
	if err != nil {
		return err
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
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// pgNotifier is the production Notifier — it just delegates to db.Notify.
type pgNotifier struct {
	pool *pgxpool.Pool
}

func (p pgNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.pool, channel, payload)
}
