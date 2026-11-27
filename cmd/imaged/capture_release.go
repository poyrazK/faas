package main

import (
	"context"
	"encoding/json"
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

// verificationContractsCapability releases verification contracts to an
// account (ADR-20261122220000).
const verificationContractsCapability = "verification-contracts"

// platformReleases are imaged's per-account platform-flag decisions.
type platformReleases struct {
	profileCaptures    func(platformflags.Subject) bool
	postgresPoolSizing func(platformflags.Subject) bool
	// verificationContracts releases gregale.verify.yaml checks
	// (ADR-20261122220000).
	verificationContracts func(platformflags.Subject) bool
}

// startPlatformReleases keeps the platform flags current through the
// runtime-config watcher and returns imaged's per-account decisions: the
// profile-captures capability, so on-demand collectors are stamped only into
// deployments of accounts the preview is released to (ADR-1155), and the
// postgres_pool_sizing flag (ADR-20261104141122). The same watcher applies the
// request-concurrency keys (ADR-20261101215228), so the pool size imaged
// stamps follows the per-instance limit the gateways and schedd enforce.
func startPlatformReleases(ctx context.Context, getenv func(string) string, store state.Store, pool *pgxpool.Pool, log *slog.Logger) (platformReleases, error) {
	flags := platformflags.NewSet(capflags.Bootstrap(getenv))
	decider, err := capflags.Load(flags.Snapshot)
	if err != nil {
		return platformReleases{}, fmt.Errorf("imaged: load capability catalog: %w", err)
	}
	keys, apply := runtimeKeys(flags, &runtimeconfig.RequestConcurrency{}, log)
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
	return platformReleases{
		profileCaptures: func(subject platformflags.Subject) bool {
			return decider.Enabled(profileCapturesCapability, subject)
		},
		postgresPoolSizing: func(subject platformflags.Subject) bool {
			return flags.Enabled(platformflags.PostgresPoolSizing, subject)
		},
		verificationContracts: func(subject platformflags.Subject) bool {
			return decider.Enabled(verificationContractsCapability, subject)
		},
	}, nil
}

// runtimeKeys are the runtime-config keys imaged's watcher applies: the
// platform flags and the request-concurrency keys (ADR-20261101215228), which
// set the per-instance limit api.Limits.PostgresPoolMax resolves for the pool
// size imaged stamps (ADR-20261104141122).
func runtimeKeys(flags *platformflags.Set, requestConcurrency *runtimeconfig.RequestConcurrency, log *slog.Logger) ([]string, runtimeconfig.ApplyFunc) {
	return runtimeconfig.WithPlatformFlags(runtimeconfig.RequestConcurrencyKeys,
		func(_ context.Context, key string, value json.RawMessage, _ int64) error {
			handled, err := requestConcurrency.Apply(key, value)
			if handled && err == nil {
				log.Info("imaged: applied request limit setting (ADR-20261101215228)", "key", key, "value", string(value))
			}
			return err
		}, flags)
}
