package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func reconcileEnvironmentCachePurges(ctx context.Context, store state.EnvironmentCachePurgeObserver, cache *gateway.ResponseCache, node, session string, cursor *int64) error {
	// Capture before reading the page. Commit-ordered IDs ensure a caught baseline
	// contains every earlier command, including after restart or lost notifications.
	latest, err := store.LatestEnvironmentCachePurgeID(ctx)
	if err != nil {
		return err
	}
	rows, err := store.ListEnvironmentCachePurgesAfter(ctx, *cursor)
	if err != nil {
		return err
	}
	next := *cursor
	for _, p := range rows {
		if err = ctx.Err(); err != nil {
			return err
		}
		if p.ID <= next {
			return state.ErrConflict
		}
		if err = cache.ApplyEnvironmentPurge(p.AppID, p.EnvironmentID, p.Path, p.ID); err != nil {
			return err
		}
		next = p.ID
	}
	if len(rows) == 0 && latest > next {
		next = latest
	} // Only deleted-owner gaps remain.
	*cursor = next
	if next < latest {
		return nil
	} // Startup bypass continues until the baseline is loaded.
	cache.MarkEnvironmentPurgesReady()
	return store.RecordEnvironmentCachePurgeWatermark(ctx, node, session, next)
}
