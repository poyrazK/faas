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
// (ADR-20261023115741) and returns the decision githubd uses to ignore pushes
// and pull requests for a capability that is off. The subject carries the
// resource's organization and plan, so organization and plan rules apply as
// they do in apid (ADR-20261105090226). Until the first reconcile succeeds,
// tier defaults decide.
func startCapabilityFlags(ctx context.Context, store state.Store, pool *pgxpool.Pool, log *slog.Logger) (func(capability string, subject platformflags.Subject) bool, error) {
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
	return func(capability string, subject platformflags.Subject) bool {
		return decider.Enabled(capability, subject)
	}, nil
}
