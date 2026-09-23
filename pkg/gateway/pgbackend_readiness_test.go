package gateway

import (
	"testing"
	"time"
)

func TestPGBackendReadinessWithdrawsAndRestoresTarget(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	target := Target{AppID: "app", NodeID: "node", InstanceID: "instance", RequiresReadiness: true}
	b.RecordTarget("app", target)

	if got := b.HealthyCount("app"); got != 0 {
		t.Fatalf("HealthyCount before readiness = %d, want 0", got)
	}
	if got := b.CapacityCount("app"); got != 1 {
		t.Fatalf("CapacityCount before readiness = %d, want 1", got)
	}
	if got := b.Pick("app"); got.OK {
		t.Fatal("Pick routed to an unready target")
	}

	readyAt := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	b.SetInstanceReadiness("app", "instance", "ready", readyAt, 1)
	if got := b.Pick("app"); !got.OK || got.Target.InstanceID != "instance" {
		t.Fatalf("Pick after ready = %+v, want instance", got)
	}

	unreadyAt := readyAt.Add(time.Second)
	b.SetInstanceReadiness("app", "instance", "unready", unreadyAt, 2)
	if got := b.HealthyCount("app"); got != 0 {
		t.Fatalf("HealthyCount after unready = %d, want 0", got)
	}
	if got := b.CapacityCount("app"); got != 1 {
		t.Fatalf("CapacityCount after unready = %d, want 1", got)
	}
	if got := b.Pick("app"); got.OK {
		t.Fatal("Pick routed to a withdrawn target")
	}

	// A delayed notification must not roll the cache back to ready.
	b.SetInstanceReadiness("app", "instance", "ready", readyAt, 1)
	if got := b.Pick("app"); got.OK {
		t.Fatal("stale ready event restored a withdrawn target")
	}

	b.SetInstanceReadiness("app", "instance", "ready", unreadyAt.Add(time.Second), 3)
	if got := b.Pick("app"); !got.OK || got.Target.InstanceID != "instance" {
		t.Fatalf("Pick after recovery = %+v, want instance", got)
	}

	// Even after the short pre-admission event cache expires, an old event may
	// not roll a live target back to a state already superseded in the target.
	b.tgtMu.Lock()
	delete(b.readinessState, staleTargetKey("app", "instance"))
	b.tgtMu.Unlock()
	b.SetInstanceReadiness("app", "instance", "unready", unreadyAt, 2)
	if got := b.Pick("app"); !got.OK {
		t.Fatal("stale event regressed a live target after event-cache expiry")
	}
}

func TestPGBackendReadinessEventBeforeTargetIsApplied(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	b.SetInstanceReadiness("app", "instance", "unready", at, 4)
	b.RecordTarget("app", Target{AppID: "app", NodeID: "node", InstanceID: "instance"})

	if got := b.Pick("app"); got.OK {
		t.Fatal("target added after unready event became routable")
	}
	b.SetInstanceReadiness("app", "instance", "ready", at.Add(time.Second), 5)
	if got := b.Pick("app"); !got.OK {
		t.Fatal("target did not become routable after a newer ready event")
	}
}
