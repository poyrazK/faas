// adr: 20261111152144

package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// fullScanInventoriesDue is ObjectInventoriesDue as it was before
// ADR-20261111152144: the due rules applied to every bucket. The indexed
// query must return exactly what it returns.
const fullScanInventoriesDue = `WITH q AS (SELECT $1::boolean AND object_inventory_quiet_mode()='on' AS quiet_on)
SELECT b.id::text, (l.listing OR COALESCE(u.observed_keys, 0) > $2::bigint)::boolean AS long_listing
FROM object_buckets b CROSS JOIN q LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id
LEFT JOIN object_bucket_activity a ON a.bucket_id=b.id
CROSS JOIN LATERAL (SELECT EXISTS (SELECT 1 FROM object_storage_inventory_scans s WHERE s.bucket_id=b.id AND s.pages > 0 AND NOT s.complete
  AND s.started_at > COALESCE(u.observed_at, '-infinity'::timestamptz) AND s.started_at > now() - $3::bigint * interval '1 second') AS listing) l
CROSS JOIN LATERAL (SELECT COALESCE(q.quiet_on AND u.observed_at IS NOT NULL AND u.inventory_scope='current'
  AND COALESCE(a.mutated_at, '-infinity'::timestamptz) < u.observed_at - $4::bigint * interval '1 second', false) AS quiet) z
WHERE b.state='ready' AND NOT EXISTS(SELECT 1 FROM object_bucket_versioning WHERE bucket_id=b.id AND state<>'ready') AND NOT EXISTS (SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=b.id AND c.state IN ('waiting','scanning'))
AND (u.attempt_at IS NULL OR u.attempt_at < now() - interval '5 minutes' OR l.listing)
AND (u.lease_until IS NULL OR u.lease_until < now())
AND NOT (b.id = ANY(COALESCE($5::uuid[], '{}')))
AND NOT (z.quiet AND NOT l.listing AND u.observed_at > now() - $6::bigint * interval '1 second')
ORDER BY CASE WHEN z.quiet THEN u.observed_at + ($7::bigint - $8::bigint) * interval '1 second'
  ELSE u.attempt_at END NULLS FIRST, b.id LIMIT $9::int`

// fullScanInventoryBacklog is ObjectInventoryBacklog as it was before
// ADR-20261111152144 §4: the backlog rules applied to every ready bucket.
// The schedule-backed query must return exactly what it returns.
const fullScanInventoryBacklog = `WITH q AS (SELECT $1::boolean AND object_inventory_quiet_mode()='on' AS quiet_on),
r AS (
  SELECT b.created_at, u.observed_at, u.attempt_at, u.lease_until,
    EXISTS(SELECT 1 FROM object_bucket_versioning v WHERE v.bucket_id=b.id AND v.state<>'ready') AS versioning,
    EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id=b.id AND c.state IN ('waiting','scanning')) AS reconciling,
    EXISTS(SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id=b.id AND l.state<>'ready') AS locking,
    COALESCE(i.pages > 0 AND NOT i.complete AND i.started_at > COALESCE(u.observed_at, '-infinity'::timestamptz)
      AND i.started_at > now() - $2::bigint * interval '1 second', false) AS listing,
    COALESCE(i.pages > 0 AND i.started_at > COALESCE(u.observed_at, '-infinity'::timestamptz) AND i.progress_at <= now()
      AND i.progress_at >= now() - $3::bigint * interval '1 second', false) AS progressing,
    COALESCE(q.quiet_on AND u.observed_at IS NOT NULL AND u.inventory_scope='current'
      AND COALESCE(a.mutated_at, '-infinity'::timestamptz) < u.observed_at - $4::bigint * interval '1 second', false) AS quiet,
    CASE WHEN q.quiet_on AND u.inventory_scope='current' AND a.active_since > u.observed_at
      AND a.active_since < u.observed_at + $5::bigint * interval '1 second'
      THEN a.active_since ELSE COALESCE(u.observed_at, b.created_at) END AS anchor
  FROM object_buckets b CROSS JOIN q
  LEFT JOIN object_storage_bucket_usage u ON u.bucket_id=b.id
  LEFT JOIN object_storage_inventory_scans i ON i.bucket_id=b.id
  LEFT JOIN object_bucket_activity a ON a.bucket_id=b.id
  WHERE b.state='ready'
)
SELECT
  (count(*) FILTER (WHERE NOT versioning AND NOT reconciling AND (attempt_at IS NULL OR attempt_at < now() - interval '5 minutes' OR listing)
    AND (lease_until IS NULL OR lease_until < now())
    AND NOT (quiet AND NOT listing AND observed_at > now() - $6::bigint * interval '1 second')))::bigint AS due_buckets,
  COALESCE(EXTRACT(EPOCH FROM now() - min(anchor) FILTER (WHERE NOT quiet AND NOT (observed_at IS NOT NULL AND (
    versioning OR locking OR reconciling
    OR (progressing AND observed_at > now() - $2::bigint * interval '1 second'))))), 0)::float8 AS oldest_age_seconds,
  COALESCE(EXTRACT(EPOCH FROM now() - min(observed_at) FILTER (WHERE quiet)), 0)::float8 AS oldest_quiet_age_seconds
FROM r`

// scheduleDrift counts buckets whose object_inventory_schedule row is
// missing, extra, or differs from the copies its sources give.
const scheduleDrift = `SELECT count(*) FROM object_buckets b
FULL JOIN object_inventory_schedule s ON s.bucket_id = b.id
LEFT JOIN object_storage_bucket_usage u ON u.bucket_id = b.id
LEFT JOIN object_bucket_activity a ON a.bucket_id = b.id
WHERE b.id IS NULL OR (b.state <> 'deleted') <> (s.bucket_id IS NOT NULL)
OR (s.bucket_id IS NOT NULL AND (s.attempt_at, s.observed_at, s.quiet_after, s.mutated_at, s.active_since) IS DISTINCT FROM
 (COALESCE(u.attempt_at, '-infinity'), COALESCE(u.observed_at, '-infinity'),
  CASE WHEN u.observed_at IS NOT NULL AND u.inventory_scope = 'current' THEN u.observed_at - interval '30 seconds' ELSE '-infinity' END,
  COALESCE(a.mutated_at, '-infinity'), COALESCE(a.active_since, '-infinity')))`

// TestObjectInventoryScheduleMatchesFullScanPG pins ADR-20261111152144: over
// randomized buckets (never listed, leased, held, listing, all-versions,
// quiet and active on both sides of every boundary), the indexed due query
// returns exactly the buckets, order and long flags the full scan returns,
// with quiet buckets on and off, for every limit and skip list; the backlog
// gauges equal the full scan's (§4); and every schedule row equals what its
// sources give.
func TestObjectInventoryScheduleMatchesFullScanPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	const buckets = 40
	ids := make([]string, 0, buckets)
	for i := range buckets {
		b := seedRecoveryBucket(t, st)
		if i > 0 { // the first stays provisioning
			if _, err := st.ClaimObjectBucket(ctx, b.AccountID, b.AppID, b.ID, "create", "provisioning"); err != nil {
				t.Fatal(err)
			}
			if err := st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, b.ID)
	}
	assertScheduleCopies(t, ctx, pool)
	// A bucket that becomes deleted leaves the schedule.
	if _, err := pool.Exec(ctx, `UPDATE object_buckets SET state = 'deleted' WHERE id = $1`, ids[buckets-1]); err != nil {
		t.Fatal(err)
	}
	assertScheduleCopies(t, ctx, pool)
	ids = ids[:buckets-1]
	// A bucket being deleted keeps its schedule row, and two buckets hold an
	// Object Lock change that never settles (lock rows are permanent).
	if _, err := pool.Exec(ctx, `UPDATE object_buckets SET state = 'deleting' WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ids[2], ids[5]} {
		if _, err := pool.Exec(ctx, `INSERT INTO object_bucket_object_lock (bucket_id, account_id, app_id, state, revision, retry_at, updated_at)
SELECT id, account_id, app_id, 'waiting', 0, now(), now() FROM object_buckets WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewPCG(20261111, 152144))
	var cover backlogCoverage
	for round := range 16 {
		// Every fourth round rewrites every bucket young but for a few
		// observed long ago and active since well after, whose anchor is
		// then the oldest.
		young := round%4 == 3
		for i, id := range ids[1:] {
			if round == 0 || young || rng.IntN(3) == 0 {
				// Version accounting never reverts to current-object
				// inventory, so a bucket keeps its scope.
				randomizeInventorySources(t, ctx, pool, rng, id, i%7 == 3, young)
			}
		}
		assertScheduleCopies(t, ctx, pool)
		for _, mode := range []string{api.ObjectInventoryQuietOff, api.ObjectInventoryQuietOn} {
			setInventoryQuietMode(t, ctx, st, mode)
			for _, quiet := range []bool{false, true} {
				compareBacklogQueries(t, ctx, pool, fmt.Sprintf("round %d mode %s quiet %v", round, mode, quiet), quiet, &cover)
				for _, limit := range []int32{1, 3, 7, 100} {
					var skip []string
					for _, id := range ids {
						if rng.IntN(8) == 0 {
							skip = append(skip, id)
						}
					}
					compareDueQueries(t, ctx, pool, fmt.Sprintf("round %d mode %s quiet %v limit %d", round, mode, quiet, limit), quiet, limit, skip)
				}
			}
		}
	}
	// The comparisons must have reached every part of the backlog query.
	if cover.due == 0 || cover.oldest == 0 || cover.quiet == 0 || cover.activated == 0 || cover.skipped == 0 {
		t.Fatalf("backlog comparisons missed a case: %+v", cover)
	}
	t.Logf("backlog comparisons: %+v", cover)
}

// backlogCoverage counts backlog comparisons with buckets due, an oldest
// observation, an oldest quiet observation, an oldest anchor taken from when
// a bucket turned active, and an exempt bucket older than the oldest anchor.
type backlogCoverage struct{ due, oldest, quiet, activated, skipped int }

func compareBacklogQueries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, quiet bool, cover *backlogCoverage) {
	t.Helper()
	p := sqlc.ObjectInventoryBacklogParams{
		Quiet: quiet, ScanMaxAgeSeconds: int64(api.ObjectInventoryScanMaxAge / time.Second), ProgressWindowSeconds: int64(api.ObjectInventoryScanProgressWindow / time.Second),
		GranularitySeconds: int64(api.ObjectInventoryMutationStampGranularity / time.Second),
		QuietMaxAgeSeconds: int64(api.ObjectInventoryQuietMaxAge / time.Second), QuietVerifySeconds: int64(api.ObjectInventoryQuietVerifyAfter / time.Second),
	}
	// One transaction, so both read the same now().
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		got, err := sqlc.New().ObjectInventoryBacklog(ctx, tx, p)
		if err != nil {
			return err
		}
		var want sqlc.ObjectInventoryBacklogRow
		if err = tx.QueryRow(ctx, fullScanInventoryBacklog, p.Quiet, p.ScanMaxAgeSeconds, p.ProgressWindowSeconds, p.GranularitySeconds, p.QuietMaxAgeSeconds, p.QuietVerifySeconds).
			Scan(&want.DueBuckets, &want.OldestAgeSeconds, &want.OldestQuietAgeSeconds); err != nil {
			return err
		}
		if got != want {
			t.Fatalf("%s:\n schedule  %+v\n full scan %+v", name, got, want)
		}
		var activated, skipped bool
		if err = tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM object_bucket_activity WHERE active_since > '-infinity' AND EXTRACT(EPOCH FROM now() - active_since)::float8 = $1),
 EXISTS(SELECT 1 FROM object_inventory_schedule s JOIN object_buckets b ON b.id = s.bucket_id AND b.state = 'ready'
  WHERE s.mutated_at >= s.quiet_after AND s.observed_at > '-infinity' AND s.observed_at < now() - $1 * interval '1 second'
  AND (EXISTS(SELECT 1 FROM object_bucket_versioning v WHERE v.bucket_id = s.bucket_id AND v.state <> 'ready')
   OR EXISTS(SELECT 1 FROM object_bucket_object_lock l WHERE l.bucket_id = s.bucket_id AND l.state <> 'ready')
   OR EXISTS(SELECT 1 FROM object_storage_capacity_reconciliations c WHERE c.bucket_id = s.bucket_id AND c.state IN ('waiting','scanning'))))`,
			got.OldestAgeSeconds).Scan(&activated, &skipped); err != nil {
			return err
		}
		for _, c := range []struct {
			hit bool
			n   *int
		}{{got.DueBuckets > 0, &cover.due}, {got.OldestAgeSeconds > 0, &cover.oldest}, {got.OldestQuietAgeSeconds > 0, &cover.quiet}, {activated, &cover.activated}, {skipped, &cover.skipped}} {
			if c.hit {
				*c.n++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(name, err)
	}
}

func setInventoryQuietMode(t *testing.T, ctx context.Context, st accountingStore, mode string) {
	t.Helper()
	value := json.RawMessage(`"` + mode + `"`)
	row, err := st.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: api.ObjectInventoryQuietConfigKey, Scope: state.RuntimeConfigScopeGlobal, DesiredValue: value, ApplyMode: state.RuntimeConfigApplyHot})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, value, ""); err != nil {
		t.Fatal(err)
	}
}

func assertScheduleCopies(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var drift int
	if err := pool.QueryRow(ctx, scheduleDrift).Scan(&drift); err != nil {
		t.Fatal(err)
	}
	if drift != 0 {
		t.Fatalf("%d schedule rows differ from their sources", drift)
	}
}

// randomizeInventorySources rewrites one bucket's usage (all-versions
// scope when allVersions), activity, listing, versioning and
// capacity-reconciliation rows. Ages cluster around the 5-minute retry, the
// 30 s stamp granularity, the 5-minute progress window, the hour a listing
// may continue and the daily verification, a few seconds from each edge.
// When young, ages stay under 15 minutes, except that one bucket in eight is
// observed about 20 hours ago and active since 10 hours ago.
func randomizeInventorySources(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rng *rand.Rand, id string, allVersions, young bool) {
	t.Helper()
	age := func() float64 {
		if young {
			return float64(rng.IntN(900))
		}
		edges := []float64{0, 5 * 60, 30, 3600, 23 * 3600, 24 * 3600}
		if rng.IntN(2) == 0 {
			return edges[rng.IntN(len(edges))] + float64(rng.IntN(41)-20)
		}
		return float64(rng.IntN(30 * 3600))
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`DELETE FROM object_storage_capacity_reconciliations WHERE bucket_id = $1`, id)
	exec(`DELETE FROM object_bucket_versioning WHERE bucket_id = $1`, id)
	if rng.IntN(8) == 0 {
		exec(`INSERT INTO object_bucket_versioning (bucket_id, desired_status, state) VALUES ($1, 'Enabled', 'waiting')`, id)
	}
	anchored := young && rng.IntN(8) == 0
	// Replace the row: an update of an all-versions row needs a verified
	// version inventory (fence_object_version_reclamation).
	exec(`DELETE FROM object_storage_bucket_usage WHERE bucket_id = $1`, id)
	if anchored || rng.IntN(5) != 0 {
		var attempt, observed *float64
		if rng.IntN(8) != 0 {
			a := max(age(), 0)
			attempt = &a
		}
		if rng.IntN(6) != 0 {
			o := max(age(), 0)
			if attempt != nil && rng.IntN(2) == 0 {
				o = *attempt + float64(rng.IntN(3))
			}
			observed = &o
		}
		if anchored {
			o := float64(72000 + rng.IntN(7200) - 3600)
			observed = &o
		}
		scope := "current"
		if allVersions {
			scope = "all_versions"
		}
		lease := []string{"none", "none", "none", "held", "expired"}[rng.IntN(5)]
		keys := int64(rng.IntN(3))
		if rng.IntN(6) == 0 {
			keys = api.ObjectStorageInventoryLongListingKeys + 1
		}
		exec(`INSERT INTO object_storage_bucket_usage (bucket_id, attempt_at, observed_at, inventory_scope, observed_keys, lease_until, token)
VALUES ($1, now() - $2 * interval '1 second', now() - $3 * interval '1 second', $4, $5,
 CASE $6 WHEN 'held' THEN now() + interval '1 minute' WHEN 'expired' THEN now() - interval '1 minute' END,
 CASE $6 WHEN 'none' THEN '' ELSE 'token' END)`,
			id, attempt, observed, scope, keys, lease)
	}
	if !anchored && rng.IntN(4) == 0 {
		exec(`DELETE FROM object_bucket_activity WHERE bucket_id = $1`, id)
	} else {
		// Mutated relative to the observation, active since -infinity, the
		// mutation, or relative to the observation (at most the mutation).
		mutated := float64([]int{-3600, -31, -30, -29, 0, 29, 31, 600, 90000}[rng.IntN(9)])
		since := []int{0, 0, 1, 2, 2, 2}[rng.IntN(6)]
		after := float64([]int{-5, 1, 600, 36000, 86399, 86401}[rng.IntN(6)])
		if anchored {
			mutated, since, after = 86400, 2, float64(36000+rng.IntN(3600))
		}
		exec(`INSERT INTO object_bucket_activity (bucket_id, mutated_at, active_since)
VALUES ($1, LEAST(COALESCE((SELECT observed_at FROM object_storage_bucket_usage WHERE bucket_id = $1), now()) + $2 * interval '1 second', now() + interval '1 hour'), '-infinity')
ON CONFLICT (bucket_id) DO UPDATE SET mutated_at = EXCLUDED.mutated_at, active_since = EXCLUDED.active_since`, id, mutated)
		exec(`UPDATE object_bucket_activity SET active_since = CASE $2::int WHEN 0 THEN '-infinity' WHEN 1 THEN mutated_at
 ELSE LEAST(mutated_at, COALESCE((SELECT observed_at FROM object_storage_bucket_usage WHERE bucket_id = $1), now()) + $3 * interval '1 second') END
WHERE bucket_id = $1`, id, since, after)
	}
	if rng.IntN(2) == 0 {
		exec(`DELETE FROM object_storage_inventory_scans WHERE bucket_id = $1`, id)
	} else {
		pages := []int{0, 1, 3}[rng.IntN(3)]
		complete := pages > 0 && rng.IntN(3) == 0
		after := ""
		if pages > 0 {
			after = "k"
		}
		start := age()
		if rng.IntN(2) == 0 {
			var observed pgtype.Timestamptz
			_ = pool.QueryRow(ctx, `SELECT observed_at FROM object_storage_bucket_usage WHERE bucket_id = $1`, id).Scan(&observed)
			if observed.Valid {
				start = time.Since(observed.Time).Seconds() + float64([]int{-60, -1, 1, 60}[rng.IntN(4)])
			}
		}
		// Progress on both sides of the 5-minute window, or at the start.
		progress := float64([]int{0, 30, 298, 302, 1 << 20}[rng.IntN(5)])
		exec(`INSERT INTO object_storage_inventory_scans (bucket_id, started_at, progress_at, after_key, pages, complete)
VALUES ($1, now() - $2 * interval '1 second', GREATEST(now() - $2 * interval '1 second', now() - $6 * interval '1 second'), $3, $4, $5)
ON CONFLICT (bucket_id) DO UPDATE SET started_at = EXCLUDED.started_at, progress_at = EXCLUDED.progress_at,
 after_key = EXCLUDED.after_key, pages = EXCLUDED.pages, complete = EXCLUDED.complete`, id, start, after, pages, complete, progress)
	}
	if rng.IntN(10) == 0 {
		exec(`INSERT INTO object_storage_capacity_reconciliations (id, bucket_id, state, deadline_at) VALUES ($1, $2, 'waiting', now() + interval '1 hour')`, uuid.NewString(), id)
	}
}

func compareDueQueries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, quiet bool, limit int32, skip []string) {
	t.Helper()
	ids := make([]pgtype.UUID, 0, len(skip))
	for _, id := range skip {
		u := uuid.MustParse(id)
		ids = append(ids, pgtype.UUID{Bytes: u, Valid: true})
	}
	p := sqlc.ObjectInventoriesDueParams{
		LongKeys: api.ObjectStorageInventoryLongListingKeys, ScanMaxAgeSeconds: int64(api.ObjectInventoryScanMaxAge / time.Second), Skip: ids, RowLimit: limit,
		Quiet: quiet, GranularitySeconds: int64(api.ObjectInventoryMutationStampGranularity / time.Second), QuietVerifySeconds: int64(api.ObjectInventoryQuietVerifyAfter / time.Second),
		QuietMaxAgeSeconds: int64(api.ObjectInventoryQuietMaxAge / time.Second), WindowSeconds: api.ObjectStorageInventoryMaxAgeSeconds,
	}
	// One transaction, so both read the same now().
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		rows, err := sqlc.New().ObjectInventoriesDue(ctx, tx, p)
		if err != nil {
			return err
		}
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, fmt.Sprintf("%s long=%v", uuid.UUID(r.ObjectBucket.ID.Bytes), r.LongListing))
		}
		old, err := tx.Query(ctx, fullScanInventoriesDue, p.Quiet, p.LongKeys, p.ScanMaxAgeSeconds, p.GranularitySeconds, ids, p.QuietVerifySeconds, p.QuietMaxAgeSeconds, p.WindowSeconds, p.RowLimit)
		if err != nil {
			return err
		}
		want, err := pgx.CollectRows(old, func(r pgx.CollectableRow) (string, error) {
			var id string
			var long bool
			err := r.Scan(&id, &long)
			return fmt.Sprintf("%s long=%v", id, long), err
		})
		if err != nil {
			return err
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s:\n indexed   %v\n full scan %v", name, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(name, err)
	}
}
