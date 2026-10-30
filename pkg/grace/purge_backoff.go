package grace

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// reasonRetainedResources is a retained-resource refusal that names no reason
// (an active operation task, or a store that does not type its refusals).
const reasonRetainedResources = "retained_resources"

// purgeBlockReasons is the closed label set of apid_app_grace_blocked_apps.
var purgeBlockReasons = []string{
	state.AppPurgeBlockedForeignKey,
	state.AppPurgeBlockedManagedPostgresBinding,
	state.AppPurgeBlockedMultipartWrite,
	state.AppPurgeBlockedObjectBucket,
	reasonRetainedResources,
}

// maxForeignKeyRetryDelay caps the back-off after a purge fails on a foreign
// key. Such a purge used to retry every tick, forever: on 2026-10-24 the
// control plane logged 155 failing purge DELETEs in 30 minutes, and each one
// locked the tables its steps and cascades touch, in the opposite order to a
// migration adding a foreign key to apps. That order deadlocked the rc.293
// activation. A restart (how a purge fix ships) clears the back-off.
const maxForeignKeyRetryDelay = 6 * time.Hour

// purgeBlock is an expired tombstone whose last purge was refused.
type purgeBlock struct {
	reason   string
	attempts int
	retryAt  time.Time // zero: retry on the next tick
}

// foreignKeyRetryDelay doubles from twice the sweep interval up to
// maxForeignKeyRetryDelay.
func foreignKeyRetryDelay(interval time.Duration, attempts int) time.Duration {
	delay := interval
	for i := 0; i < attempts && delay < maxForeignKeyRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, maxForeignKeyRetryDelay)
}

// purgeRefused records why DeleteAppPermanently left app in place. A foreign
// key backs off; a retained resource is a cheap wait that takes no row lock,
// so it is retried every tick and only recorded. Any other error is logged and
// retried on the next tick, as before.
func (g *Grace) purgeRefused(ctx context.Context, app state.App, err error, now time.Time) {
	if errors.Is(err, state.ErrNotFound) {
		delete(g.blocked, app.ID)
		return
	}
	var typed *state.AppPurgeBlockedError
	reason := reasonRetainedResources
	switch {
	case errors.As(err, &typed):
		reason = typed.Reason
	case !errors.Is(err, state.ErrConflict):
		g.metrics.failure("delete_database")
		g.p.Log.Warn("grace: app hard delete failed", "app", app.ID, "err", err)
		return
	}
	block := g.blocked[app.ID]
	block.reason, block.attempts, block.retryAt = reason, block.attempts+1, time.Time{}
	if reason == state.AppPurgeBlockedForeignKey {
		block.retryAt = now.Add(foreignKeyRetryDelay(g.p.Interval, block.attempts))
		g.blocked[app.ID] = block
		g.metrics.failure("foreign_key")
		g.p.Log.Warn("grace: app purge blocked by a foreign key; backing off",
			"app", app.ID, "slug", app.Slug, "constraint", typed.Constraint, "table", typed.Table,
			"attempts", block.attempts, "retry_at", block.retryAt)
		return
	}
	g.blocked[app.ID] = block
	// A live bucket, managed PostgreSQL binding or unsettled multipart write
	// still holds the app. That is a wait, not a failure; logging it as one
	// every minute buried real purge errors (Hunt 11 H11-21). A live binding
	// should not exist on a deleted app, so its first sighting is a warning.
	g.metrics.failure("retained_resources")
	level := slog.LevelDebug
	if reason == state.AppPurgeBlockedManagedPostgresBinding && block.attempts == 1 {
		level = slog.LevelWarn
	}
	g.p.Log.Log(ctx, level, "grace: app hard delete waits for retained resources",
		"app", app.ID, "slug", app.Slug, "reason", reason, "attempts", block.attempts)
}

// backingOff reports whether app's purge is waiting out a back-off.
func (g *Grace) backingOff(appID string, now time.Time) bool {
	block, ok := g.blocked[appID]
	return ok && now.Before(block.retryAt)
}

// recordBlocked forgets tombstones that are no longer listed (purged or
// restored elsewhere) and publishes the blocked count by reason.
func (g *Grace) recordBlocked(listed map[string]struct{}) {
	counts := make(map[string]int, len(purgeBlockReasons))
	for id, block := range g.blocked {
		if _, ok := listed[id]; !ok {
			delete(g.blocked, id)
			continue
		}
		counts[block.reason]++
	}
	for _, reason := range purgeBlockReasons {
		g.metrics.blockedApps.WithLabelValues(reason).Set(float64(counts[reason]))
	}
}
