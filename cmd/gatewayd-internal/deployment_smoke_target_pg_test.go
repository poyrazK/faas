// adr: 531
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentSmokeCandidatePostgresSourcesHistoryTimeoutRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "smoke-candidate@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "smoke-candidate", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:smoke-candidate", Status: state.DeployPending, CommitSHA: "0123456789abcdef0123456789abcdef01234567", Tag: "hotfix", OverridePort: 9090,
		OverrideReadinessProbe: []byte(`{"path":"/readyz"}`), Sidecars: []byte(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{}}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='snapshotting', inferred_profile='{"version":1,"port":3000,"private_extra":"must-not-project"}' WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(id,app_id,deployment_id,node_id,wake_id,state,ram_mb,started_at) SELECT gen_random_uuid(),$1,$2,$3,gen_random_uuid(),'parked',128,now()+interval '5 minutes' FROM generate_series(1,2000)`, app.ID, dep.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "foreign-smoke", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(ctx, foreign.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.RunningDeploymentSmokeTarget(ctx, foreign.ID, dep.ID); err != nil || found {
		t.Fatalf("foreign deployment owner entered private candidate lookup: found=%t err=%v", found, err)
	}
	placement, found, err := store.RunningDeploymentSmokeTarget(ctx, app.ID, dep.ID)
	if err != nil || !found || placement.InstanceID != instance.ID || placement.DeploymentLive || strings.Contains(string(placement.InferredProfile), "must-not-project") {
		t.Fatalf("history/ownership/projection: placement=%+v found=%t err=%v", placement, found, err)
	}
	backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithDeploymentSmokeTargetLoader(newDeploymentSmokeTargetLoader(store)).WithTargetReadinessLoader(newTargetReadinessLoader(store))
	resolve := func(t *testing.T, wantReady bool) {
		t.Helper()
		target, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, dep.ID)
		if found != wantReady || (wantReady && (err != nil || target.InstanceID != instance.ID || target.AppID != app.ID || target.DeploymentID != dep.ID || target.NodeID != instance.NodeID || target.WakeID != instance.WakeID || target.Port != 9090 || target.CommitSHA != "0123456789abcdef0123456789abcdef01234567" || target.DeploymentTag != "hotfix" || target.ImageDigest != dep.ImageDigest || target.DeploymentCreatedAt == "")) || (!wantReady && err == nil) {
			t.Fatalf("private candidate readiness: want=%t target=%+v found=%t err=%v", wantReady, target, found, err)
		}
		if backend.CapacityCount(app.ID) != 0 || backend.PickForDeployment(app.ID, dep.ID).OK {
			t.Fatal("unpromoted candidate entered ordinary capacity")
		}
	}
	at := time.Now().UTC().Add(-time.Minute)
	publish := func(t *testing.T, source, status string, sequence int) {
		t.Helper()
		body := map[string]string{"app_id": app.ID, "instance_id": instance.ID, "wake_id": instance.WakeID, "node_id": instance.NodeID, "status": status}
		kind := "wake.app_readiness"
		if source == "proxy" {
			kind, body["sidecar_name"] = "wake.sidecar_health", source
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEventAt(ctx, "vmmd", kind, &instance.ID, data, at.Add(time.Duration(sequence)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("complete source set and recovery", func(t *testing.T) {
		resolve(t, false)
		publish(t, "primary", "ready", 1)
		resolve(t, false)
		publish(t, "proxy", "unready", 2)
		resolve(t, false)
		publish(t, "proxy", "ready", 3)
		resolve(t, true)
		publish(t, "primary", "unready", 4)
		resolve(t, false)
		publish(t, "primary", "ready", 5)
		resolve(t, true)
	})
	t.Run("newest current lifetime", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE instances SET started_at=now()-interval '5 minutes' WHERE id=$1`, instance.ID); err != nil {
			t.Fatal(err)
		}
		newer, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		instance = newer
		resolve(t, false)
		publish(t, "primary", "ready", 6)
		resolve(t, false)
		publish(t, "proxy", "ready", 7)
		resolve(t, true)
	})
	for _, lock := range []struct{ name, statement string }{
		{"placement", "LOCK TABLE instances IN ACCESS EXCLUSIVE MODE"},
		{"configuration", "LOCK TABLE deployments IN ACCESS EXCLUSIVE MODE"},
		{"observations", "LOCK TABLE events IN ACCESS EXCLUSIVE MODE"},
	} {
		t.Run(lock.name+" timeout recovery", func(t *testing.T) {
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := tx.Exec(ctx, lock.statement); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			_, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, dep.ID)
			if found || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > api.TrafficPlacementReadTimeout+time.Second {
				t.Fatalf("SQL outage exceeded shared bound or returned candidate: found=%t elapsed=%s err=%v", found, time.Since(started), err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			resolve(t, true)
		})
	}
	t.Run("eligible deployment states", func(t *testing.T) {
		for _, phase := range []string{"pending", "building", "imaging", "failed", "superseded", "cancelled", "live", "snapshotting"} {
			if _, err := pool.Exec(ctx, `UPDATE deployments SET status=$2 WHERE id=$1`, dep.ID, phase); err != nil {
				t.Fatal(err)
			}
			_, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, dep.ID)
			if err != nil || found != (phase == "live" || phase == "snapshotting") {
				t.Fatalf("deployment eligibility: phase=%s found=%t err=%v", phase, found, err)
			}
		}
	})
	t.Run("deleted deployment and app", func(t *testing.T) {
		for _, mutation := range []struct{ remove, restore string }{
			{"UPDATE deployments SET deleted_at=now() WHERE id=$1", "UPDATE deployments SET deleted_at=NULL WHERE id=$1"},
			{"UPDATE apps SET status='deleted' WHERE id=$1", "UPDATE apps SET status='active' WHERE id=$1"},
		} {
			id := dep.ID
			if strings.Contains(mutation.remove, "UPDATE apps") {
				id = app.ID
			}
			if _, err := pool.Exec(ctx, mutation.remove, id); err != nil {
				t.Fatal(err)
			}
			if _, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, dep.ID); err != nil || found {
				t.Fatalf("deleted owner/candidate retained: found=%t err=%v", found, err)
			}
			if _, err := pool.Exec(ctx, mutation.restore, id); err != nil {
				t.Fatal(err)
			}
			resolve(t, true)
		}
	})
	t.Run("disabled configuration", func(t *testing.T) {
		disabled, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:smoke-disabled", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE deployments SET status='snapshotting' WHERE id=$1`, disabled.ID); err != nil {
			t.Fatal(err)
		}
		resident, err := store.CreateInstance(ctx, app.ID, disabled.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if target, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, disabled.ID); err != nil || !found || target.InstanceID != resident.ID || target.RequiresReadiness || backend.CapacityCount(app.ID) != 0 {
			t.Fatalf("owner-matched disabled configuration: target=%+v found=%t err=%v", target, found, err)
		}
	})
	t.Run("terminal resident is absent", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE instances SET state='parked' WHERE app_id=$1 AND deployment_id=$2`, app.ID, dep.ID); err != nil {
			t.Fatal(err)
		}
		if _, found, err := backend.ResolveDeploymentSmokeTarget(ctx, app.ID, dep.ID); err != nil || found {
			t.Fatalf("terminal history retained as candidate: found=%t err=%v", found, err)
		}
	})
	t.Log("actual current candidate SQLC lookup, owner/deletion/status filters, durable source gates and lock timeout/recovery; no guest, LISTEN or deployed fleet acceptance")
}
