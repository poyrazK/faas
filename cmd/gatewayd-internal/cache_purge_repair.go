package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	responseCachePurgePollInterval  = 2 * time.Second
	responseCachePurgeRetention     = 30 * 24 * time.Hour
	responseCachePurgePruneInterval = 24 * time.Hour
	responseCachePurgeBatch         = 256
)

type responseCachePurgeRepairStore interface {
	LatestResponseCachePurgeID(context.Context) (int64, error)
	ListResponseCachePurgesAfter(context.Context, int64, int) ([]state.ResponseCachePurgeChange, error)
	BootstrapGatewayResponseCachePurgeCursor(context.Context, string) (int64, error)
	UpsertGatewayResponseCachePurgeWatermark(context.Context, string, int64) error
	PruneResponseCachePurgeChangeLog(context.Context, time.Time) (int64, error)
}

type responseCachePurgeRepairInvalidator interface {
	PurgeResponseCacheByApp(string) error
	PurgeResponseCacheAll() error
	InvalidateResponseCacheByPath(string, string) error
	InvalidateResponseCacheByTag(string, string) error
}

var _ responseCachePurgeRepairStore = (*state.PgStore)(nil)

// repairDurableResponseCachePurges applies one bounded ledger page. It only
// advances the cursor after every L1/L2 invalidation succeeds, so a failed
// shared-cache purge is replayed instead of reported as complete.
func repairDurableResponseCachePurges(
	ctx context.Context,
	store responseCachePurgeRepairStore,
	inv responseCachePurgeRepairInvalidator,
	lastID *int64,
	log *slog.Logger,
) (int, error) {
	changes, err := store.ListResponseCachePurgesAfter(ctx, *lastID, responseCachePurgeBatch)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}
	fromID := *lastID
	nextID := *lastID
	for _, change := range changes {
		if change.ID <= nextID {
			continue
		}
		if strings.TrimSpace(change.AppID) == "" || (change.PathGlob != "" && change.Tag != "") {
			return 0, fmt.Errorf("response-cache purge %d has invalid scope", change.ID)
		}
		if change.Tag != "" {
			tag, err := api.NormalizeCacheTag(change.Tag)
			if err != nil {
				return 0, fmt.Errorf("response-cache purge %d has invalid tag: %w", change.ID, err)
			}
			if err := inv.InvalidateResponseCacheByTag(change.AppID, tag); err != nil {
				return 0, fmt.Errorf("apply response-cache purge %d by tag: %w", change.ID, err)
			}
		} else if change.PathGlob == "" || change.PathGlob == "*" {
			if err := inv.PurgeResponseCacheByApp(change.AppID); err != nil {
				return 0, fmt.Errorf("apply response-cache purge %d for app: %w", change.ID, err)
			}
		} else if err := inv.InvalidateResponseCacheByPath(change.AppID, change.PathGlob); err != nil {
			return 0, fmt.Errorf("apply response-cache purge %d by path: %w", change.ID, err)
		}
		nextID = change.ID
	}
	if nextID == *lastID {
		return 0, nil
	}
	*lastID = nextID
	if log != nil {
		log.Info("gatewayd: replayed response-cache purges",
			"from_id", fromID,
			"to_id", *lastID,
			"rows", len(changes))
	}
	return len(changes), nil
}

// watchDurableResponseCachePurges complements cache_purge_requested. Named
// gateways resume their durable position (or a serving peer's position for a
// new node); an unnamed single-box install has no fleet cursor, so it performs
// one strict shared-cache flush before establishing its local baseline.
func watchDurableResponseCachePurges(
	ctx context.Context,
	store responseCachePurgeRepairStore,
	inv responseCachePurgeRepairInvalidator,
	log *slog.Logger,
	nodeName string,
) {
	if store == nil || inv == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	nodeName = strings.TrimSpace(nodeName)
	lastID := int64(0)
	baselineReady := false
	if nodeName == "" {
		if latest, err := store.LatestResponseCachePurgeID(ctx); err != nil {
			log.Warn("gatewayd: response-cache purge baseline failed", "err", err)
		} else if err := inv.PurgeResponseCacheAll(); err != nil {
			log.Warn("gatewayd: initial response-cache purge failed", "err", err)
		} else {
			// The baseline is read before the full flush: requests already
			// committed are covered by that flush, while any later commit stays
			// above the cursor for replay (or its fast notification).
			lastID = latest
			baselineReady = true
		}
	} else if cursor, err := store.BootstrapGatewayResponseCachePurgeCursor(ctx, nodeName); err != nil {
		log.Warn("gatewayd: response-cache purge cursor bootstrap failed", "node", nodeName, "err", err)
	} else {
		lastID = cursor
		baselineReady = true
	}

	poll := time.NewTicker(responseCachePurgePollInterval)
	defer poll.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if !baselineReady {
				var cursor int64
				var err error
				if nodeName == "" {
					cursor, err = store.LatestResponseCachePurgeID(pollCtx)
					if err == nil {
						err = inv.PurgeResponseCacheAll()
					}
				} else {
					cursor, err = store.BootstrapGatewayResponseCachePurgeCursor(pollCtx, nodeName)
				}
				if err != nil {
					cancel()
					log.Warn("gatewayd: response-cache purge cursor retry failed", "node", nodeName, "err", err)
					continue
				}
				lastID = cursor
				baselineReady = true
			}
			_, err := repairDurableResponseCachePurges(pollCtx, store, inv, &lastID, log)
			if err != nil {
				cancel()
				log.Warn("gatewayd: response-cache purge replay failed", "node", nodeName, "revision", lastID, "err", err)
				continue
			}
			if nodeName != "" {
				if err := store.UpsertGatewayResponseCachePurgeWatermark(pollCtx, nodeName, lastID); err != nil {
					cancel()
					log.Warn("gatewayd: publish response-cache purge watermark failed", "node", nodeName, "revision", lastID, "err", err)
					continue
				}
			}
			cancel()
			now := time.Now().UTC()
			if lastPrune.IsZero() || now.Sub(lastPrune) >= responseCachePurgePruneInterval {
				pruneCtx, pruneCancel := context.WithTimeout(ctx, 5*time.Second)
				if _, pruneErr := store.PruneResponseCachePurgeChangeLog(pruneCtx, now.Add(-responseCachePurgeRetention)); pruneErr != nil {
					log.Warn("gatewayd: response-cache purge log prune failed", "err", pruneErr)
				}
				pruneCancel()
				lastPrune = now
			}
		}
	}
}
