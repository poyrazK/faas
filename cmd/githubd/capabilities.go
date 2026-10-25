package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// startCapabilityFlags keeps the product capability flags current
// (ADR-20261023115741) and returns the per-account decision githubd uses to
// ignore pushes and pull requests for a capability that is off. githubd knows
// only the bound account, so flags it consumes take account rules alone.
// Until the first reconcile succeeds, tier defaults decide.
func startCapabilityFlags(ctx context.Context, store state.Store, pool *pgxpool.Pool, log *slog.Logger) (func(capability, accountID string) bool, error) {
	set := platformflags.NewSet(capflags.Bootstrap(os.Getenv))
	decider, err := capflags.Load(set.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("githubd: load capability catalog: %w", err)
	}
	keys, apply := runtimeconfig.WithPlatformFlags(nil, nil, set)
	watcher := runtimeconfig.New(store, pool, keys, apply, log)
	watcher.Daemon = "githubd"
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("githubd: initial platform flag reconcile failed; tier defaults decide", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("githubd: platform flag watcher stopped", "err", err)
		}
	}()
	return func(capability, accountID string) bool {
		return decider.Enabled(capability, platformflags.Subject{AccountID: accountID})
	}, nil
}
