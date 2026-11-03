package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// Build log retention (ADR-20261102182856). builderd writes every build's
// output to deployment_logs; meterd deletes a build's rows once its last line
// is older than BuildLogRetentionDays, unless the deployment is live (every
// serving, canary or `--no-traffic` staged release has status 'live') or is
// one of its app's api.BuildLogRetainedDeployments newest deployments by
// created_at, whatever their status. The rest of a deployment's record stays.

// BuildLogRetentionDays is how long a build's log outlives its last line. It
// is the longest customer log window (MaxLogEventRetentionDays, Scale's
// 90-day archive cap, which the archive bucket's lifecycle rule also
// enforces), so build output and runtime output share one deletion horizon.
const BuildLogRetentionDays = MaxLogEventRetentionDays

// BuildLogRetentionInterval is the sweep cadence. Each pass sweeps one slice.
const BuildLogRetentionInterval = time.Hour

// BuildLogRetentionSlices splits the deployment id space by its first hex
// digit. A pass sweeps the slice its UTC hour selects, so it examines about a
// sixteenth of the builds that still hold rows, and every build is examined at
// least once every 16 hours however often meterd restarts.
const BuildLogRetentionSlices = 16

// BuildLogRetentionScanBatch bounds the builds one statement examines. Rows
// are deleted RetentionBatchSize per statement, at most MaxRetentionBatches
// statements per pass.
const BuildLogRetentionScanBatch = 500

// retentionBuildLogsBatchSQL examines the next $3 builds with rows in the id
// window ($1, $2] and deletes up to $6 rows of the expired ones. Every step is
// an index probe; nothing scans deployment_logs by time:
//
//   - logged walks the distinct deployment ids of deployment_logs_pkey
//     (deployment_id, seq) with a loose index scan, so a build costs one probe
//     however many lines it wrote, and builds whose rows are gone cost nothing;
//   - a build's last line comes from deployment_logs_seq_idx
//     (deployment_id, seq DESC); seq is one sequence, so the highest seq is the
//     newest line;
//   - the newest deployments come from deployments_app_created_id_idx
//     (app_id, created_at DESC, id DESC).
//
// It returns the builds examined, the last id examined (the next window's
// exclusive lower bound) and the rows deleted.
const retentionBuildLogsBatchSQL = `
WITH RECURSIVE logged(id, n) AS (
    SELECT (SELECT l.deployment_id FROM deployment_logs l
             WHERE l.deployment_id > $1::uuid AND l.deployment_id <= $2::uuid
             ORDER BY l.deployment_id LIMIT 1), 1
  UNION ALL
    SELECT (SELECT l.deployment_id FROM deployment_logs l
             WHERE l.deployment_id > g.id AND l.deployment_id <= $2::uuid
             ORDER BY l.deployment_id LIMIT 1), g.n + 1
      FROM logged g
     WHERE g.id IS NOT NULL AND g.n < $3
),
scanned AS MATERIALIZED (
    SELECT id, n FROM logged WHERE id IS NOT NULL
),
expired AS MATERIALIZED (
    SELECT s.id
      FROM scanned s
      JOIN deployments d ON d.id = s.id
     WHERE d.status <> 'live'
       AND (SELECT l.written_at FROM deployment_logs l
             WHERE l.deployment_id = s.id
             ORDER BY l.seq DESC LIMIT 1) < now() - $4::interval
       AND d.id NOT IN (SELECT newest.id FROM deployments newest
                         WHERE newest.app_id = d.app_id
                         ORDER BY newest.created_at DESC, newest.id DESC
                         LIMIT $5)
),
doomed AS MATERIALIZED (
    SELECT l.deployment_id, l.seq
      FROM deployment_logs l
      JOIN expired e ON e.id = l.deployment_id
     WHERE l.written_at < now() - $4::interval
     LIMIT $6
),
deleted AS (
    DELETE FROM deployment_logs l
     USING doomed x
     WHERE l.deployment_id = x.deployment_id AND l.seq = x.seq
    RETURNING 1
)
SELECT (SELECT count(*) FROM scanned),
       (SELECT id::text FROM scanned ORDER BY n DESC LIMIT 1),
       (SELECT count(*) FROM deleted)`

// buildLogRetentionDB is the SQL surface the sweep needs; cmd/meterd's
// poolAdapter satisfies it.
type buildLogRetentionDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// BuildLogRetentionResult counts one pass: builds examined (a window repeated
// after a full delete batch counts again) and rows deleted.
type BuildLogRetentionResult struct {
	Scanned int64
	Deleted int64
}

const (
	uuidNil     = "00000000-0000-0000-0000-000000000000"
	uuidMaxTail = "fffffff-ffff-ffff-ffff-ffffffffffff"
)

// BuildLogRetentionSliceBounds returns slice's id window as an exclusive lower
// and inclusive upper bound. The 16 windows are contiguous and cover every
// uuid except the nil uuid, which gen_random_uuid never returns.
func BuildLogRetentionSliceBounds(slice int) (after, through string) {
	const hex = "0123456789abcdef"
	slice = ((slice % BuildLogRetentionSlices) + BuildLogRetentionSlices) % BuildLogRetentionSlices
	through = string(hex[slice]) + uuidMaxTail
	if slice == 0 {
		return uuidNil, through
	}
	return string(hex[slice-1]) + uuidMaxTail, through
}

// BuildLogRetentionSlice is the slice the pass at t sweeps: its UTC hour
// modulo BuildLogRetentionSlices.
func BuildLogRetentionSlice(t time.Time) int {
	return int((t.Unix() / 3600) % BuildLogRetentionSlices)
}

// RetentionOnceBuildLogs sweeps one slice. A window whose delete batch came
// back full is examined again from the same bound (its expired builds may
// hold more rows); otherwise the next window starts after the last build
// examined. It returns ErrRetentionBatchCap after MaxRetentionBatches
// statements; the next pass over the slice resumes the work.
func RetentionOnceBuildLogs(ctx context.Context, db buildLogRetentionDB, slice int) (BuildLogRetentionResult, error) {
	after, through := BuildLogRetentionSliceBounds(slice)
	interval := fmt.Sprintf("%d days", BuildLogRetentionDays)
	var out BuildLogRetentionResult
	for i := 0; i < MaxRetentionBatches; i++ {
		var scanned, deleted int64
		var last *string
		if err := db.QueryRow(ctx, retentionBuildLogsBatchSQL, after, through, BuildLogRetentionScanBatch,
			interval, api.BuildLogRetainedDeployments, RetentionBatchSize).Scan(&scanned, &last, &deleted); err != nil {
			return out, fmt.Errorf("build log retention (slice %d, batch %d, deleted so far %d): %w", slice, i, out.Deleted, err)
		}
		out.Scanned += scanned
		out.Deleted += deleted
		if deleted >= RetentionBatchSize {
			continue
		}
		if scanned < BuildLogRetentionScanBatch || last == nil {
			return out, nil
		}
		after = *last
	}
	return out, ErrRetentionBatchCap
}

// BuildLogRetentionLoop sweeps at boot and every interval until ctx is done.
// observe receives each pass's deleted rows and error (cmd/meterd's metrics);
// a failed pass retries on the next tick.
func BuildLogRetentionLoop(ctx context.Context, db buildLogRetentionDB, interval time.Duration, now func() time.Time, log *slog.Logger, observe func(int64, error)) {
	if interval <= 0 {
		interval = BuildLogRetentionInterval
	}
	if now == nil {
		now = time.Now
	}
	run := func() {
		slice := BuildLogRetentionSlice(now())
		res, err := RetentionOnceBuildLogs(ctx, db, slice)
		if observe != nil {
			observe(res.Deleted, err)
		}
		if log == nil {
			return
		}
		switch {
		case err == nil:
			log.Info("build log retention tick ok", "slice", slice, "builds_examined", res.Scanned, "rows_deleted", res.Deleted)
		case errors.Is(err, ErrRetentionBatchCap):
			log.Warn("build log retention hit batch cap; the slice resumes on its next pass", "slice", slice, "rows_deleted", res.Deleted, "err", err)
		default:
			log.Error("build log retention failed", "slice", slice, "rows_deleted", res.Deleted, "err", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
