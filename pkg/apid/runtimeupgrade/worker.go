package runtimeupgrade

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// RunWorker observes the private executor for systemd readiness/liveness. Only
// explicitly selected apid worker startup calls it; it starts no listeners.
func RunWorker(ctx context.Context, log *slog.Logger, store state.RuntimeUpgradeOperationStore) error {
	live := wire.NewLiveness()
	live.Register("runtime_upgrade", api.RuntimeUpgradeOperationLease+api.RuntimeUpgradeWorkerRetryMax+api.RuntimeUpgradeOperationInterval)
	stopWatchdog := daemonunit.WatchdogFromEnv(ctx, live.Healthy)
	defer stopWatchdog()
	var ready atomic.Bool
	stopReady := daemonunit.NotifyReadyWhen(ctx, ready.Load)
	defer stopReady()
	executor := Executor{Store: store, Observe: func(err error) {
		live.Beat("runtime_upgrade") // errors are completed work; a hung call is not
		if err != nil {
			// Database errors can include credentials/values; retain no raw text.
			log.Warn("private runtime upgrade iteration failed; retrying after backoff")
			return
		}
		ready.Store(true) // first successful claim/idle poll establishes readiness
	}}
	return executor.Run(ctx)
}
