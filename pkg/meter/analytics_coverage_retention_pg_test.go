//go:build !no_pg

// adr: 20261111140000 — retain ingestion evidence through supported analytics windows.
package meter

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAnalyticsCoverageRetentionKeepsCurrentEvidence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "coverage-retention-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "coverage-retention"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-time.Duration(api.MustLimitsFor(api.PlanScale).DebugTelemetryRetentionDays+1) * 24 * time.Hour)
	for _, at := range []time.Time{old, now} {
		for _, kind := range []string{"pending", "lost"} {
			if err := store.RecordRequestAnalyticsWindow(t.Context(), uuid.New(), uuid.MustParse(app.ID), at, kind); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, at := range []time.Time{old, now} {
		node := []string{"expired-coverage", "current-coverage"}[i]
		if _, err = pool.Exec(t.Context(), "INSERT INTO request_analytics_coverage_intervals(node_name,covered_since,covered_until) VALUES($1,$2,$2)", node, at); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), "INSERT INTO request_analytics_coverage_watermarks(node_name,registered_at,previous_source_at,source_at) VALUES($1,$2,$2,$2)", node, at); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(t.Context(), "INSERT INTO request_analytics_coverage_gaps(node_name,app_id,gap_from,gap_until) VALUES($1,$2,$3,$3)", node, app.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := RetentionOnceAnalyticsCoverage(t.Context(), rollupRetentionPG{pool})
	if err != nil || deleted != 5 {
		t.Fatalf("deleted=%d err=%v, want five expired evidence rows", deleted, err)
	}
	var windows, audits int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM request_analytics_rollup_windows").Scan(&windows); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM request_analytics_rollup_audit WHERE app_id=$1", app.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if windows != 2 || audits != 1 {
		t.Fatalf("current windows/audit were lost: %d/%d", windows, audits)
	}
}
