//go:build !no_pg

// PostgreSQL's source spool retention matches MemStore (ADR-20261026120000).
package state_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSourceSpoolInFlightPaths(t *testing.T) {
	testSourceSpoolInFlightPaths(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

// A committed upload's .part is the build source on a single box: the stale
// sweep keeps it while its deployment is in flight and for the 24h retention
// window; cancelled parts keep the 1h grace.
func TestPgReapStaleUploadPartFilesKeepsInFlightSource(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	app, ids := testSourceSpoolInFlightPaths(t, store)
	for _, row := range []struct{ id, status, deployment, age string }{
		{"part-inflight", "committed", ids["imaging"], "48 hours"},
		{"part-done", "committed", ids["failed"], "48 hours"},
		{"part-recent", "committed", ids["live"], "2 hours"},
		{"part-cancelled", "cancelled", "", "2 hours"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO upload_sessions (id, account_id, app_slug, total_size, received_bytes, part_path, status, last_patched_at, deployment_id)
			VALUES ($1, $2::uuid, $3, 1, 1, '/spool/' || $1 || '.part', $4, now() - $5::interval, NULLIF($6, ''))`,
			row.id, app.AccountID, app.Slug, row.status, row.age, row.deployment); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
	}
	stale, err := store.ReapStaleUploadPartFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range stale {
		if strings.HasPrefix(row.ID, "part-") {
			got = append(got, row.ID)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "part-cancelled,part-done" {
		t.Fatalf("stale parts = %v, want [part-cancelled part-done]", got)
	}
}
