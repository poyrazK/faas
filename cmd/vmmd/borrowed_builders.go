package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// startBorrowedBuilderWatcher keeps builder_elastic_slots applied to vmmd's
// borrowed builders (ADR-20261118154632). vmmd starts with it off; its
// forced give-back runs whenever a borrowed builder exists, whatever the
// flag says.
func startBorrowedBuilderWatcher(ctx context.Context, store state.Store, pool *pgxpool.Pool, borrowed *vmmdgrpc.BorrowedBuilders, log *slog.Logger) {
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyBuilderElasticSlots},
		func(_ context.Context, key string, value json.RawMessage, _ int64) error {
			return applyBuilderElasticSlots(borrowed, key, value)
		}, log)
	watcher.Daemon = "vmmd"
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("vmmd: initial builder_elastic_slots reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("vmmd: builder_elastic_slots watcher exited", "err", err)
		}
	}()
}

func applyBuilderElasticSlots(borrowed *vmmdgrpc.BorrowedBuilders, key string, value json.RawMessage) error {
	if key != runtimeconfig.KeyBuilderElasticSlots {
		return nil
	}
	mode, err := runtimeconfig.String(value)
	if err != nil {
		return err
	}
	if !api.ValidBuilderElasticSlots(mode) {
		return fmt.Errorf("vmmd: builder_elastic_slots %q is not off or on", mode)
	}
	borrowed.SetEnabled(mode == api.BuilderElasticSlotsOn)
	return nil
}
