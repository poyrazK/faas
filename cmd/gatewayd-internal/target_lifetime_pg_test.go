// adr: 375
package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetLifetimeReadinessCannotCertifyDifferentWakeOrNode(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "target-lifetime@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "target-lifetime", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:lifetime", Status: state.DeployPending, TrafficPercent: 100, OverrideReadinessProbe: []byte(`{"path":"/readyz"}`)})
	if err != nil {
		t.Fatal(err)
	}
	target := gateway.Target{AppID: app.ID, DeploymentID: dep.ID, InstanceID: "reused-instance", NodeID: "current-node", WakeID: "current-wake", RequiresReadiness: true}
	b := gateway.NewPGBackend(nil, nil, discardLogger()).WithTargetReadinessLoader(newTargetReadinessLoader(store))
	b.RecordTarget(app.ID, target)
	for _, identity := range []struct{ wake, node string }{
		{"old-wake", "current-node"}, {"current-wake", "old-node"}, {"", ""}, {"current-wake", "current-node"},
	} {
		body, err := json.Marshal(map[string]string{"app_id": app.ID, "instance_id": target.InstanceID, "wake_id": identity.wake, "node_id": identity.node, "status": "ready"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(t.Context(), "vmmd", "wake.app_readiness", nil, body); err != nil {
			t.Fatal(err)
		}
		if err := b.ReconcileTargetReadiness(t.Context()); err != nil {
			t.Fatal(err)
		}
		want := identity.wake == target.WakeID && identity.node == target.NodeID
		if got := b.Pick(app.ID); got.OK != want || b.CapacityCount(app.ID) != 1 {
			t.Fatalf("readiness from wake=%q node=%q routed=%t want=%t capacity=%d", identity.wake, identity.node, got.OK, want, b.CapacityCount(app.ID))
		}
	}
}

func TestTargetLifetimeDelayedTerminalDoesNotEvictNewWake(t *testing.T) {
	b := gateway.NewPGBackend(nil, nil, discardLogger())
	b.RecordTarget("app", gateway.Target{AppID: "app", DeploymentID: "deployment", InstanceID: "instance", NodeID: "node", WakeID: "current"})
	handleInvalidation(t.Context(), b, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"app_id":"app","instance_id":"instance","wake_id":"old","state":"parked"}`}, discardLogger())
	if !b.Pick("app").OK || b.CapacityCount("app") != 1 {
		t.Fatal("delayed terminal event evicted the current wake")
	}
	handleInvalidation(t.Context(), b, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"app_id":"app","instance_id":"instance","wake_id":"current","state":"parked"}`}, discardLogger())
	if b.Pick("app").OK || b.CapacityCount("app") != 0 {
		t.Fatal("current terminal event did not withdraw target")
	}
}

func TestTargetLifetimePostgresNotificationIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	listener, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(t.Context(), "LISTEN instance_readiness_changed"); err != nil {
		t.Fatal(err)
	}
	b := gateway.NewPGBackend(nil, nil, discardLogger())
	b.RecordTarget("app", gateway.Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", WakeID: "wake", NodeID: "node", RequiresReadiness: true, ReadinessGates: &gateway.ReadinessGates{RequiredSources: []string{"primary_app", "sidecar:proxy"}}})
	for _, event := range []struct {
		wake, node, sidecar, status string
		ready                       bool
	}{
		{"wake", "node", "", "ready", false},
		{"wake", "node", "proxy", "ready", true},
		{"old", "node", "", "unready", true},
		{"wake", "old-node", "proxy", "unready", true},
		{"wake", "node", "", "unready", false},
	} {
		kind := "wake.app_readiness"
		if event.sidecar != "" {
			kind = "wake.sidecar_health"
		}
		body, err := json.Marshal(map[string]string{"app_id": "app", "instance_id": "instance", "wake_id": event.wake, "node_id": event.node, "sidecar_name": event.sidecar, "status": event.status})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(t.Context(), "vmmd", kind, nil, body); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		n, err := listener.Conn().WaitForNotification(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			WakeID string `json:"wake_id"`
			NodeID string `json:"node_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &payload); err != nil || payload.WakeID != event.wake || payload.NodeID != event.node {
			t.Fatalf("trigger identity=%+v err=%v", payload, err)
		}
		handleInvalidation(t.Context(), b, db.Notification{Channel: n.Channel, Payload: n.Payload}, discardLogger())
		if b.Pick("app").OK != event.ready || b.CapacityCount("app") != 1 {
			t.Fatalf("notification=%+v ready=%t capacity=%d", event, b.Pick("app").OK, b.CapacityCount("app"))
		}
	}
}

func TestTargetLifetimeDelayedMigrationNotificationDoesNotEvictCurrentOwner(t *testing.T) {
	b := gateway.NewPGBackend(nil, nil, discardLogger())
	b.RecordTarget("app", gateway.Target{AppID: "app", InstanceID: "instance", NodeID: "destination", WakeID: "wake", DeploymentID: "deployment"})
	handleInvalidation(t.Context(), b, db.Notification{Channel: db.NotifyInstanceChanged, Payload: `{"app_id":"app","instance_id":"instance","wake_id":"wake","node_id":"source","state":"migrating"}`}, discardLogger())
	if !b.Pick("app").OK || b.CapacityCount("app") != 1 {
		t.Fatal("delayed migration source event evicted destination")
	}
}
