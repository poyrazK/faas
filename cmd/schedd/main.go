// Command schedd — scheduler and instance-lifecycle owner (spec §4.3).
//
// schedd is the ONLY writer to the instances table and the sole owner of the
// state machine (spec §Component ownership, §6). It runs admission control, the
// idle reaper, eviction, and cron in one process — single writer, no distributed
// locking. M5+: the daemon subscribes to apid's pg_notify channels and runs the
// reaper + cron ticks on a state.Store backed by Postgres.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("schedd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	pool, err := db.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return err
	}

	store := state.NewPgStore(pool)
	ledger := sched.NewLedger()
	loop := sched.NewLoop(pool, store, ledger, log)

	log.Info("schedd ready",
		"ram_ceiling_mb", api.RAMAdmissionCeilingMB,
		"eviction_threshold_mb", sched.EvictionThresholdMB,
		"vcpu_slots", api.VCPUSlots)

	// Heartbeat the wire helper expects to see; the loop blocks.
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				log.Debug("reaper tick", "resident_mb", ledger.ResidentRAM(), "headroom_mb", ledger.HeadroomMB())
			}
		}
	}()
	return loop.Run(ctx)
}
