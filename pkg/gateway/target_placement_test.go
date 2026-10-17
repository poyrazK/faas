// adr: 570
package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func placementFixture(app, instance string) Target {
	return Target{AppID: app, InstanceID: instance, NodeID: "node", WakeID: "wake", DeploymentID: "deployment"}
}

func placementEndpoint(target Target) ServiceEndpoint {
	port := target.Port
	if port == 0 {
		port = api.DefaultAppPort
	}
	return ServiceEndpoint{InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, wakeID: target.WakeID, Port: port}
}

func placementSnapshot(app string, targets ...Target) TargetPlacementSnapshot {
	snapshot := TargetPlacementSnapshot{AppID: app, Complete: true}
	for _, target := range targets {
		snapshot.Targets = append(snapshot.Targets, TargetPlacement{Target: target, DeploymentLive: true})
	}
	return snapshot
}

func TestTargetPlacementRepairHotStoppedMovedAndMissedStart(t *testing.T) {
	old := placementFixture("app", "old")
	moved := placementFixture("app", "moved")
	current := moved
	current.NodeID, current.Port = "destination", 9090
	started := placementFixture("app", "new")
	snapshot := placementSnapshot("app", current, started)
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		return map[string]TargetPlacementSnapshot{"app": snapshot}, nil
	})
	b.RecordTarget("app", old)
	b.RecordTarget("app", moved)
	for range 10 {
		b.TouchTarget("app", "old", time.Now())
		b.TouchTarget("app", "moved", time.Now())
	}
	if err := b.ReconcileTargetPlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.CapacityCount("app") != 2 || b.ServiceEndpointRoutable("app", placementEndpoint(moved)) {
		t.Fatal("stopped/moved cache survived authoritative repair")
	}
	for range 12 {
		got := b.Pick("app")
		if !got.OK || (got.Target.InstanceID != started.InstanceID && !sameTargetPlacement(got.Target, current)) {
			t.Fatalf("stale public pick: %+v", got)
		}
	}
	if !b.ServiceEndpointRoutable("app", placementEndpoint(current)) {
		t.Fatal("new placement did not repair managed routing")
	}
	b.RecordTarget("app", moved)
	if b.ServiceEndpointRoutable("app", placementEndpoint(moved)) {
		t.Fatal("delayed old placement rehydrated after repair")
	}
}

func TestTargetPlacementIncompleteRefusesWithoutEvictionAndRecovers(t *testing.T) {
	for _, reason := range []string{"error", "missing", "partial", "wrong owner", "duplicate", "bad node", "overflow"} {
		t.Run(reason, func(t *testing.T) {
			target := placementFixture("app", "instance")
			good := placementSnapshot("app", target)
			bad := good
			var loadErr error
			switch reason {
			case "error":
				loadErr = errors.New("database unavailable")
			case "missing":
				bad = TargetPlacementSnapshot{}
			case "partial":
				bad.Complete = false
			case "wrong owner":
				bad.AppID = "other"
			case "duplicate":
				bad.Targets = append(bad.Targets, bad.Targets[0])
			case "bad node":
				bad.Targets = append([]TargetPlacement(nil), bad.Targets...)
				bad.Targets[0].NodeID = ""
			case "overflow":
				bad.Targets = make([]TargetPlacement, api.TrafficPlacementTargetsPerApp+1)
			}
			loaded := bad
			b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
				return map[string]TargetPlacementSnapshot{"app": loaded}, loadErr
			})
			b.RecordTarget("app", target)
			if err := b.ReconcileTargetPlacements(t.Context()); (err != nil) != (loadErr != nil) {
				t.Fatal(err)
			}
			if b.Pick("app").OK || b.CapacityCount("app") != 1 || b.TargetPlacementRefreshStatus().Succeeded {
				t.Fatal("partial/error evicted or routed resident")
			}
			b.RecordTarget("app", target) // A replay cannot clear a failed verification.
			if b.Pick("app").OK {
				t.Fatal("duplicate publication renewed failed placement")
			}
			loaded, loadErr = good, nil
			if err := b.ReconcileTargetPlacements(t.Context()); err != nil || !b.Pick("app").OK || b.CapacityCount("app") != 1 {
				t.Fatalf("recovery: %v", err)
			}
		})
	}
}

func TestTargetPlacementReadCannotResurrectConcurrentChanges(t *testing.T) {
	for _, change := range []string{"admission", "eviction", "picker replacement", "weights"} {
		t.Run(change, func(t *testing.T) {
			old := placementFixture("app", "instance")
			started, release := make(chan struct{}), make(chan struct{})
			b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
				close(started)
				<-release
				return map[string]TargetPlacementSnapshot{"app": placementSnapshot("app", old)}, nil
			})
			b.RecordTarget("app", old)
			done := make(chan error, 1)
			go func() { done <- b.ReconcileTargetPlacements(t.Context()) }()
			<-started
			fresh := old
			fresh.WakeID = "replacement"
			switch change {
			case "admission":
				b.RecordTarget("app", fresh)
			case "eviction":
				b.EvictRoutedTarget(old)
			case "picker replacement":
				b.EvictTarget("app")
				b.RecordTarget("app", fresh)
			case "weights":
				b.WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{{ID: "other", TrafficPercent: 100}}})
				if err := b.RefreshDeploymentWeights(t.Context(), "app"); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			if err := <-done; err != nil || b.TargetPlacementRefreshStatus().Discarded != 1 {
				t.Fatalf("stale read applied: %v", err)
			}
			if change == "eviction" && b.CapacityCount("app") != 0 {
				t.Fatal("evicted instance resurrected")
			}
			if change == "admission" || change == "picker replacement" {
				if got := b.Pick("app"); !got.OK || got.Target.WakeID != fresh.WakeID {
					t.Fatalf("new admission overwritten: %+v", got)
				}
			}
		})
	}
}

func TestTargetPlacementRetainsKnownRetiredRevisionOnly(t *testing.T) {
	old := placementFixture("app", "old")
	old.DeploymentID = "retired"
	unknown := old
	unknown.InstanceID = "undiscovered-retired"
	current := placementFixture("app", "current")
	snapshot := placementSnapshot("app", old, unknown, current)
	snapshot.Targets[0].DeploymentLive, snapshot.Targets[1].DeploymentLive = false, false
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		return map[string]TargetPlacementSnapshot{"app": snapshot}, nil
	})
	b.RecordTarget("app", old)
	b.RecordTarget("app", current)
	duplicate := old
	duplicate.DeploymentID = "wrong-bucket"
	b.RecordTarget("app", duplicate)
	b.WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{{ID: current.DeploymentID, TrafficPercent: 100}}})
	if err := b.RefreshDeploymentWeights(t.Context(), "app"); err != nil {
		t.Fatal(err)
	}
	if err := b.ReconcileTargetPlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.CapacityCount("app") != 2 || !b.PickForDeployment("app", "retired").OK || b.Pick("app").Target.InstanceID != current.InstanceID {
		t.Fatal("retained revision/cohort accounting changed")
	}
}

func TestTargetPlacementWorkerScansRosterWithoutPerBatchTickDelay(t *testing.T) {
	calls := make(chan time.Time, 3)
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(ctx context.Context, apps []string) (map[string]TargetPlacementSnapshot, error) {
		select {
		case calls <- time.Now():
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		out := map[string]TargetPlacementSnapshot{}
		for _, app := range apps {
			target := placementFixture(app, "instance")
			target.NodeID = "verified"
			out[app] = placementSnapshot(app, target)
		}
		return out, nil
	})
	for i := range 2*api.TrafficPlacementAppBatchSize + 1 {
		app := fmt.Sprintf("roster-%02d", i)
		b.RecordTarget(app, placementFixture(app, "instance"))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); b.RunTargetPlacementReconciler(ctx) }()
	var first, last time.Time
	for i := range 3 {
		select {
		case at := <-calls:
			if i == 0 {
				first = at
			}
			last = at
		case <-time.After(3 * api.TrafficPlacementReconcileInterval):
			t.Fatal("worker did not scan known roster")
		}
	}
	if last.Sub(first) >= api.TrafficPlacementReconcileInterval {
		t.Fatal("worker added a tick delay between batches")
	}
	until := time.Now().Add(time.Second)
	for !b.TargetPlacementRefreshStatus().At.After(last) {
		if time.Now().After(until) {
			t.Fatal("final snapshot did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	for i := range 2*api.TrafficPlacementAppBatchSize + 1 {
		if got := b.Pick(fmt.Sprintf("roster-%02d", i)); !got.OK || got.Target.NodeID != "verified" {
			t.Fatalf("roster app remained stale: %+v", got)
		}
	}
}

func TestTargetPlacementBatchRotationAndIndependentAppFailure(t *testing.T) {
	seen := map[string]bool{}
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(_ context.Context, apps []string) (map[string]TargetPlacementSnapshot, error) {
		if len(apps) > api.TrafficPlacementAppBatchSize {
			t.Fatal("unbounded app input")
		}
		out := map[string]TargetPlacementSnapshot{}
		for _, app := range apps {
			seen[app] = true
			out[app] = placementSnapshot(app, placementFixture(app, "instance"))
		}
		delete(out, "app-00")
		return out, nil
	})
	for i := range 3*api.TrafficPlacementAppBatchSize + 1 {
		app := fmt.Sprintf("app-%02d", i)
		b.RecordTarget(app, placementFixture(app, "instance"))
	}
	for range 4 {
		if err := b.ReconcileTargetPlacements(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 3*api.TrafficPlacementAppBatchSize+1 || b.Pick("app-00").OK || !b.Pick("app-01").OK || b.CapacityCount("app-00") != 1 {
		t.Fatal("rotation/failure isolation did not hold")
	}
}

func TestTargetPlacementDeadlineLeaseAndWorkerCancellation(t *testing.T) {
	target := placementFixture("app", "instance")
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(ctx context.Context, _ []string) (map[string]TargetPlacementSnapshot, error) {
		<-ctx.Done()
		return map[string]TargetPlacementSnapshot{"app": placementSnapshot("app", target)}, nil // Late success is invalid.
	})
	b.RecordTarget("app", target)
	if err := b.ReconcileTargetPlacements(t.Context()); !errors.Is(err, context.DeadlineExceeded) || b.Pick("app").OK || b.CapacityCount("app") != 1 {
		t.Fatalf("late success: %v", err)
	}
	b.tgtMu.Lock()
	b.appsPicker["app"].sets["deployment"].entries[0].PlacementUnavailable = false
	b.appsPicker["app"].sets["deployment"].entries[0].PlacementVerifiedUntil = time.Now().Add(-time.Second)
	b.tgtMu.Unlock()
	b.TouchTarget("app", target.InstanceID, time.Now())
	if b.Pick("app").OK || b.ServiceEndpointRoutable("app", placementEndpoint(target)) {
		t.Fatal("hot traffic renewed placement lease")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); b.RunTargetPlacementReconciler(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("placement worker did not join cancellation")
	}
}

func TestTargetPlacementPreservesNewerReadinessAndRefusesFailedRead(t *testing.T) {
	target, ready := repairReadyTarget()
	target.WakeID = "wake"
	ready.WakeID, ready.NodeID = target.WakeID, target.NodeID
	started, release := make(chan struct{}), make(chan struct{})
	var readErr error
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		if started != nil {
			close(started)
			<-release
		}
		return map[string]TargetPlacementSnapshot{"app": placementSnapshot("app", target)}, nil
	}).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		return map[string]TargetReadinessSnapshot{target.InstanceID: ready}, readErr
	})
	b.RecordTarget("app", target)
	done := make(chan error, 1)
	go func() { done <- b.ReconcileTargetPlacements(t.Context()) }()
	<-started
	b.SetInstanceReadinessForTarget("app", target.InstanceID, target.WakeID, target.NodeID, "primary_app", "unready", ready.States["primary_app"].UpdatedAt.Add(time.Second), 10)
	close(release)
	if err := <-done; err != nil || b.Pick("app").OK {
		t.Fatalf("older repair erased concurrent withdrawal: %v", err)
	}
	started = nil
	readErr = errors.New("readiness unavailable")
	if err := b.ReconcileTargetPlacements(t.Context()); !errors.Is(err, readErr) || b.Pick("app").OK || b.CapacityCount("app") != 1 {
		t.Fatalf("partial readiness success accepted: %v", err)
	}
	readErr = nil
	state := ready.States["primary_app"]
	state.UpdatedAt, state.EventID = state.UpdatedAt.Add(2*time.Second), 11
	ready.States["primary_app"] = state
	if err := b.ReconcileTargetPlacements(t.Context()); err != nil || !b.Pick("app").OK {
		t.Fatalf("readiness recovery: %v", err)
	}
}
