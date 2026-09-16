// Package meter — storage rollup tick (ADR-049 §B.3).
//
// pkg/meter/storage.go populates snapshot_storage_daily
// (migration 00070) on a meterd cron tick. The table is
// search_path-relative; production search_path=public puts it
// in the public schema, pgtest-isolated tests put it in a
// faas_test_<hex> schema. Every
// FAAS_STORAGE_ROLLUP_INTERVAL (default 1 h) the loop walks
// every (account, app) and sums the latest non-stale
// snapshots.mem_bytes + snapshots.disk_bytes + retained app-layer
// artifact bytes. The result is upserted into snapshot_storage_daily
// for the current day (UTC midnight).
//
// Distinct from pkg/meter/rollup.go (usage_daily from usage_minutes):
// usage_daily is additive-merge on cumulative minute rows; storage
// is a point-in-time overwrite of the day's snapshot. The two
// contracts diverge on the SQL ON CONFLICT clause; both live in
// the same package because they share the meterd cron cadence and
// the execer interface.
package meter

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DefaultStorageRollupInterval is the cadence the storage rollup
// tick runs at when the caller passes 0. 1 h matches the audit's
// recommendation (storage changes slowly; daily granularity is
// fine).
const DefaultStorageRollupInterval = time.Hour

// Store is the read+write surface the storage rollup needs. The
// full state.Store satisfies it; we narrow the interface so
// tests can use a minimal fake.
type Store interface {
	// ListAllApps returns every app across every account. The
	// rollup walks this list and sums snapshots per (account, app).
	ListAllApps(ctx context.Context) ([]AppRow, error)
	// LatestSnapshotBytes returns mem_bytes + disk_bytes for the
	// app's latest non-stale snapshot. Returns 0 for both when
	// the app has no snapshot yet (a cold start, not an error).
	LatestSnapshotBytes(ctx context.Context, appID string) (memBytes, diskBytes int64, err error)
	// AppendSnapshotStorage writes the rollup row. See
	// pkg/state.Store.AppendSnapshotStorage.
	AppendSnapshotStorage(ctx context.Context, accountID, appID string, day time.Time, snapshotBytes, layerBytes int64) error
}

// AppRow is the minimal (account_id, app_id) projection the
// rollup needs. Matches state.App's AccountID + ID columns.
type AppRow struct {
	AccountID string
	AppID     string
}

// StorageRollupOnce walks every (account, app), sums the latest
// snapshot bytes, and upserts the row for day=today (UTC). Idempotent
// on PK (account_id, app_id, day). Returns the number of rows
// touched (useful for metrics + test assertions).
//
// FAIL-SOFT per app (PR #428 review warning #3). A transient
// lookup error for one app (e.g. LatestSnapshotBytes failing
// mid-loop because pgxpool is saturated, or a flaky layerFn)
// must NOT abort the whole rollup — the next tick covers the
// gap. The first error is logged via log at Warn and returned
// alongside the rows-written-so-far so the loop can decide
// whether to count the tick as a partial success. log may be
// nil (tests pass nil and assert no panic).
func StorageRollupOnce(ctx context.Context, store Store, now time.Time, layerFn func(ctx context.Context, appID string) (int64, error), log *slog.Logger) (int, error) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	apps, err := store.ListAllApps(ctx)
	if err != nil {
		return 0, fmt.Errorf("list apps: %w", err)
	}
	written := 0
	var firstErr error
	for _, a := range apps {
		memBytes, diskBytes, err := store.LatestSnapshotBytes(ctx, a.AppID)
		if err != nil {
			// Fail-soft: log + skip this app, keep walking.
			if log != nil {
				log.Warn("storage rollup: snapshot bytes lookup failed; skipping app",
					"app_id", a.AppID, "err", err)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("snapshot bytes app=%s: %w", a.AppID, err)
			}
			continue
		}
		var layerBytes int64
		if layerFn != nil {
			layerBytes, err = layerFn(ctx, a.AppID)
			if err != nil {
				if log != nil {
					log.Warn("storage rollup: layer bytes lookup failed; skipping app",
						"app_id", a.AppID, "err", err)
				}
				if firstErr == nil {
					firstErr = fmt.Errorf("layer bytes app=%s: %w", a.AppID, err)
				}
				continue
			}
		}
		snapshotBytes := memBytes + diskBytes
		if err := store.AppendSnapshotStorage(ctx, a.AccountID, a.AppID, day, snapshotBytes, layerBytes); err != nil {
			if log != nil {
				log.Warn("storage rollup: upsert failed; skipping app",
					"app_id", a.AppID, "err", err)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("upsert rollup app=%s: %w", a.AppID, err)
			}
			continue
		}
		written++
	}
	return written, firstErr
}

// StorageRollupLoop is the free-function goroutine that calls
// StorageRollupOnce every Interval. Returns on ctx.Done().
// Matches the cadence contract for the other meterd ticks
// (pkg/meter/rollup.go::RollupLoop).
func StorageRollupLoop(ctx context.Context, store Store, layerFn func(ctx context.Context, appID string) (int64, error), interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultStorageRollupInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := StorageRollupOnce(ctx, store, time.Now().UTC(), layerFn, log)
			if err != nil {
				if log != nil {
					log.Warn("storage rollup tick had per-app errors; partial success",
						"err", err, "written", n)
				}
				continue
			}
			if log != nil {
				log.Debug("storage rollup tick ok", "rows", n)
			}
		}
	}
}
