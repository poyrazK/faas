package main

import (
	"context"
	"log/slog"
	"time"
)

const (
	edgeRuleRepairPollInterval  = 2 * time.Second
	edgeRuleRepairRetention     = 30 * 24 * time.Hour
	edgeRuleRepairPruneInterval = 24 * time.Hour
)

type edgeRuleRepairStore interface {
	LatestEdgeRuleChangeID(context.Context) (int64, error)
	PruneEdgeRuleChangeLog(context.Context, time.Time) (int64, error)
}

type edgeRuleRepairInvalidator interface {
	ResetEdgeRules()
	InvalidateResponseCacheAll()
}

// repairDurableEdgeRuleChanges turns a durable high-water mark into a
// conservative cache repair. The event payload is informational; flushing
// both policy and response caches is safe even when several mutations were
// coalesced while a gateway was disconnected.
func repairDurableEdgeRuleChanges(ctx context.Context, store edgeRuleRepairStore, inv edgeRuleRepairInvalidator, lastID *int64, log *slog.Logger) (bool, error) {
	latest, err := store.LatestEdgeRuleChangeID(ctx)
	if err != nil {
		return false, err
	}
	if latest <= *lastID {
		return false, nil
	}
	inv.ResetEdgeRules()
	inv.InvalidateResponseCacheAll()
	previous := *lastID
	*lastID = latest
	if log != nil {
		log.Info("gatewayd: repaired missed edge-rule invalidation", "from_id", previous, "to_id", latest)
	}
	return true, nil
}

// watchDurableEdgeRuleChanges complements LISTEN/NOTIFY. A notification is
// still the fast path, while this loop closes the restart/disconnect gap by
// polling the transactionally populated edge-rule ledger from every gateway.
func watchDurableEdgeRuleChanges(ctx context.Context, store edgeRuleRepairStore, inv edgeRuleRepairInvalidator, log *slog.Logger) {
	if store == nil || inv == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	lastID := int64(0)
	if latest, err := store.LatestEdgeRuleChangeID(ctx); err != nil {
		log.Warn("gatewayd: edge-rule repair baseline failed", "err", err)
	} else {
		lastID = latest
	}

	poll := time.NewTicker(edgeRuleRepairPollInterval)
	defer poll.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := repairDurableEdgeRuleChanges(pollCtx, store, inv, &lastID, log)
			cancel()
			if err != nil {
				log.Warn("gatewayd: edge-rule repair poll failed", "err", err)
				continue
			}
			now := time.Now().UTC()
			if lastPrune.IsZero() || now.Sub(lastPrune) >= edgeRuleRepairPruneInterval {
				pruneCtx, pruneCancel := context.WithTimeout(ctx, 5*time.Second)
				if _, pruneErr := store.PruneEdgeRuleChangeLog(pruneCtx, now.Add(-edgeRuleRepairRetention)); pruneErr != nil {
					log.Warn("gatewayd: edge-rule repair ledger prune failed", "err", pruneErr)
				}
				pruneCancel()
				lastPrune = now
			}
		}
	}
}
