package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	edgeRuleRepairPollInterval  = 2 * time.Second
	edgeRuleRepairRetention     = 30 * 24 * time.Hour
	edgeRuleRepairPruneInterval = 24 * time.Hour
)

// edgeRuleRepairReplayLimit bounds one targeted replay. A backlog at least
// this large (a long disconnect) is repaired with a wholesale flush instead.
const edgeRuleRepairReplayLimit = 500

type edgeRuleRepairStore interface {
	LatestEdgeRuleChangeID(context.Context) (int64, error)
	ListEdgeRuleChangesAfter(context.Context, int64, int) ([]state.EdgeRuleChange, error)
	PruneEdgeRuleChangeLog(context.Context, time.Time) (int64, error)
	UpsertGatewayEdgeRuleWatermark(context.Context, string, string, int64) error
}

type edgeRuleRepairInvalidator interface {
	ResetEdgeRules()
	InvalidateResponseCacheAll()
}

// repairDurableEdgeRuleChanges replays durable edge-rule mutations into the
// caches. Every ledger row carries the match_host patterns the trigger read
// from the rule itself, so each change invalidates only the hosts and apps it
// can affect (the notification path usually already did; repeating a scoped
// invalidation is cheap, unlike the wholesale flush this loop used to run on
// every mutation). A backlog at the replay limit, or an unreadable ledger,
// falls back to the conservative wholesale flush.
func repairDurableEdgeRuleChanges(ctx context.Context, store edgeRuleRepairStore, inv edgeRuleRepairInvalidator, lastID *int64, log *slog.Logger) (bool, error) {
	latest, err := store.LatestEdgeRuleChangeID(ctx)
	if err != nil {
		return false, err
	}
	if latest <= *lastID {
		return false, nil
	}
	previous := *lastID
	changes, listErr := store.ListEdgeRuleChangesAfter(ctx, *lastID, edgeRuleRepairReplayLimit)
	if listErr != nil || len(changes) == 0 || len(changes) >= edgeRuleRepairReplayLimit {
		inv.ResetEdgeRules()
		inv.InvalidateResponseCacheAll()
		*lastID = latest
	} else {
		for _, change := range changes {
			invalidateEdgeRuleScope(ctx, inv, change.AppID, change.MatchHosts)
		}
		*lastID = changes[len(changes)-1].ID
	}
	if log != nil {
		log.Info("gatewayd: repaired missed edge-rule invalidation", "from_id", previous, "to_id", *lastID, "scoped", listErr == nil && len(changes) > 0 && len(changes) < edgeRuleRepairReplayLimit)
	}
	return true, nil
}

// watchDurableEdgeRuleChanges complements LISTEN/NOTIFY. A notification is
// still the fast path, while this loop closes the restart/disconnect gap by
// polling the transactionally populated edge-rule ledger from every gateway.
func watchDurableEdgeRuleChanges(ctx context.Context, store edgeRuleRepairStore, inv edgeRuleRepairInvalidator, log *slog.Logger, nodeName string) {
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
		if err := store.UpsertGatewayEdgeRuleWatermark(publishCtx, nodeName, bootID, lastID); err != nil {
			log.Warn("gatewayd: publish edge-rule watermark failed", "node", nodeName, "revision", lastID, "err", err)
		}
	}
	if latest, err := store.LatestEdgeRuleChangeID(ctx); err != nil {
		log.Warn("gatewayd: edge-rule repair baseline failed", "err", err)
	} else {
		lastID = latest
		baselineReady = true
		publishWatermark(ctx)
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
			if !baselineReady {
				latest, baselineErr := store.LatestEdgeRuleChangeID(pollCtx)
				if baselineErr != nil {
					cancel()
					log.Warn("gatewayd: edge-rule repair baseline retry failed", "err", baselineErr)
					continue
				}
				lastID = latest
				baselineReady = true
				publishWatermark(pollCtx)
				cancel()
				continue
			}
			_, err := repairDurableEdgeRuleChanges(pollCtx, store, inv, &lastID, log)
			if err != nil {
				cancel()
				log.Warn("gatewayd: edge-rule repair poll failed", "err", err)
				continue
			}
			publishWatermark(pollCtx)
			cancel()
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
