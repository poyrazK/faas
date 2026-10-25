package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// This worker resumes existing intents only. It cannot create a drain request,
// fence an app, change credentials, or activate a customer cutover.
func startManagedPostgresDrainRecovery(ctx context.Context, engine *sched.Engine, store environmentQualificationRecoveryNodeStore, ownerNodeID string, log *slog.Logger) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		after := ""
		recoverPage := func() {
			owner, err := environmentQualificationRecoveryNodeID(ctx, store, ownerNodeID)
			if err != nil {
				log.Warn("schedd: cutover drain owner lookup failed", "err", err)
				return
			}
			page, err := engine.RecoverManagedPostgresDrains(ctx, owner, after, api.ManagedPostgresCutoverDrainBatchMax)
			if page.NextCursor == after && after != "" {
				after = ""
				err = errors.Join(err, state.ErrConflict)
			} else {
				after = page.NextCursor
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("schedd: cutover drain recovery failed", "examined", page.Examined, "retired", page.Retired, "err", err)
			}
		}
		recoverPage()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recoverPage()
			}
		}
	}()
}

// databaseSwitchDrainEngine captures and retires drain plans.
type databaseSwitchDrainEngine interface {
	PlanManagedPostgresCutoverDrain(ctx context.Context, cutoverID string) (state.ManagedPostgresDrainPlan, error)
	RecoverManagedPostgresDrains(ctx context.Context, ownerNodeID, afterID string, limit int) (sched.ManagedPostgresDrainRecoveryPage, error)
}

// startDatabaseSwitchDrainCapture captures the drain plan a rollback
// database switch asked for (ADR-20261023181931) and retires its members
// right away instead of on the next recovery tick: the app is fenced and
// not serving while it waits. It acts only for apps this schedd owns;
// capture for another owner's app fails its authority check.
func startDatabaseSwitchDrainCapture(ctx context.Context, engine databaseSwitchDrainEngine, store any, nodes environmentQualificationRecoveryNodeStore, ownerNodeID string, log *slog.Logger) {
	switches, ok := store.(state.DatabaseSwitchStore)
	if !ok || engine == nil || nodes == nil || log == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(api.DatabaseSwitchCheckInterval)
		defer ticker.Stop()
		for {
			captureDatabaseSwitchDrains(ctx, engine, switches, nodes, ownerNodeID, log)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func captureDatabaseSwitchDrains(ctx context.Context, engine databaseSwitchDrainEngine, switches state.DatabaseSwitchStore,
	nodes environmentQualificationRecoveryNodeStore, ownerNodeID string, log *slog.Logger) {
	pending, err := switches.DrainingDatabaseSwitches(ctx, api.DatabaseSwitchBatch)
	if err != nil || len(pending) == 0 {
		if err != nil && ctx.Err() == nil {
			log.Warn("schedd: list database switches waiting for a drain", "err", err)
		}
		return
	}
	owner, err := environmentQualificationRecoveryNodeID(ctx, nodes, ownerNodeID)
	if err != nil {
		log.Warn("schedd: database switch drain owner lookup failed", "err", err)
		return
	}
	captured := false
	for _, sw := range pending {
		if _, err := engine.PlanManagedPostgresCutoverDrain(ctx, sw.CutoverID); err != nil {
			if !errors.Is(err, state.ErrConflict) && ctx.Err() == nil {
				log.Warn("schedd: capture database switch drain", "switch", sw.ID, "cutover", sw.CutoverID, "err", err)
			}
			continue
		}
		captured = true
	}
	if !captured {
		return
	}
	if page, err := engine.RecoverManagedPostgresDrains(ctx, owner, "", api.ManagedPostgresCutoverDrainBatchMax); err != nil && ctx.Err() == nil {
		log.Warn("schedd: database switch drain retirement", "examined", page.Examined, "retired", page.Retired, "err", err)
	}
}
