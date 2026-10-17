// adr: 570
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetPlacementPostgresRepairWithoutNotifications(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "placement-repair@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "placement-repair", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:placement", CommitSHA: "abcdef0", Tag: "hotfix", Scope: "production", Status: state.DeployPending, TrafficPercent: 100, OverrideReadinessProbe: []byte(`{"path":"/readyz"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	destinationRegion := "qualification-destination"
	destination, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "placement-destination", Region: &destinationRegion, TargetURL: "unix:///run/placement.sock",
		MemMB: 1024, AdmissionCeilingMB: 512, VPCPUs: 1, VCPUBudget: 1, MaxConcurrency: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	create := func() gateway.Target {
		t.Helper()
		ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID}
	}
	stopped, moving := create(), create()
	backends := []*gateway.PGBackend{}
	for range 2 {
		b := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetPlacementLoader(newTargetPlacementLoader(store)).WithTargetReadinessLoader(newTargetReadinessLoader(store))
		b.RecordTarget(app.ID, stopped)
		b.RecordTarget(app.ID, moving)
		backends = append(backends, b)
	}
	if _, err := pool.Exec(ctx, `UPDATE instances SET state='parked' WHERE id=$1`, stopped.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instances SET node_id=$2 WHERE id=$1`, moving.InstanceID, destination.ID); err != nil {
		t.Fatal(err)
	}
	current := moving
	current.NodeID = destination.ID
	newlyStarted := create()
	if _, err := pool.Exec(ctx, `UPDATE deployments SET override_port=9090 WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	for _, b := range backends {
		b.TouchTarget(app.ID, stopped.InstanceID, time.Now())
		if err := b.ReconcileTargetPlacements(ctx); err != nil {
			t.Fatal(err)
		}
		if b.CapacityCount(app.ID) != 2 || b.Pick(app.ID).OK {
			t.Fatal("placement repair bypassed missing readiness or retained stopped resident")
		}
	}
	for _, target := range []gateway.Target{current, newlyStarted} {
		body, err := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": target.InstanceID, "wake_id": target.WakeID, "node_id": target.NodeID, "status": "ready"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "vmmd", "wake.app_readiness", nil, body); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range backends {
		if err := b.ReconcileTargetPlacements(ctx); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for range 6 {
			picked := b.Pick(app.ID)
			if !picked.OK || picked.Target.Port != 9090 || picked.Target.InstanceID == stopped.InstanceID || (picked.Target.InstanceID == moving.InstanceID && picked.Target.NodeID != destination.ID) {
				t.Fatalf("stale placement: %+v", picked)
			}
			if picked.Target.CommitSHA != dep.CommitSHA || picked.Target.DeploymentTag != dep.Tag || picked.Target.ImageDigest != dep.ImageDigest || picked.Target.DeploymentCreatedAt != dep.CreatedAt.UTC().Format(time.RFC3339Nano) || (picked.Target.InstanceID == moving.InstanceID && picked.Target.Region != destinationRegion) {
				t.Fatalf("repaired placement lost durable provenance: %+v", picked.Target)
			}
			seen[picked.Target.InstanceID] = true
		}
		endpoints, err := b.ServiceEndpoints(ctx, app.ID)
		if err != nil || len(endpoints.Endpoints) != 2 || len(seen) != 2 {
			t.Fatalf("managed/public fan-out: %+v %v", endpoints, err)
		}
	}
	// Lock only the private fixture database. Actual SQLC timeout withdraws
	// routing on both independently cached gateways, then repairs after release.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE instances IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for _, b := range backends {
		if err := b.ReconcileTargetPlacements(ctx); err == nil || b.Pick(app.ID).OK || b.CapacityCount(app.ID) != 2 {
			t.Fatalf("store outage: %v", err)
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, b := range backends {
		if err := b.ReconcileTargetPlacements(ctx); err != nil || !b.Pick(app.ID).OK || b.CapacityCount(app.ID) != 2 {
			t.Fatalf("placement recovery: %v", err)
		}
	}
	t.Log("two independent caches, actual SQLC placement/readiness reads, no LISTEN subscribers; row transitions and node/guest execution are fixtures")
}

func TestTargetPlacementDaemonStartupJoinsWorkerOnFailure(t *testing.T) {
	t.Setenv("FAAS_CONSUMER_USAGE_OUTBOX_ROOT", t.TempDir())
	started, stopped := make(chan struct{}), make(chan struct{})
	b := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetPlacementLoader(func(ctx context.Context, _ []string) (map[string]gateway.TargetPlacementSnapshot, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	b.RecordTarget("app", gateway.Target{AppID: "app", InstanceID: "instance", NodeID: "node", DeploymentID: "deployment"})
	deps := defaultDeps()
	deps.backend, deps.config = b, &Config{RateLimit: TOMLRateLimitConfig{Mode: "local"}}
	deps.capCheck = func() error { return nil }
	deps.listen = func(string, string) (net.Listener, error) {
		select {
		case <-started:
			return nil, errors.New("placement test listen failure")
		case <-time.After(time.Second):
			return nil, errors.New("placement worker did not start")
		}
	}
	if err := runWithDeps(t.Context(), discardLogger(), deps); err == nil {
		t.Fatal("listen failure was swallowed")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("daemon returned with live placement query")
	}
}
