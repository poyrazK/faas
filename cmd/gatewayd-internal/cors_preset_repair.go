package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	corsPresetRepairPollInterval  = 2 * time.Second
	corsPresetRepairRetention     = 30 * 24 * time.Hour
	corsPresetRepairPruneInterval = 24 * time.Hour
	corsPresetRepairBatch         = 256
)

type corsPresetRepairStore interface {
	LatestCorsPresetChangeID(context.Context) (int64, error)
	ListCorsPresetChangesAfter(context.Context, int64, int) ([]state.CorsPresetChange, error)
	PruneCorsPresetChangeLog(context.Context, time.Time) (int64, error)
	UpsertGatewayCorsPresetWatermark(context.Context, string, string, int64) error
}

type corsPresetRepairInvalidator interface {
	ResetCorsPresets(accountID string)
}

var _ corsPresetRepairStore = (*state.PgStore)(nil)

// repairDurableCorsPresetChanges applies one bounded page. Account IDs are
// deduplicated because each reset drops the same per-host compiled-rule cache;
// the account still scopes which preset changed and what status can attest.
func repairDurableCorsPresetChanges(
	ctx context.Context,
	store corsPresetRepairStore,
	inv corsPresetRepairInvalidator,
	lastID *int64,
	log *slog.Logger,
) (int, error) {
	changes, err := store.ListCorsPresetChangesAfter(ctx, *lastID, corsPresetRepairBatch)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}
	accounts := make(map[string]struct{}, len(changes))
	nextID := *lastID
	for _, change := range changes {
		if change.ID <= nextID {
			continue
		}
		if strings.TrimSpace(change.AccountID) == "" {
			return 0, fmt.Errorf("CORS preset change %d has empty account id", change.ID)
		}
		nextID = change.ID
		accounts[change.AccountID] = struct{}{}
	}
	if nextID == *lastID {
		return 0, nil
	}
	accountIDs := make([]string, 0, len(accounts))
	for accountID := range accounts {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Strings(accountIDs)
	for _, accountID := range accountIDs {
		inv.ResetCorsPresets(accountID)
	}
	previous := *lastID
	*lastID = nextID
	if log != nil {
		log.Info("gatewayd: replayed CORS preset changes",
			"from_id", previous,
			"to_id", *lastID,
			"rows", len(changes),
			"accounts", len(accountIDs))
	}
	return len(changes), nil
}

// watchDurableCorsPresetChanges complements the CORS pg_notify fast path. Each
// gateway owns a cursor, so a missed notification cannot leave compiled CORS
// actions stale indefinitely on a still-serving replica.
func watchDurableCorsPresetChanges(ctx context.Context, store corsPresetRepairStore, inv corsPresetRepairInvalidator, log *slog.Logger, nodeName string) {
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
		if err := store.UpsertGatewayCorsPresetWatermark(publishCtx, nodeName, bootID, lastID); err != nil {
			log.Warn("gatewayd: publish CORS preset watermark failed", "node", nodeName, "revision", lastID, "err", err)
		}
	}
	if latest, err := store.LatestCorsPresetChangeID(ctx); err != nil {
		log.Warn("gatewayd: CORS preset repair baseline failed", "err", err)
	} else {
		// A fresh process has no compiled-rule cache, so its initial position
		// may safely baseline to the current ledger head.
		lastID = latest
		baselineReady = true
		publishWatermark(ctx)
	}

	poll := time.NewTicker(corsPresetRepairPollInterval)
	defer poll.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if !baselineReady {
				latest, baselineErr := store.LatestCorsPresetChangeID(pollCtx)
				if baselineErr != nil {
					cancel()
					log.Warn("gatewayd: CORS preset repair baseline retry failed", "err", baselineErr)
					continue
				}
				lastID = latest
				baselineReady = true
				publishWatermark(pollCtx)
				cancel()
				continue
			}
			_, err := repairDurableCorsPresetChanges(pollCtx, store, inv, &lastID, log)
			if err != nil {
				cancel()
				log.Warn("gatewayd: CORS preset repair poll failed", "err", err)
				continue
			}
			publishWatermark(pollCtx)
			cancel()
			now := time.Now().UTC()
			if lastPrune.IsZero() || now.Sub(lastPrune) >= corsPresetRepairPruneInterval {
				pruneCtx, pruneCancel := context.WithTimeout(ctx, 5*time.Second)
				if _, pruneErr := store.PruneCorsPresetChangeLog(pruneCtx, now.Add(-corsPresetRepairRetention)); pruneErr != nil {
					log.Warn("gatewayd: CORS preset repair ledger prune failed", "err", pruneErr)
				}
				pruneCancel()
				lastPrune = now
			}
		}
	}
}
