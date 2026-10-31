// adr: 570
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetPublicationPostgresSyntheticSourcesOutageAndRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "publication-readiness@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "publication-readiness", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: state.DefaultEnvScope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:publication", Status: state.DeployPending, TrafficPercent: 100, OverrideReadinessProbe: []byte(`{"path":"/readyz"}`),
		Sidecars: []byte(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{}}]`)})
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
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	target := gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: instance.ID, NodeID: instance.NodeID, WakeID: instance.WakeID}
	backend := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetReadinessLoader(newTargetReadinessLoader(store)).WithTargetPlacementLoader(newTargetPlacementLoader(store))
	forwards := 0
	adapter := &synthAdapter{store: store, backend: backend, forward: func(gateway.Target) http.Handler {
		forwards++
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}}
	invoke := func(ready bool) {
		t.Helper()
		before := forwards
		inv := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke}
		_, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, inv, target)
		if backend.CapacityCount(app.ID) != 1 || backend.Pick(app.ID).OK != ready {
			t.Fatalf("publication lost readiness/capacity: ready=%t pick=%+v capacity=%d err=%v", ready, backend.Pick(app.ID), backend.CapacityCount(app.ID), err)
		}
		if ready {
			if err != nil || status != http.StatusNoContent || forwards != before+1 {
				t.Fatalf("ready synthetic publication: status=%d forwards=%d/%d err=%v", status, forwards, before, err)
			}
		} else if err == nil || status != 0 || forwards != before {
			t.Fatalf("unready synthetic guest delivery: status=%d forwards=%d/%d err=%v", status, forwards, before, err)
		}
	}
	at := time.Now().UTC().Add(-time.Minute)
	publish := func(source, status string, sequence int) {
		t.Helper()
		body := map[string]string{"app_id": app.ID, "instance_id": target.InstanceID, "wake_id": target.WakeID, "node_id": target.NodeID, "status": status}
		kind := "wake.app_readiness"
		if source == "proxy" {
			kind, body["sidecar_name"] = "wake.sidecar_health", source
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEventAt(ctx, "vmmd", kind, &target.InstanceID, data, at.Add(time.Duration(sequence)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	invoke(false)
	publish("primary", "ready", 1)
	invoke(false)
	publish("proxy", "unready", 2)
	invoke(false)
	publish("proxy", "ready", 3)
	invoke(true)
	publish("primary", "unready", 4)
	invoke(false)
	publish("primary", "ready", 5)
	invoke(true)

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
	if _, err := tx.Exec(ctx, `LOCK TABLE events IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	invoke(false)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	invoke(true)
	if forwards != 3 {
		t.Fatalf("unexpected guest delivery count: %d", forwards)
	}
	t.Log("actual owner/instance rows, configuration and source reads, no LISTEN delivery, SQL timeout and recovery; guest forwarding is a fixture")
}
