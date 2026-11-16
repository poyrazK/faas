package state

import (
	"context"
	"testing"
	"time"
)

// A deploy whose traffic policy analysis ran out of time was refused as 422
// traffic_policy_too_complex; only the Go client resent it, so concurrent raw
// API deploys of a project's services failed (2026-11-12 agent feedback).
// CreateDeployment now reruns the create, as app delete does.
func TestPgCreateDeploymentRetriesTimedOutTrafficAnalysis(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE edge_rules IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	// Hold the lock past the first attempt's 2 s analysis budget, then let
	// the retry through.
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(2500 * time.Millisecond)
		_ = lock.Rollback(context.Background())
	}()
	start := time.Now()
	created, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployPending})
	elapsed := time.Since(start)
	<-released
	if err != nil {
		t.Fatalf("create deployment was not retried after a timed-out analysis: %v (after %s)", err, elapsed)
	}
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("create finished in %s: the analysis never waited on the lock, so the test proved nothing", elapsed)
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `select count(*) from deployments where app_id = $1`, app.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || created.ID == "" {
		t.Fatalf("refused attempt left a row or the retry made none: rows=%d created=%q", rows, created.ID)
	}
}
