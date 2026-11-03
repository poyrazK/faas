package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// profileCapturesCapability is the product capability whose platform flag
// releases on-demand captures to an account (ADR-1155, ADR-20261023115741).
const profileCapturesCapability = "profile-captures"

// startProfileCapturesRelease keeps the platform flags current through the
// runtime-config watcher and returns the per-account decision of
// profile-captures, so imaged stamps on-demand collectors only into
// deployments of accounts the preview is released to.
func startProfileCapturesRelease(ctx context.Context, getenv func(string) string, store state.Store, pool *pgxpool.Pool, log *slog.Logger) (func(platformflags.Subject) bool, error) {
	flags := platformflags.NewSet(capflags.Bootstrap(getenv))
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("imaged: load capability catalog: %w", err)
	}
	keys, apply := runtimeconfig.WithPlatformFlags(nil, nil, flags)
	watcher := runtimeconfig.New(store, pool, keys, apply, log)
	watcher.Daemon = "imaged" // ADR-1700 apply receipts
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("imaged: initial platform flag reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("imaged: platform flag watcher exited", "err", err)
		}
	}()
	return func(subject platformflags.Subject) bool {
		return decider.Enabled(profileCapturesCapability, subject)
	}, nil
}
