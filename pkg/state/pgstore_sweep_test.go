package state_test

// PG-backed round-trips for the stuck-running build sweep (issue #195
// B1.4). The unit-level tests in pkg/builderd/reaper_test.go cover
// the MemStore path; this file exercises the actual SQL against a
// real Postgres so the partial-index migration, the CAS guard, and
// the sweep query are all verified end-to-end.
//
// Skips when Postgres is unreachable via pgtest.Open — no `make test`
// regression in environments without a running cluster.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSweepDeps carries the same shape as pgDeps in
// pgstore_account_deletion_test.go. PgStore does not expose its pool
// (memory note: pgStore(t) hides the pool by design), so the test
// fixture re-opens it via pgtest.Open to backdate started_at.
type pgSweepDeps struct {
	store *state.PgStore
	pool  *pgxpool.Pool
	ctx   context.Context
}

func pgSweepStore(t *testing.T) pgSweepDeps {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pgSweepDeps{store: state.NewPgStore(pool), pool: pool, ctx: ctx}
}

// seedSweepBuild creates one account + app + deployment + build row
// in 'running' so the sweep can match it. Mirrors the MemStore
// fixture in pkg/builderd/reaper_test.go but seeds against the SQL
// tables.
func seedSweepBuild(t *testing.T, s *state.PgStore, ctx context.Context) (acctID, buildID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "sweep-"+t.Name()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed acct: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "sweep-" + t.Name(), Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	// Tarball kind is the only one in builds_kind_check that we can also
	// drive from CreateDeployment. Image deploys skip the builds table
	// entirely (they go through imaged directly), so DeploymentKindImage
	// would violate the CHECK and fail the test setup.
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/source.tar.gz",
		Status: state.DeployBuilding,
	})
	if err != nil {
		t.Fatalf("seed dep: %v", err)
	}
	b, err := s.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 1<<20, "/tmp/log")
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	if err := s.UpdateBuildStatus(ctx, b.ID, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("flip to running: %v", err)
	}
	return acct.ID, b.ID
}

// TestSweepStuckRunningBuilds_PgStore exercises the actual SQL
// against a real Postgres. The threshold is 5 minutes; the build
// created by seedSweepBuild has started_at ≈ now() which is well
// inside the threshold. The sweep must match 0 rows.
//
// Then we backdate the row via the pool (state.PgStore doesn't expose
// started_at directly) and re-sweep. The sweep must match 1 row and
// flip status='failed' + failure_class='timeout' + finished_at stamped.
func TestSweepStuckRunningBuilds_PgStore(t *testing.T) {
	d := pgSweepStore(t)
	_, buildID := seedSweepBuild(t, d.store, d.ctx)

	// First sweep: row is fresh → no match.
	n, err := d.store.SweepStuckRunningBuilds(d.ctx, time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("sweep fresh: %v", err)
	}
	if n != 0 {
		t.Errorf("fresh sweep: count = %d, want 0", n)
	}

	// Backdate the row 10 minutes. state.PgStore doesn't expose a
	// SetBuildStartedAtForTest hook (intentional — the public surface
	// doesn't include started_at mutation). Reach around via the pool
	// since this is a test.
	if _, err := d.pool.Exec(d.ctx,
		`update builds set started_at = now() - interval '10 minutes' where id = $1`,
		buildID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	// Re-sweep: must match 1 row.
	n, err = d.store.SweepStuckRunningBuilds(d.ctx, time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("sweep backdated: %v", err)
	}
	if n != 1 {
		t.Errorf("backdated sweep: count = %d, want 1", n)
	}

	// Verify the row is now failed+timeout+finished_at stamped.
	got, err := d.store.BuildByID(d.ctx, buildID)
	if err != nil {
		t.Fatalf("BuildByID: %v", err)
	}
	if got.Status != state.BuildFailed {
		t.Errorf("post-sweep status = %q, want %q", got.Status, state.BuildFailed)
	}
	if got.FailureClass != state.FailureTimeout {
		t.Errorf("post-sweep failure_class = %q, want %q",
			got.FailureClass, state.FailureTimeout)
	}
	if got.FinishedAt.IsZero() {
		t.Error("post-sweep finished_at was not stamped")
	}
	dep, err := d.store.DeploymentByID(d.ctx, got.DeploymentID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if dep.Status != state.DeployFailed {
		t.Errorf("post-sweep deployment status = %q, want %q", dep.Status, state.DeployFailed)
	}
	if dep.ErrorCode != api.CodeBuildTimeout {
		t.Errorf("post-sweep deployment error_code = %q, want %q", dep.ErrorCode, api.CodeBuildTimeout)
	}

	// Idempotency: a second sweep matches 0 rows.
	n, err = d.store.SweepStuckRunningBuilds(d.ctx, time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("idempotent sweep: count = %d, want 0", n)
	}

	// Late markSucceeded cannot resurrect (CAS guard regression).
	// The row is now 'failed'; UpdateBuildStatus(BuildSucceeded, …)
	// must return ErrNotFound.
	err = d.store.UpdateBuildStatus(d.ctx, buildID, state.BuildSucceeded, "", false, true)
	if err == nil {
		t.Error("B1.4 race regression: late markSucceeded resurrected a swept row")
	}
}

// TestUpdateBuildStatus_CASGuard_PgStore verifies the terminal-write
// CAS guard against real Postgres. A row in 'running' can be flipped
// to 'failed' (CAS succeeds); a row already in 'failed' cannot be
// flipped to 'succeeded' (CAS fails; ErrNotFound).
func TestUpdateBuildStatus_CASGuard_PgStore(t *testing.T) {
	d := pgSweepStore(t)
	_, buildID := seedSweepBuild(t, d.store, d.ctx)

	// Row is 'running' → flip to 'failed' (CAS must succeed).
	if err := d.store.UpdateBuildStatus(d.ctx, buildID, state.BuildFailed, state.FailureInfra, false, true); err != nil {
		t.Fatalf("running → failed: %v", err)
	}
	// Row is now 'failed' → flip to 'succeeded' (CAS must fail).
	err := d.store.UpdateBuildStatus(d.ctx, buildID, state.BuildSucceeded, "", false, true)
	if err == nil {
		t.Error("CAS guard regression: failed → succeeded allowed (should be rejected)")
	}
}
