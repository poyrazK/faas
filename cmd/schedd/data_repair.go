package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Cleanup runs independently of the creation gate and current executor
// bindings. Enabling creation additionally requires pinned runner metadata and
// the same exact-scope bindings apid admitted into the durable task.
func startDataRepairRehearsals(ctx context.Context, engine *sched.Engine, store *state.PgStore, vmm *sched.VMMRouter, owner string, seedReady bool, log *slog.Logger) error {
	enabled := strings.TrimSpace(os.Getenv("FAAS_DATA_REPAIR_REHEARSAL_DISPATCH")) == "1"
	runner, configured, err := core.RunnerFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("schedd: incomplete data repair runner metadata")
	}
	bindings, err := repairapi.LoadBindings(os.Getenv("FAAS_DATA_REPAIR_BINDINGS_PATH"), os.Getenv)
	if err != nil {
		return fmt.Errorf("schedd: invalid data repair executor bindings")
	}
	var dispatch []sched.DataRepairRehearsalBinding
	for _, b := range bindings {
		if !b.ScheduledRehearsal {
			continue
		}
		x, ok := b.Executor.(sched.DataRepairRehearsalExecutor)
		if !ok {
			return fmt.Errorf("schedd: data repair executor cannot export snapshots")
		}
		dispatch = append(dispatch, sched.DataRepairRehearsalBinding{Scope: b.Scope, Identity: b.Identity, Executor: x})
	}
	if enabled && (!configured || !seedReady || len(dispatch) == 0) {
		return fmt.Errorf("schedd: data repair dispatch requires runner metadata, bindings and a seeded ledger")
	}
	node, err := environmentQualificationRecoveryNodeID(ctx, store, owner)
	if err != nil {
		return fmt.Errorf("schedd: data repair node identity unavailable: %w", err)
	}
	c := &sched.DataRepairRehearsalCoordinator{Store: store, Admission: engine, VMM: vmm, Bindings: dispatch, Runner: runner, NodeID: node, Enabled: enabled, Log: log}
	go func() {
		if err := c.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("data repair rehearsal worker stopped", "error_class", "configuration")
		}
	}()
	return nil
}
