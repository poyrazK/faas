//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPgStore_CreateEdgeRuleIfUnderQuotaRetriesConcurrentAppWrite — schedd
// stamps a new app's owner node right after apid creates it. A rule created in
// that window locked the app row inside a repeatable-read snapshot older than
// the stamp, failed with a serialization failure, and apid answered 503 "could
// not create edge rule" (e2e-general edge-rules, 2026-10-29). The create now
// retries with a fresh snapshot, as app creation and rule-set rollback do.
func TestPgStore_CreateEdgeRuleIfUnderQuotaRetriesConcurrentAppWrite(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "serialization")

	// Hold schedd's update uncommitted, so the create takes its snapshot first
	// and then waits on the row.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `update apps set node_id = $2 where id = $1`, app, resolveDefaultLocal(t, ctx, s)); err != nil {
		t.Fatalf("hold app row: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateEdgeRuleIfUnderQuota(ctx, pgSampleEdgeRuleParams(acct, app, "serialization.example.com"), api.MustLimitsFor(api.PlanPro))
		done <- err
	}()
	for waiting := false; !waiting; {
		if err := pool.QueryRow(ctx, `select exists (select 1 from pg_stat_activity
			where datname = current_database() and wait_event_type = 'Lock' and query ilike '%from apps where id%for update%')`).Scan(&waiting); err != nil {
			t.Fatalf("read lock waits: %v", err)
		}
		select {
		case err := <-done:
			t.Fatalf("create finished before it waited on the app row: %v", err)
		case <-ctx.Done():
			t.Fatal("create never waited on the app row")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit app row update: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("create after a concurrent app row update: %v", err)
	}
}
