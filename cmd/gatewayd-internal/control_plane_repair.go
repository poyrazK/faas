package main

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	controlPlaneRepairPollInterval  = 2 * time.Second
	controlPlaneRepairRetention     = 30 * 24 * time.Hour
	controlPlaneRepairPruneInterval = 24 * time.Hour
	controlPlaneRepairBatch         = 256
)

type controlPlaneRepairStore interface {
	LatestControlPlaneChangeID(context.Context) (int64, error)
	ListControlPlaneChangesAfter(context.Context, int64, int) ([]state.ControlPlaneChange, error)
	PruneControlPlaneChangeLog(context.Context, time.Time) (int64, error)
}

type controlPlaneRepairInvalidator interface {
	ResetApp(string)
	InvalidateResponseCacheByApp(string)
}

var _ controlPlaneRepairStore = (*state.PgStore)(nil)

// repairDurableControlPlaneChanges applies one bounded ledger page. The
// ledger is broadcast, so every gateway owns a cursor and a missed
// LISTEN/NOTIFY event cannot leave one replica serving stale app state.
// Multiple rows for an app are coalesced into one conservative invalidation.
func repairDurableControlPlaneChanges(
	ctx context.Context,
	store controlPlaneRepairStore,
	inv controlPlaneRepairInvalidator,
	lastID *int64,
	log *slog.Logger,
) (int, error) {
	changes, err := store.ListControlPlaneChangesAfter(ctx, *lastID, controlPlaneRepairBatch)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}
	apps := make(map[string]struct{}, len(changes))
	fromID := *lastID
	for _, change := range changes {
		if change.ID <= *lastID {
			continue
		}
		*lastID = change.ID
		if change.AppID != "" {
			apps[change.AppID] = struct{}{}
		}
	}
	appIDs := make([]string, 0, len(apps))
	for appID := range apps {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	for _, appID := range appIDs {
		inv.ResetApp(appID)
		inv.InvalidateResponseCacheByApp(appID)
	}
	if log != nil {
		log.Info("gatewayd: replayed control-plane cache invalidations",
			"from_id", fromID,
			"to_id", *lastID,
			"rows", len(changes),
			"apps", len(apps))
	}
	return len(changes), nil
}

// watchDurableControlPlaneChanges complements all app_changed LISTEN paths.
// It starts at the current high-water mark because a fresh gateway has empty
// caches; thereafter every committed app mutation is replayed by ID.
func watchDurableControlPlaneChanges(ctx context.Context, store controlPlaneRepairStore, inv controlPlaneRepairInvalidator, log *slog.Logger) {
	if store == nil || inv == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	lastID := int64(0)
	baselineReady := false
	if latest, err := store.LatestControlPlaneChangeID(ctx); err != nil {
		log.Warn("gatewayd: control-plane repair baseline failed", "err", err)
	} else {
		lastID = latest
		baselineReady = true
	}

	poll := time.NewTicker(controlPlaneRepairPollInterval)
	defer poll.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if !baselineReady {
				latest, baselineErr := store.LatestControlPlaneChangeID(pollCtx)
				cancel()
				if baselineErr != nil {
					log.Warn("gatewayd: control-plane repair baseline retry failed", "err", baselineErr)
					continue
				}
				lastID = latest
				baselineReady = true
				continue
			}
			_, err := repairDurableControlPlaneChanges(pollCtx, store, inv, &lastID, log)
			cancel()
			if err != nil {
				log.Warn("gatewayd: control-plane repair poll failed", "err", err)
				continue
			}
			now := time.Now().UTC()
			if lastPrune.IsZero() || now.Sub(lastPrune) >= controlPlaneRepairPruneInterval {
				pruneCtx, pruneCancel := context.WithTimeout(ctx, 5*time.Second)
				if _, pruneErr := store.PruneControlPlaneChangeLog(pruneCtx, now.Add(-controlPlaneRepairRetention)); pruneErr != nil {
					log.Warn("gatewayd: control-plane repair ledger prune failed", "err", pruneErr)
				}
				pruneCancel()
				lastPrune = now
			}
		}
	}
}
