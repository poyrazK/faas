package state_test

// ADR-20261122220000 amendment 2: deployment verification leases against
// PostgreSQL.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreDeploymentVerificationLeases(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	var leases state.VerificationLeaseStore = store
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	acct, err := store.CreateAccount(ctx, fmt.Sprintf("lease+%s@example.com", name), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "lease-" + name[:min(len(name), 30)], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:lease", Scope: state.DefaultEnvScope})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)
	if err := leases.PutDeploymentVerificationLease(ctx, dep.ID, app.ID, "compute-1", until); err != nil {
		t.Fatal(err)
	}
	// A later activation takes the lease over.
	if err := leases.PutDeploymentVerificationLease(ctx, dep.ID, app.ID, "compute-2", until); err != nil {
		t.Fatal(err)
	}
	var node string
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT node_name, expires_at FROM deployment_verification_leases WHERE deployment_id = $1`, dep.ID).Scan(&node, &expires); err != nil {
		t.Fatal(err)
	}
	if node != "compute-2" || !expires.Equal(until) {
		t.Fatalf("lease = %s until %s", node, expires)
	}
	if err := leases.DeleteDeploymentVerificationLease(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := leases.DeleteDeploymentVerificationLease(ctx, dep.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deployment_verification_leases WHERE deployment_id = $1`, dep.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after delete = %d, %v", n, err)
	}
}
