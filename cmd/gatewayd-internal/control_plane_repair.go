package main

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

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
	UpsertGatewayControlPlaneWatermark(context.Context, string, string, int64) error
}

type controlPlaneRepairInvalidator interface {
	ResetApp(string)
	InvalidateRoutesForApp(string)
	InvalidateResponseCacheByApp(string)
	RefreshLiveTargets(context.Context, string) error
	RefreshDeploymentWeights(context.Context, string) error
}

var _ controlPlaneRepairStore = (*state.PgStore)(nil)

// repairDurableControlPlaneChanges applies one bounded ledger page. The
// ledger is broadcast, so every gateway owns a cursor and a missed
// LISTEN/NOTIFY event cannot leave one replica serving stale app or traffic
// state. Multiple rows for an app are coalesced into one refresh.
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
	trafficApps := make(map[string]struct{})
	fromID := *lastID
	nextID := *lastID
	for _, change := range changes {
		if change.ID <= nextID {
			continue
		}
		nextID = change.ID
		if change.AppID != "" {
			apps[change.AppID] = struct{}{}
			if change.ResourceType == "deployment_traffic" {
				trafficApps[change.AppID] = struct{}{}
			}
		}
	}
	appIDs := make([]string, 0, len(apps))
	for appID := range apps {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	for _, appID := range appIDs {
		inv.ResetApp(appID)
		inv.InvalidateRoutesForApp(appID)
		inv.InvalidateResponseCacheByApp(appID)
		if _, changed := trafficApps[appID]; changed {
			// The target set must be hydrated before the new weight table is
			// published, or a newly weighted deployment can briefly 503.
			if err := inv.RefreshLiveTargets(ctx, appID); err != nil {
				return 0, err
			}
			if err := inv.RefreshDeploymentWeights(ctx, appID); err != nil {
				return 0, err
			}
		}
	}
	// Never skip a committed policy change if a database refresh failed. All
	// invalidations above are idempotent, so the next poll can replay the page.
	*lastID = nextID
	if log != nil {
		log.Info("gatewayd: replayed control-plane changes",
			"from_id", fromID,
			"to_id", *lastID,
			"rows", len(changes),
			"apps", len(apps))
	}
	return len(changes), nil
}

// watchDurableControlPlaneChanges complements app_changed and traffic-change
// LISTEN paths.
// It starts at the current high-water mark because a fresh gateway has empty
// caches; thereafter every committed app or traffic mutation is replayed by ID.
func watchDurableControlPlaneChanges(ctx context.Context, store controlPlaneRepairStore, inv controlPlaneRepairInvalidator, log *slog.Logger, nodeName string) {
	if store == nil || inv == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	nodeName = strings.TrimSpace(nodeName)
	bootID := uuid.NewString()
	lastID := int64(0)
	baselineReady := false
	publishWatermark := func(publishCtx context.Context) {
		if nodeName == "" {
			return // unnamed single-box installs have no serving-fleet identity
		}
		if err := store.UpsertGatewayControlPlaneWatermark(publishCtx, nodeName, bootID, lastID); err != nil {
			log.Warn("gatewayd: publish control-plane watermark failed", "node", nodeName, "revision", lastID, "err", err)
		}
	}
	if latest, err := store.LatestControlPlaneChangeID(ctx); err != nil {
		log.Warn("gatewayd: control-plane repair baseline failed", "err", err)
	} else {
		lastID = latest
		baselineReady = true
		publishWatermark(ctx)
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
				if baselineErr != nil {
					cancel()
					log.Warn("gatewayd: control-plane repair baseline retry failed", "err", baselineErr)
					continue
				}
				lastID = latest
				baselineReady = true
				publishWatermark(pollCtx)
				cancel()
				continue
			}
			_, err := repairDurableControlPlaneChanges(pollCtx, store, inv, &lastID, log)
			if err != nil {
				cancel()
				log.Warn("gatewayd: control-plane repair poll failed", "err", err)
				continue
			}
			publishWatermark(pollCtx)
			cancel()
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
