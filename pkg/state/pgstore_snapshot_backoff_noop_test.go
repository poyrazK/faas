// spec: §6 — clearing an already-clear snapshot backoff writes no tuple.
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func deploymentXmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var xmin string
	if err := pool.QueryRow(ctx, `select xmin::text from deployments where id=$1::uuid`, id).Scan(&xmin); err != nil {
		t.Fatalf("read xmin: %v", err)
	}
	return xmin
}

// Every successful wake clears the backoff. A clear row must stay untouched
// (same xmin) so the deployments row triggers do not fire on the wake path,
// while a recorded miss is still reset.
func TestPgDeploymentClearSnapshotBackoffSkipsClearRow(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, _, depID := seedLiveDeploy(t, s, ctx, "backoff-noop", "backoff-noop")

	before := deploymentXmin(t, ctx, pool, depID)
	if err := s.DeploymentClearSnapshotBackoff(ctx, depID); err != nil {
		t.Fatalf("clear on clear row: %v", err)
	}
	if after := deploymentXmin(t, ctx, pool, depID); after != before {
		t.Fatalf("clear on an already-clear row rewrote it: xmin %s -> %s", before, after)
	}

	if err := s.DeploymentRecordSnapshotMiss(ctx, depID, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("record miss: %v", err)
	}
	if err := s.DeploymentClearSnapshotBackoff(ctx, depID); err != nil {
		t.Fatalf("clear after miss: %v", err)
	}
	var count int
	var lastAtSet, untilSet bool
	if err := pool.QueryRow(ctx, `select snapshot_miss_count, snapshot_miss_last_at is not null,
		snapshot_miss_backoff_until is not null from deployments where id=$1::uuid`, depID).Scan(&count, &lastAtSet, &untilSet); err != nil {
		t.Fatalf("read backoff: %v", err)
	}
	if count != 0 || lastAtSet || untilSet {
		t.Fatalf("backoff not cleared after a miss: count=%d last_at=%v until=%v", count, lastAtSet, untilSet)
	}
}
