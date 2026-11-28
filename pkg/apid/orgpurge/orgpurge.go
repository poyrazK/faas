// Package orgpurge releases deleted shared organizations' slugs once their
// deletion window has passed (ADR-061 amendment 1). Before it, a deleted
// organization kept its slug, and its place in members' `org list`, forever
// (prod hunt r73: audit-1001-org, deleted 2026-09-30).
package orgpurge

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Interval is how often Run sweeps.
const Interval = time.Hour

// Auditor records one purge.
type Auditor interface {
	Emit(ctx context.Context, action string, actorAccountID *string, payload map[string]any)
}

// Sweep purges one batch of organizations deleted more than
// api.DeletedOrgPurgeAfter before now and returns how many it purged. Several
// apids may sweep at once: rows are claimed with skip locked and a purged
// slug is never picked again.
func Sweep(ctx context.Context, store state.DeletedOrgPurgeStore, audit Auditor, log *slog.Logger, now time.Time) int {
	purged, err := store.PurgeDeletedOrgs(ctx, now.Add(-api.DeletedOrgPurgeAfter), api.DeletedOrgPurgeBatch)
	if err != nil {
		log.Warn("org_purge.sweep", "err", err)
		return 0
	}
	for _, p := range purged {
		if audit != nil {
			audit.Emit(ctx, "org.purged", nil, map[string]any{"org_id": p.ID, "slug": p.Slug})
		}
		log.Info("org_purge.purged", "org_id", p.ID, "released_slug", p.Slug)
	}
	return len(purged)
}

// Run sweeps every Interval until ctx ends.
func Run(ctx context.Context, store state.DeletedOrgPurgeStore, audit Auditor, log *slog.Logger) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		Sweep(ctx, store, audit, log, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
