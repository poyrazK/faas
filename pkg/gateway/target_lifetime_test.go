// adr: 570
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTargetLifetimeWarmForwardFailurePreservesRoutingWake(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	old := Target{AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "old", DeploymentID: "deployment"}
	b.RecordTarget(old.AppID, old)
	h := &Handler{}
	_, forwarded := h.armWakeFirstByte(httptest.NewRequest(http.MethodGet, "http://app.example/", nil), old.AppID, old, "")
	if forwarded.WakeID != "" {
		t.Fatal("warm request reused first-byte telemetry")
	}
	b.EvictRoutedTarget(forwarded)
	b.RecordTarget(old.AppID, old)
	if b.Pick(old.AppID).OK || b.CapacityCount(old.AppID) != 0 {
		t.Fatal("warm transport failure did not quarantine the selected wake")
	}
	fresh := old
	fresh.WakeID = "replacement"
	b.RecordTarget(fresh.AppID, fresh)
	if got := b.Pick(fresh.AppID); !got.OK || got.Target.WakeID != fresh.WakeID {
		t.Fatalf("old warm forward quarantined replacement: %+v", got)
	}
}

func TestTargetLifetimeFreshWakeBypassesOldQuarantine(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	old := Target{AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "old", DeploymentID: "deployment"}
	b.RecordTarget("app", old)
	b.EvictInstance("app", "instance")
	b.RecordTarget("app", old)
	if b.CapacityCount("app") != 0 {
		t.Fatal("evicted wake was reinserted")
	}
	unknown := old
	unknown.WakeID = ""
	b.RecordTarget("app", unknown)
	if b.CapacityCount("app") != 0 {
		t.Fatal("missing wake identity bypassed known quarantine")
	}
	fresh := old
	fresh.WakeID = "fresh"
	b.RecordTarget("app", fresh)
	if got := b.Pick("app"); !got.OK || got.Target.WakeID != "fresh" || b.CapacityCount("app") != 1 {
		t.Fatalf("fresh wake blocked by old quarantine: %+v capacity=%d", got, b.CapacityCount("app"))
	}
}

func TestTargetLifetimeNotificationsAndForwardFailuresAreScoped(t *testing.T) {
	old := Target{AppID: "app", InstanceID: "instance", NodeID: "old-node", WakeID: "old", DeploymentID: "deployment"}
	fresh := old
	fresh.NodeID, fresh.WakeID = "new-node", "fresh"
	for _, retire := range []struct {
		name string
		fn   func(*PGBackend)
	}{
		{"terminal", func(b *PGBackend) { b.EvictInstanceForWake("app", "instance", "old") }},
		{"forward", func(b *PGBackend) { evictStaleTarget(b, "app", old) }},
	} {
		t.Run(retire.name, func(t *testing.T) {
			b := NewPGBackend(nil, nil, nil)
			b.RecordTarget("app", fresh)
			retire.fn(b)
			b.RecordTarget("app", old)
			if got := b.Pick("app"); !got.OK || got.Target.WakeID != "fresh" || b.CapacityCount("app") != 1 {
				t.Fatalf("old lifetime retired/replaced fresh target: %+v", got)
			}
			b.EvictRoutedTarget(fresh)
			if b.Pick("app").OK || b.CapacityCount("app") != 0 {
				t.Fatal("current forward failure did not withdraw target")
			}
		})
	}
}

func TestTargetLifetimeReadinessCacheAndEndpointLeaseDoNotCrossWake(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	at := time.Now()
	b.SetInstanceReadinessForTarget("app", "instance", "old", "node", "primary_app", "ready", at, 10)
	target := Target{AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "fresh", DeploymentID: "deployment", RequiresReadiness: true, ReadinessGates: &ReadinessGates{RequiredSources: []string{"primary_app"}}}
	b.RecordTarget("app", target)
	if b.Pick("app").OK {
		t.Fatal("pre-admission ready event crossed wake identity")
	}
	b.SetInstanceReadinessForTarget("app", "instance", "fresh", "node", "primary_app", "ready", at.Add(-time.Second), 1)
	if !b.Pick("app").OK {
		t.Fatal("current wake could not recover after newer old event")
	}
	snapshot, err := b.ServiceEndpoints(t.Context(), "app")
	if err != nil || len(snapshot.Endpoints) != 1 {
		t.Fatalf("endpoint snapshot=%+v err=%v", snapshot, err)
	}
	oldLease := snapshot.Endpoints[0]
	for _, identity := range []struct{ wake, node string }{{"old", "node"}, {"fresh", "other"}, {"", ""}} {
		b.SetInstanceReadinessForTarget("app", "instance", identity.wake, identity.node, "primary_app", "unready", at.Add(time.Minute), 99)
		if !b.Pick("app").OK {
			t.Fatalf("wrong wake/node notification withdrew target: %+v", identity)
		}
	}
	target.WakeID = "next"
	b.RecordTarget("app", target)
	b.SetInstanceReadinessForTarget("app", "instance", "next", "node", "primary_app", "ready", at, 2)
	if !b.Pick("app").OK || b.ServiceEndpointRoutable("app", oldLease) {
		t.Fatal("old managed lease was accepted for a replacement wake")
	}
}

func TestTargetLifetimeReadinessSnapshotEchoesWakeAndNode(t *testing.T) {
	for _, identity := range []struct{ wake, node string }{{"old", "node"}, {"fresh", "old-node"}, {"", ""}, {"fresh", "node"}} {
		target := Target{AppID: "app", InstanceID: "instance", NodeID: "node", WakeID: "fresh", DeploymentID: "deployment"}
		snapshot := TargetReadinessSnapshot{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", WakeID: identity.wake, NodeID: identity.node}
		want := identity.wake == target.WakeID && identity.node == target.NodeID
		if got := applyTargetReadiness(&target, snapshot, time.Now()); got != want || target.routeReady() != want {
			t.Fatalf("snapshot identity=%+v accepted=%t ready=%t", identity, got, target.routeReady())
		}
	}
}

func TestTargetLifetimeMigrationWithdrawalUsesNode(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	current := Target{AppID: "app", InstanceID: "instance", NodeID: "destination", WakeID: "wake", DeploymentID: "deployment"}
	b.RecordTarget("app", current)
	b.EvictInstanceForRoutingIdentity("app", "instance", "wake", "source")
	old := current
	old.NodeID = "source"
	b.RecordTarget("app", old)
	if got := b.Pick("app"); !got.OK || got.Target.NodeID != "destination" {
		t.Fatalf("old migration node evicted/replaced current owner: %+v", got)
	}
	b.EvictInstanceForRoutingIdentity("app", "instance", "wake", "destination")
	if b.Pick("app").OK || b.CapacityCount("app") != 0 {
		t.Fatal("current owner withdrawal failed")
	}
}
