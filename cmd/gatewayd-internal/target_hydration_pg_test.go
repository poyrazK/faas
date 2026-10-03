// adr: 375
package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type hydrationCapacityScheduler struct{ gateway.NoopScheduler }

func (hydrationCapacityScheduler) AdmitInstance(context.Context, string, string, string, string) (string, string, string, string, int32, bool, int, error) {
	return "", "", "", "", 0, true, 0, nil
}

func TestTargetHydrationPostgresCurrentReadSourcesTimeoutRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "hydration@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "hydration", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:hydration", Status: state.DeployPending, TrafficPercent: 100, OverridePort: 9090, OverrideReadinessProbe: []byte(`{"path":"/readyz"}`)})
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
	for range api.TrafficPlacementTargetsPerApp + 10 {
		if _, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 128, node.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "foreign-hydration", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	foreignDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: foreign.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:foreign", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, foreignDep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(ctx, app.ID, foreignDep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID, Port: 9090}
	newBackend := func() *gateway.PGBackend {
		return gateway.NewPGBackend(nil, hydrationCapacityScheduler{}, discardLogger()).WithTargetPlacementLoader(newTargetPlacementLoader(store)).WithTargetReadinessLoader(newTargetReadinessLoader(store))
	}
	unknown := newBackend()
	if err := unknown.ReconcileLiveTargets(ctx, app.ID); err != nil || unknown.Pick(app.ID).OK || unknown.CapacityCount(app.ID) != 1 {
		t.Fatalf("restart discovery bypassed missing readiness or historical bounds: capacity=%d pick=%+v err=%v", unknown.CapacityCount(app.ID), unknown.Pick(app.ID), err)
	}
	body, err := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "vmmd", "wake.app_readiness", &instance.ID, body); err != nil {
		t.Fatal(err)
	}
	backends := make(map[string]*gateway.PGBackend)
	read := func(b *gateway.PGBackend, path string) error {
		switch path {
		case "reconcile":
			return b.ReconcileLiveTargets(ctx, app.ID)
		case "refresh":
			return b.RefreshLiveTargets(ctx, app.ID)
		case "validate":
			_, err := b.ValidateLiveTarget(ctx, app.ID, instance.ID)
			return err
		default:
			_, _, atCapacity, err := b.Admit(ctx, app.ID, dep.ID, state.DefaultEnvScope, "gateway", 5)
			if !atCapacity {
				t.Fatal("capacity fixture lost the scheduler's typed result")
			}
			return err
		}
	}
	for _, path := range []string{"reconcile", "refresh", "validate", "at capacity"} {
		t.Run(path, func(t *testing.T) {
			b := newBackend()
			backends[path] = b
			if path == "validate" {
				b.RecordTarget(app.ID, target)
			}
			if err := read(b, path); err != nil {
				t.Fatal(err)
			}
			picked := b.Pick(app.ID)
			if !picked.OK || b.CapacityCount(app.ID) != 1 || picked.Target.InstanceID != instance.ID || picked.Target.AppID != app.ID || picked.Target.DeploymentID != dep.ID || picked.Target.NodeID != node.ID || picked.Target.WakeID != instance.WakeID || picked.Target.Port != 9090 || picked.Target.ImageDigest != dep.ImageDigest {
				t.Fatalf("scoped hydration lost current identity/provenance: capacity=%d pick=%+v", b.CapacityCount(app.ID), picked)
			}
		})
	}
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
	for path, b := range backends {
		if err := read(b, path); err == nil || b.Pick(app.ID).OK || b.CapacityCount(app.ID) != 1 {
			t.Fatalf("current hydration outage failed open: path=%s err=%v capacity=%d pick=%+v", path, err, b.CapacityCount(app.ID), b.Pick(app.ID))
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for path, b := range backends {
		if err := read(b, path); err != nil || !b.Pick(app.ID).OK || b.CapacityCount(app.ID) != 1 {
			t.Fatalf("current hydration did not recover: path=%s err=%v", path, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE instances SET state='parked' WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	for _, b := range backends {
		if err := b.RefreshLiveTargets(ctx, app.ID); err != nil || b.CapacityCount(app.ID) != 0 || b.Pick(app.ID).OK {
			t.Fatalf("complete request snapshot retained stopped residents: %v", err)
		}
	}
	t.Log("actual SQLC readers, historical and foreign-owner exclusions, source verification, bounded SQL outage/recovery across four independent caches; scheduler, guest and node execution remain fixtures")
}
