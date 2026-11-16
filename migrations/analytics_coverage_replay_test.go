//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAnalyticsCoverageMigrationReplayPreservesEvidence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var account, app string
	if err := pool.QueryRow(ctx, `INSERT INTO accounts(email,plan)
 VALUES('analytics-replay@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb)
 VALUES($1,'analytics-replay',128) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO compute_nodes(name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,created_at)
 VALUES('analytics-replay','unix:///tmp/analytics-replay.sock',2,1024,10,512,now()-interval '12 hours')`)
	exec(`INSERT INTO request_telemetry_coverage
 (node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 VALUES('analytics-replay','11111111-1111-4111-8111-111111111111',1,true,10000,0,0,now()-interval '3 minutes',now(),now()-interval '4 hours')`)
	exec(`INSERT INTO request_analytics_node_coverage
 (node_name,boot_id,counting_since,heartbeat_at,unattributed_dropped_total)
 VALUES('analytics-replay','11111111-1111-4111-8111-111111111111',now()-interval '4 hours',now()-interval '3 minutes',0)`)
	// Preserve both heartbeat timestamps: the previous one bounds a later gap.
	exec(`UPDATE request_telemetry_coverage SET sequence=2,source_at=now()-interval '2 minutes'
 WHERE node_name='analytics-replay'`)
	exec(`UPDATE request_analytics_node_coverage SET heartbeat_at=now()-interval '2 minutes'
 WHERE node_name='analytics-replay'`)
	// These tracked gaps begin at the previous heartbeat, before last_gap_at.
	// Replaying a legacy backfill must not add a second, incomplete gap merely
	// because its primary key would differ from the tracked interval's key.
	exec(`INSERT INTO request_telemetry_app_gaps(node_name,app_id,last_gap_at,pending_count,dropped_total,updated_at)
 VALUES('analytics-replay',$1,now()-interval '1 minute',0,1,now())`, app)
	exec(`INSERT INTO request_analytics_app_gaps(node_name,app_id,last_gap_at,pending_count,updated_at)
 VALUES('analytics-replay',$1,now()-interval '1 minute',0,now())`, app)
	var trackedGaps int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM request_analytics_coverage_gaps g
 JOIN request_analytics_coverage_watermarks w USING(node_name,source_kind)
 WHERE g.node_name='analytics-replay' AND g.gap_from=w.previous_source_at
 AND g.gap_from<w.source_at AND NOT g.history_incomplete`).Scan(&trackedGaps); err != nil {
		t.Fatal(err)
	}
	if trackedGaps != 2 {
		t.Fatalf("seeded %d tracked raw/counting gaps, want 2", trackedGaps)
	}
	exec(`INSERT INTO request_analytics_rollup_audit(app_id,audited_since)
 VALUES($1,now()-interval '4 hours')`, app)
	exec(`INSERT INTO request_analytics_rollup_windows(id,app_id,bucket_start,state)
 VALUES(gen_random_uuid(),$1,date_trunc('minute',now())-interval '2 minutes','pending'),
 (gen_random_uuid(),$1,date_trunc('minute',now())-interval '1 minute','lost')`, app)

	snapshot := func() string {
		t.Helper()
		var evidence string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'watermarks',(SELECT jsonb_agg(to_jsonb(w) ORDER BY node_name,source_kind) FROM request_analytics_coverage_watermarks w),
 'intervals',(SELECT jsonb_agg(to_jsonb(i) ORDER BY node_name,source_kind,covered_since) FROM request_analytics_coverage_intervals i),
 'gaps',(SELECT jsonb_agg(to_jsonb(g) ORDER BY node_name,source_kind,app_id,gap_from) FROM request_analytics_coverage_gaps g),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY app_id) FROM request_analytics_rollup_audit a),
 'windows',(SELECT jsonb_agg(to_jsonb(w) ORDER BY id) FROM request_analytics_rollup_windows w))::text`).Scan(&evidence); err != nil {
			t.Fatal(err)
		}
		return evidence
	}
	before := snapshot()
	for _, filename := range []string{
		"20261111162527186_analytics_ingestion_coverage.sql",
		"20261111163357221_analytics_counting_coverage_history.sql",
	} {
		content, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		up, _, ok := strings.Cut(string(content), "-- +goose Down")
		if !ok {
			t.Fatalf("%s has no Down boundary", filename)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, up); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("replay %s: %v", filename, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshot(); after != before {
		t.Fatalf("replaying coverage migrations changed existing delivery evidence\nbefore: %s\nafter: %s", before, after)
	}
}
