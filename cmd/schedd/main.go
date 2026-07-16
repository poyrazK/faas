// Command schedd — scheduler and instance-lifecycle owner (spec §4.3).
//
// schedd is the ONLY writer to the instances table and the sole owner of the
// state machine (spec §Component ownership, §6). It runs admission control, the
// idle reaper, eviction, and cron in one process — single writer, no distributed
// locking. The Postgres-backed wake/park loop is driven from M5 (apid + PG); this
// milestone lands the tested policy core (admission ledger, reaper, eviction).
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("schedd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	ledger := sched.NewLedger()
	log.Info("schedd ready",
		"ram_ceiling_mb", api.RAMAdmissionCeilingMB,
		"eviction_threshold_mb", sched.EvictionThresholdMB,
		"vcpu_slots", api.VCPUSlots)

	// Idle reaper cadence (spec §4.3: every 10 s). Until the instances table is
	// wired (M5), this surfaces the live ledger; the real reaper reads the table,
	// calls sched.ReapIdle / SelectEvictions, and drives park via vmmd.
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			log.Debug("reaper tick", "resident_mb", ledger.ResidentRAM(), "headroom_mb", ledger.HeadroomMB())
		}
	}
}
