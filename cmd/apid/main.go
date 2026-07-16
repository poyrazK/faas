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

func main() {
	wire.Daemon("apid", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	store := state.NewMemStore() // TODO(M5): swap for the Postgres store

	// Dev-only: seed a Free account bound to $FAAS_DEV_TOKEN so the CLI can be
	// exercised end-to-end without the (browser-paste) signup flow. Never set in
	// production — the Postgres store + real login supersede this.
	if tok := os.Getenv("FAAS_DEV_TOKEN"); tok != "" {
		if err := seedDevAccount(ctx, store, tok); err != nil {
			return err
		}
		log.Warn("dev account seeded from FAAS_DEV_TOKEN — do not use in production")
	}

	srv := newServer(store, log, os.Getenv("FAAS_APPS_DOMAIN"))

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("apid listening", "addr", listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
