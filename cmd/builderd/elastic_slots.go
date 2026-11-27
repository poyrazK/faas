package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	builderdpkg "github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/builderslots"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// startElasticSlots wires elastic builder slots (ADR-20261118154632): the
// node's builder_slots entry and builder_elastic_slots follow the runtime
// config watcher, the give-back loop runs for as long as builderd does, and
// up to api.BuilderSlotsMax-1 extra poll workers claim builds while a slot is
// free. With the flag off (the default) none of them claims or cancels
// anything.
func startElasticSlots(ctx, runCtx context.Context, cfg *Config, store *state.PgStore, pool *pgxpool.Pool, b *builderdpkg.Builderd, pollInterval time.Duration, log *slog.Logger) {
	elastic := builderdpkg.NewElasticSlots(builderHeartbeatNodeName(cfg), slotCPUs(os.Getenv), builderslots.NewReader().Read, log)
	b.WithElasticSlots(elastic)
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyBuilderElasticSlots, runtimeconfig.KeyBuilderSlots},
		func(_ context.Context, key string, value json.RawMessage, _ int64) error {
			return applyElasticSlots(elastic, key, value, log)
		}, log)
	watcher.Daemon = "builderd"
	if err := watcher.Reconcile(ctx); err != nil {
		log.Warn("builderd: initial builder slot settings read failed; elastic slots stay off", "err", err)
	}
	go func() {
		if err := watcher.Run(ctx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("builderd: builder slot settings watcher exited", "err", err)
		}
	}()
	go b.RunBorrowedGiveBack(runCtx)
	for i := 1; i < api.BuilderSlotsMax; i++ {
		go workerLoopWithMetrics(runCtx, elasticWorker{b: b, index: i}, pollInterval, log, nil)
	}
}

// builderSlotCPUsEnv overrides the CPU count builder_slots' max is clamped to.
// Acceptance hosts have fewer CPUs than a compute node; production leaves it
// unset.
const builderSlotCPUsEnv = "FAAS_BUILDER_SLOT_CPUS"

func slotCPUs(getenv func(string) string) int {
	if n, err := strconv.Atoi(getenv(builderSlotCPUsEnv)); err == nil && n > 0 {
		return n
	}
	return runtime.NumCPU()
}

func applyElasticSlots(elastic *builderdpkg.ElasticSlots, key string, value json.RawMessage, log *slog.Logger) error {
	switch key {
	case runtimeconfig.KeyBuilderElasticSlots:
		mode, err := runtimeconfig.String(value)
		if err != nil {
			return err
		}
		if !api.ValidBuilderElasticSlots(mode) {
			return fmt.Errorf("builderd: builder_elastic_slots %q is not off or on", mode)
		}
		on := mode == api.BuilderElasticSlotsOn
		if on != elastic.Enabled() {
			log.Warn("builderd: elastic builder slots changed", "on", on)
		}
		elastic.SetEnabled(on)
	case runtimeconfig.KeyBuilderSlots:
		parsed, err := builderslots.Parse(value)
		if err != nil {
			return err
		}
		elastic.SetConfig(parsed)
	}
	return nil
}

// elasticWorker is extra poll worker index: it claims only while elastic
// slots leave a slot it could fill.
type elasticWorker struct {
	b     *builderdpkg.Builderd
	index int
}

func (w elasticWorker) ProcessNext(ctx context.Context) (builderdpkg.BuildResult, error) {
	return w.b.ProcessNextElastic(ctx, w.index)
}
