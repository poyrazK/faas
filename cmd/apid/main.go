// Command apid — control API (spec §4.2).
//
// apid is the public REST API, the auth boundary, and the ONLY writer to
// customer-intent tables (accounts, apps, deployments, domains). It validates
// plan quotas before any work happens and never calls vmmd/builderd directly —
// it writes rows and notifies owners via pg_notify (spec §Component ownership).
//
// M5 wires the service against an in-memory store so the API + quota logic runs
// end-to-end; the Postgres-backed store (sqlc over migrations/) drops in behind
// the same state.Store interface.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

const listenAddr = "127.0.0.1:8081" // behind gatewayd; not a public listener

// runDeps is the DI seam for run — same pattern as vmmd / gatewayd so we can
// exercise the listener lifecycle without binding :8081 from tests.
type runDeps struct {
	listen  func(network, addr string) (net.Listener, error)
	store   func() state.Store
	getenv  func(string) string
	newSrv  func(addr string, h http.Handler) *http.Server
}

func defaultDeps() runDeps {
	return runDeps{
		listen: net.Listen,
		store:  func() state.Store { return state.NewMemStore() },
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
	return runWithDeps(ctx, log, defaultDeps())
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

	srv := newServer(store, log, deps.getenv("FAAS_APPS_DOMAIN"))

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
		return httpSrv.Shutdown(shutdownCtx)
	}
}
