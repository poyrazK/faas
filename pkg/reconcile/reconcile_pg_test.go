//go:build !no_pg

// Postgres integration tests for pkg/reconcile. Run via
// `go test ./pkg/reconcile -tags=!no_pg` or via `make test`.
// Mirrors the pgStore helper from pkg/state/pgstore_test.go so
// the schema lifecycle is identical to the state package's
// integration suite. Each test stands up a fresh schema, runs
// the migrations, and exercises the reconcile Service against
// the real PgStore.
//
// pgtest.Open() skips the test when Postgres is unreachable so
// the file is safe to compile + run on a CI runner without
// needing a local Postgres on the dev box.

package reconcile_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgReconcileStore sets up a fresh Postgres schema, migrates it,
// and returns a PgStore + audit auditor + reconcile Service wired
// together. The auditor uses real pkg/audit (best-effort) against
// the events table; the pg tests assert on the events table
// directly, not on counters.
func pgReconcileStore(t *testing.T) (*state.PgStore, *reconcile.Service, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	aud := audit.New(store, log, pgNoopOps{}, "reconcile")
	svc := reconcile.NewService(store, aud, log)
	return store, svc, pool, ctx
}

// pgNoopOps returns real prometheus Counter / Observer instances
// but never registers them with a registry. The audit pipeline
// .Inc() / .Observe() calls are no-ops. Avoids the nil-pointer
// crash that a hand-rolled nil-returning stub would hit.
type pgNoopOps struct{}

func (pgNoopOps) AuditWriteFailures(string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{})
}
func (pgNoopOps) AuditWriteFailureDuration(string) prometheus.Observer {
	return prometheus.NewHistogram(prometheus.HistogramOpts{})
}

// AuditLogWriteTotal + AuditLogWriteFailuresTotal
// (PR-#TBD / C5) — same no-op shape as the existing two
// methods. The reconcile-pg tests don't assert on these
// counters; the audit emit path increments them as a
// side-effect of every emit.
func (pgNoopOps) AuditLogWriteTotal(string, string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{})
}
func (pgNoopOps) AuditLogWriteFailuresTotal(string, string, string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{})
}

func countAuditRows(t *testing.T, pool *pgxpool.Pool, kind string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`select count(*) from events where kind = $1`, kind).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func seedAccountProject(t *testing.T, store *state.PgStore, scanSource state.ProjectScanSource) (state.Account, state.Project) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "reconcile-pg@example.test", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, err := store.CreateProject(context.Background(), state.Project{
		AccountID:        acct.ID,
		Slug:             "reconcile-pg",
		RepoFullName:     "octocat/reconcile-pg",
		ProductionBranch: "main",
		ScanSource:       scanSource,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return acct, proj
}

func TestPgReconcile_FullCycle(t *testing.T) {
	store, svc, pool, ctx := pgReconcileStore(t)
	_, proj := seedAccountProject(t, store, state.ProjectScanSourceCompose)

	// 3-workload scan with no existing apps → 3 creates.
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", RootDir: "", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
			{Name: "web", RootDir: "", Source: "compose.yaml: web", Tier: reposcan.TierCompose},
			{Name: "worker", RootDir: "", Source: "compose.yaml: worker", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	out, err := svc.Reconcile(ctx, proj, scan, "sha-pg-1", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(out.Added) != 3 {
		t.Errorf("expected 3 added, got %d", len(out.Added))
	}
	if countAuditRows(t, pool, "project.workload.added") != 3 {
		t.Errorf("expected 3 workload.added audit rows")
	}
	if countAuditRows(t, pool, "project.reconcile.started") != 1 {
		t.Errorf("expected 1 reconcile.started audit row")
	}
}

func TestPgReconcile_Quota_BlocksCreateSet(t *testing.T) {
	store, svc, pool, ctx := pgReconcileStore(t)
	acct, proj := seedAccountProject(t, store, state.ProjectScanSourceCompose)
	// Use the one-app Free cap so this remains deterministic even if the
	// paid-plan quota table changes independently of this regression test.
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}

	// Free plan cap = 1. Seed 4 existing apps, then attempt 3
	// creates → projected 7 > 1 → quota_blocked alert.
	for _, n := range []string{"app-a", "app-b", "app-c", "app-d"} {
		app := state.App{
			AccountID:     proj.AccountID,
			ProjectID:     proj.ID,
			Slug:          n,
			RootDir:       "",
			WorkloadName:  n,
			WorkloadClass: state.WorkloadClassHTTP,
			Status:        state.AppActive,
		}
		if _, err := store.CreateApp(context.Background(), app); err != nil {
			t.Fatalf("seed app %s: %v", n, err)
		}
	}

	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "app-e", Source: "compose.yaml: app-e", Tier: reposcan.TierCompose},
			{Name: "app-f", Source: "compose.yaml: app-f", Tier: reposcan.TierCompose},
			{Name: "app-g", Source: "compose.yaml: app-g", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	out, err := svc.Reconcile(ctx, proj, scan, "sha-pg-2", "main", nil)
	if err == nil {
		t.Fatal("expected quota error")
	}
	if len(out.Added) != 0 {
		t.Errorf("expected 0 adds on quota, got %d", len(out.Added))
	}
	if len(out.Alerts) != 1 || out.Alerts[0].Kind != "quota_blocked" {
		t.Errorf("expected quota_blocked alert, got %v", out.Alerts)
	}
	if countAuditRows(t, pool, "project.reconcile.quota_blocked") != 1 {
		t.Errorf("expected 1 quota_blocked audit row")
	}
}

func TestPgReconcile_ScanSourceUpgrade(t *testing.T) {
	store, svc, _, ctx := pgReconcileStore(t)
	_, proj := seedAccountProject(t, store, state.ProjectScanSourceSingle)

	// Scan tier = Compose, stored tier = Single. Upgrade is
	// allowed; the alert flow is silent until downgrade.
	scan := reposcan.Result{
		Workloads: []reposcan.Workload{
			{Name: "api", Source: "compose.yaml: api", Tier: reposcan.TierCompose},
		},
		Tier: reposcan.TierCompose,
	}
	_, err := svc.Reconcile(ctx, proj, scan, "sha-pg-3", "main", nil)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	updated, err := store.ProjectByID(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ProjectByID: %v", err)
	}
	if updated.ScanSource != state.ProjectScanSourceCompose {
		t.Errorf("expected ScanSource=compose, got %q", updated.ScanSource)
	}
}
