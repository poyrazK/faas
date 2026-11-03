//go:build !no_pg

package meter

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// openBuildLogDB returns a migrated private database whether or not the
// template database is enabled (OpenMigrated falls back to an empty Open).
func openBuildLogDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

type buildLogFixture struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	store *state.PgStore
}

func (f buildLogFixture) app(slug string) state.App {
	f.t.Helper()
	account, err := f.store.CreateAccount(f.ctx, slug+"-"+uuid.NewString()[:8]+"@build-logs.test", api.PlanPro)
	if err != nil {
		f.t.Fatal(err)
	}
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: account.ID, Slug: slug + "-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		f.t.Fatal(err)
	}
	return app
}

// deployment creates a deployment; a later create supersedes it while it is
// still pending. failed fails it at once, so it stays failed.
func (f buildLogFixture) deployment(app state.App, failed bool, extra ...func(*state.Deployment)) state.Deployment {
	f.t.Helper()
	d := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Scope: "default"}
	for _, fn := range extra {
		fn(&d)
	}
	created, err := f.store.CreateDeployment(f.ctx, d)
	if err != nil {
		f.t.Fatal(err)
	}
	if failed {
		if err := f.store.UpdateDeploymentStatus(f.ctx, created.ID, state.DeployFailed, "build failed"); err != nil {
			f.t.Fatal(err)
		}
	}
	return created
}

func (f buildLogFixture) createdDaysAgo(id string, days int) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE deployments SET created_at = now() - make_interval(days => $2) WHERE id = $1`, id, days); err != nil {
		f.t.Fatal(err)
	}
}

// logs writes n build lines daysAgo days ago.
func (f buildLogFixture) logs(id string, n, daysAgo int) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO deployment_logs (deployment_id, stream, line, written_at)
SELECT $1, 'build', 'line ' || g, now() - make_interval(days => $3) FROM generate_series(1, $2) g`, id, n, daysAgo); err != nil {
		f.t.Fatal(err)
	}
}

func (f buildLogFixture) rows(id string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM deployment_logs WHERE deployment_id = $1`, id).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f buildLogFixture) sweepAll() int64 {
	f.t.Helper()
	var deleted int64
	for slice := 0; slice < BuildLogRetentionSlices; slice++ {
		res, err := RetentionOnceBuildLogs(f.ctx, partitionTestDB{pool: f.pool}, slice)
		if err != nil {
			f.t.Fatalf("slice %d: %v", slice, err)
		}
		deleted += res.Deleted
	}
	return deleted
}

// adr: 20261102182856
// A build's log is deleted 90 days after its last line unless its deployment
// is live (the serving release and a canary beside it) or among its app's 10
// newest deployments, whatever their status; the count is per app.
func TestBuildLogRetentionKeepsLiveAndNewestDeployments(t *testing.T) {
	ctx := t.Context()
	pool := openBuildLogDB(t)
	f := buildLogFixture{t: t, ctx: ctx, pool: pool, store: state.NewPgStore(pool)}
	retainedOld := BuildLogRetentionDays + 10

	app := f.app("retained")
	// Older than the newest ten, not live: old1 (10,005 lines, more than one
	// delete batch), old2 (failed), old3. old4's last line is recent.
	old1, old2, old3, old4 := f.deployment(app, false), f.deployment(app, true), f.deployment(app, false), f.deployment(app, false)
	newest := make([]state.Deployment, api.BuildLogRetainedDeployments)
	for i := range newest {
		newest[i] = f.deployment(app, i%3 == 0)
	}
	stable := f.deployment(app, false)
	if err := f.store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	canary := f.deployment(app, false, func(d *state.Deployment) {
		d.CanaryPreset, d.CanaryTotalSteps, d.TrafficPercent = "balanced", 4, 10
	})
	if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent = 90, rollout_state = 'complete' WHERE id = $1`, stable.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status = 'live', traffic_percent = 10, rollout_state = 'rolling_out' WHERE id = $1`, canary.ID); err != nil {
		t.Fatal(err)
	}

	// created_at order: stable, canary, old1..old4, then the newest ten.
	f.createdDaysAgo(stable.ID, 300)
	f.createdDaysAgo(canary.ID, 299)
	for i, d := range []state.Deployment{old1, old2, old3, old4} {
		f.createdDaysAgo(d.ID, 200-i)
	}
	for i, d := range newest {
		f.createdDaysAgo(d.ID, 150-i)
	}
	f.logs(stable.ID, 3, 300)
	f.logs(canary.ID, 3, 299)
	f.logs(old1.ID, 10_005, 200)
	f.logs(old2.ID, 3, 199)
	f.logs(old3.ID, 3, 198)
	f.logs(old4.ID, 1, 197)
	f.logs(old4.ID, 1, BuildLogRetentionDays-30)
	for i, d := range newest {
		f.logs(d.ID, 3, 150-i)
	}

	// A second app with three old deployments keeps them all: the newest
	// ten are counted per app.
	other := f.app("other")
	var others []state.Deployment
	for i := 0; i < 3; i++ {
		d := f.deployment(other, i == 1)
		f.createdDaysAgo(d.ID, retainedOld+i)
		f.logs(d.ID, 3, retainedOld+i)
		others = append(others, d)
	}

	if deleted := f.sweepAll(); deleted != 10_005+3+3 {
		t.Fatalf("first sweep deleted %d rows, want %d", deleted, 10_005+3+3)
	}
	want := map[string]int{stable.ID: 3, canary.ID: 3, old1.ID: 0, old2.ID: 0, old3.ID: 0, old4.ID: 2}
	for _, d := range newest {
		want[d.ID] = 3
	}
	for _, d := range others {
		want[d.ID] = 3
	}
	for id, rows := range want {
		if got := f.rows(id); got != rows {
			t.Errorf("deployment %s keeps %d rows, want %d", id, got, rows)
		}
	}
	if deleted := f.sweepAll(); deleted != 0 {
		t.Fatalf("second sweep deleted %d rows", deleted)
	}

	// A new deployment pushes the oldest of the newest ten out; the next
	// sweep deletes its old log and nothing else.
	latest := f.deployment(app, false)
	f.logs(latest.ID, 3, 0)
	if deleted := f.sweepAll(); deleted != 3 {
		t.Fatalf("sweep after a new deployment deleted %d rows, want 3", deleted)
	}
	if got := f.rows(newest[0].ID); got != 0 {
		t.Fatalf("deployment pushed out of the newest ten keeps %d rows", got)
	}
	for _, d := range append(newest[1:], latest, stable, canary) {
		if got := f.rows(d.ID); got != 3 {
			t.Errorf("deployment %s keeps %d rows, want 3", d.ID, got)
		}
	}
}

// A build whose last line is inside the window keeps every line, and the
// sweep leaves a build still being written alone.
func TestBuildLogRetentionKeepsABuildWithARecentLine(t *testing.T) {
	ctx := t.Context()
	pool := openBuildLogDB(t)
	f := buildLogFixture{t: t, ctx: ctx, pool: pool, store: state.NewPgStore(pool)}
	app := f.app("recent")
	var ds []state.Deployment
	for i := 0; i < api.BuildLogRetainedDeployments+2; i++ {
		ds = append(ds, f.deployment(app, true))
		f.createdDaysAgo(ds[i].ID, 400-i)
	}
	f.logs(ds[0].ID, 2, 400)
	f.logs(ds[0].ID, 1, 0)
	f.logs(ds[1].ID, 2, 399)
	if deleted := f.sweepAll(); deleted != 2 {
		t.Fatalf("deleted %d rows, want 2", deleted)
	}
	if got := f.rows(ds[0].ID); got != 3 {
		t.Fatalf("build with a line written today keeps %d of 3 rows", got)
	}
}

// DeploymentLogUsage is what builderd's byte cap re-derives after a restart.
func TestPgDeploymentLogUsageCountsBytesAndMarker(t *testing.T) {
	ctx := t.Context()
	pool := openBuildLogDB(t)
	f := buildLogFixture{t: t, ctx: ctx, pool: pool, store: state.NewPgStore(pool)}
	d := f.deployment(f.app("usage"), true)
	if usage, err := f.store.DeploymentLogUsage(ctx, d.ID); err != nil || usage != (state.DeploymentLogUsage{}) {
		t.Fatalf("empty log usage = %+v, %v", usage, err)
	}
	for _, line := range []string{"step 1\n", "ünïcode\n"} {
		if _, err := f.store.AppendDeploymentLog(ctx, d.ID, "build", line); err != nil {
			t.Fatal(err)
		}
	}
	wantBytes := int64(len("step 1\n") + len("ünïcode\n"))
	if usage, err := f.store.DeploymentLogUsage(ctx, d.ID); err != nil || usage.Bytes != wantBytes || usage.Truncated {
		t.Fatalf("usage = %+v, %v; want %d bytes, not truncated", usage, err, wantBytes)
	}
	if _, err := f.store.AppendDeploymentLog(ctx, d.ID, state.DeploymentLogStreamSystem, "[truncated]\n"); err != nil {
		t.Fatal(err)
	}
	if usage, err := f.store.DeploymentLogUsage(ctx, d.ID); err != nil || !usage.Truncated || usage.Bytes != wantBytes+int64(len("[truncated]\n")) {
		t.Fatalf("usage with marker = %+v, %v", usage, err)
	}
	if usage, err := f.store.DeploymentLogUsage(ctx, "not-a-uuid"); err != nil || usage != (state.DeploymentLogUsage{}) {
		t.Fatalf("malformed id usage = %+v, %v", usage, err)
	}
}
