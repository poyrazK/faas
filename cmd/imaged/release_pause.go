package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// startReleasePauseWatcher follows the break-glass release pause (ADR
// 20261022140000) and returns its current value. While it is on, an approved
// release held before its release command does not resume
// (ADR-20261020085706 amendment 13). A failed first read leaves releases
// flowing, as in builderd; the watcher keeps retrying.
func startReleasePauseWatcher(ctx context.Context, store *state.PgStore, pool *pgxpool.Pool, log *slog.Logger) func() bool {
	paused := runtimeconfig.NewBoolFlag(false)
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyPlatformReleasePause},
		func(_ context.Context, _ string, value json.RawMessage, _ int64) error {
			on, err := runtimeconfig.Bool(value)
			if err != nil {
				return err
			}
			if on != paused.Load() {
				log.Warn("imaged: break-glass release pause changed", "paused", on)
			}
			paused.Store(on)
			return nil
		}, log)
	watcher.Daemon = "imaged"
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("imaged: initial release pause read failed; releases resume", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("imaged: release pause watcher exited", "err", err)
		}
	}()
	return paused.Load
}
