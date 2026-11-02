package meter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261102182856
func TestBuildLogRetentionHorizonIsTheLongestLogWindow(t *testing.T) {
	if BuildLogRetentionDays != api.PlanScale.LogArchiveRetentionDaysMax() {
		t.Fatalf("build logs kept %d days, the longest log archive window is %d", BuildLogRetentionDays, api.PlanScale.LogArchiveRetentionDaysMax())
	}
	if api.BuildLogRetainedDeployments < api.SnapshotRollbackRetentionDeployments {
		t.Fatalf("build logs keep %d deployments, fewer than the %d-deployment rollback window", api.BuildLogRetainedDeployments, api.SnapshotRollbackRetentionDeployments)
	}
}

func TestBuildLogRetentionSlicesCoverTheIDSpace(t *testing.T) {
	prevThrough := ""
	for slice := 0; slice < BuildLogRetentionSlices; slice++ {
		after, through := BuildLogRetentionSliceBounds(slice)
		if slice == 0 && after != uuidNil {
			t.Fatalf("slice 0 starts after %s, want the nil uuid", after)
		}
		if slice > 0 && after != prevThrough {
			t.Fatalf("slice %d starts after %s, slice %d ends at %s", slice, after, slice-1, prevThrough)
		}
		if len(through) != 36 || !(after < through) {
			t.Fatalf("slice %d window (%s, %s] is malformed", slice, after, through)
		}
		prevThrough = through
	}
	if prevThrough != "ffffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Fatalf("last slice ends at %s", prevThrough)
	}
	if after, through := BuildLogRetentionSliceBounds(BuildLogRetentionSlices + 3); after != "2fffffff-ffff-ffff-ffff-ffffffffffff" || through != "3fffffff-ffff-ffff-ffff-ffffffffffff" {
		t.Fatalf("slice wraps to (%s, %s]", after, through)
	}
}

func TestBuildLogRetentionSliceFollowsTheHour(t *testing.T) {
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	seen := map[int]bool{}
	for h := 0; h < BuildLogRetentionSlices; h++ {
		at := start.Add(time.Duration(h)*time.Hour + 59*time.Minute)
		if got, again := BuildLogRetentionSlice(at), BuildLogRetentionSlice(at.Add(-59*time.Minute)); got != again {
			t.Fatalf("hour %d maps to slices %d and %d", h, got, again)
		}
		seen[BuildLogRetentionSlice(at)] = true
	}
	if len(seen) != BuildLogRetentionSlices {
		t.Fatalf("16 consecutive hours swept %d slices", len(seen))
	}
}

// The batch statement must stay on indexed paths: a loose index scan of
// deployment_logs_pkey, the newest line through deployment_logs_seq_idx, the
// newest deployments through deployments_app_created_id_idx, and a bounded
// delete by primary key.
func TestBuildLogRetentionSQLKeepsLiveAndNewestDeployments(t *testing.T) {
	for _, want := range []string{
		"l.deployment_id > g.id AND l.deployment_id <= $2::uuid",
		"ORDER BY l.deployment_id LIMIT 1",
		"g.n < $3",
		"d.status <> 'live'",
		"ORDER BY l.seq DESC LIMIT 1) < now() - $4::interval",
		"ORDER BY newest.created_at DESC, newest.id DESC",
		"LIMIT $5",
		"LIMIT $6",
		"l.deployment_id = x.deployment_id AND l.seq = x.seq",
	} {
		if !strings.Contains(retentionBuildLogsBatchSQL, want) {
			t.Fatalf("build log retention SQL lost %q", want)
		}
	}
}

type scriptedBuildLogBatch struct {
	scanned, deleted int64
	last             *string
	err              error
}

type scriptedBuildLogDB struct {
	batches []scriptedBuildLogBatch
	afters  []string
	args    [][]any
}

func (d *scriptedBuildLogDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	d.args = append(d.args, args)
	d.afters = append(d.afters, args[0].(string))
	if len(d.batches) == 0 {
		return scriptedBuildLogRow{batch: scriptedBuildLogBatch{}}
	}
	next := d.batches[0]
	d.batches = d.batches[1:]
	return scriptedBuildLogRow{batch: next}
}

type scriptedBuildLogRow struct{ batch scriptedBuildLogBatch }

func (r scriptedBuildLogRow) Scan(dest ...any) error {
	if r.batch.err != nil {
		return r.batch.err
	}
	*dest[0].(*int64) = r.batch.scanned
	*dest[1].(**string) = r.batch.last
	*dest[2].(*int64) = r.batch.deleted
	return nil
}

func buildLogID(s string) *string { return &s }

func TestRetentionOnceBuildLogsAdvancesRepeatsAndStops(t *testing.T) {
	first, second := "30000000-0000-4000-8000-000000000001", "3fff0000-0000-4000-8000-000000000002"
	db := &scriptedBuildLogDB{batches: []scriptedBuildLogBatch{
		{scanned: BuildLogRetentionScanBatch, last: buildLogID(first), deleted: 7},
		// A full delete batch: the same window is examined again.
		{scanned: BuildLogRetentionScanBatch, last: buildLogID(second), deleted: RetentionBatchSize},
		{scanned: BuildLogRetentionScanBatch, last: buildLogID(second), deleted: 3},
		// A short scan reached the end of the slice.
		{scanned: 12, last: buildLogID("3fffffff-0000-4000-8000-000000000003"), deleted: 0},
	}}
	res, err := RetentionOnceBuildLogs(context.Background(), db, 3)
	if err != nil {
		t.Fatal(err)
	}
	after, through := BuildLogRetentionSliceBounds(3)
	want := []string{after, first, first, second}
	if strings.Join(db.afters, ",") != strings.Join(want, ",") {
		t.Fatalf("windows started after %v, want %v", db.afters, want)
	}
	if res.Deleted != 7+RetentionBatchSize+3 || res.Scanned != 3*BuildLogRetentionScanBatch+12 {
		t.Fatalf("result = %+v", res)
	}
	args := db.args[0]
	if args[1] != through || args[2] != BuildLogRetentionScanBatch || args[3] != "90 days" ||
		args[4] != api.BuildLogRetainedDeployments || args[5] != RetentionBatchSize {
		t.Fatalf("batch arguments = %v", args)
	}
}

func TestRetentionOnceBuildLogsStopsOnAnEmptySliceAndReportsErrors(t *testing.T) {
	db := &scriptedBuildLogDB{batches: []scriptedBuildLogBatch{{scanned: 0}}}
	if res, err := RetentionOnceBuildLogs(context.Background(), db, 0); err != nil || res.Scanned != 0 || len(db.afters) != 1 {
		t.Fatalf("empty slice: %+v %v after %d statements", res, err, len(db.afters))
	}
	boom := errors.New("database unavailable")
	db = &scriptedBuildLogDB{batches: []scriptedBuildLogBatch{{scanned: BuildLogRetentionScanBatch, last: buildLogID("10000000-0000-4000-8000-000000000001"), deleted: 2}, {err: boom}}}
	res, err := RetentionOnceBuildLogs(context.Background(), db, 1)
	if !errors.Is(err, boom) || res.Deleted != 2 {
		t.Fatalf("failed batch: %+v %v", res, err)
	}
}

func TestRetentionOnceBuildLogsHitsTheBatchCap(t *testing.T) {
	batches := make([]scriptedBuildLogBatch, MaxRetentionBatches)
	for i := range batches {
		batches[i] = scriptedBuildLogBatch{scanned: BuildLogRetentionScanBatch, last: buildLogID("50000000-0000-4000-8000-000000000001"), deleted: RetentionBatchSize}
	}
	db := &scriptedBuildLogDB{batches: batches}
	res, err := RetentionOnceBuildLogs(context.Background(), db, 5)
	if !errors.Is(err, ErrRetentionBatchCap) || res.Deleted != int64(MaxRetentionBatches)*RetentionBatchSize {
		t.Fatalf("capped pass: %+v %v", res, err)
	}
}
