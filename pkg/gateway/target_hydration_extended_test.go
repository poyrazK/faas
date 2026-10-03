// adr: 375
package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTargetHydrationCertifiesReadinessBeforeDiscovery(t *testing.T) {
	for _, path := range []string{"reconcile", "refresh", "validate", "at capacity"} {
		for _, kind := range []string{"disabled probe", "ready", "sidecar unready", "store error"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				target := placementFixture("app", "instance")
				now := time.Now()
				state := TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID}
				if kind != "disabled probe" {
					state.RequiredSources = []string{"primary_app", "sidecar:proxy"}
					state.States = map[string]ReadinessState{"primary_app": {Ready: true, UpdatedAt: now, EventID: 1}, "sidecar:proxy": {Ready: kind == "ready", UpdatedAt: now, EventID: 2}}
				}
				failure := errors.New("readiness unavailable")
				var placementDeadline time.Time
				reads := 0
				b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(ctx context.Context, _ []string) (map[string]TargetPlacementSnapshot, error) {
					placementDeadline, _ = ctx.Deadline()
					return map[string]TargetPlacementSnapshot{target.AppID: placementSnapshot(target.AppID, target)}, nil
				}).WithTargetReadinessLoader(func(ctx context.Context, targets []Target) (map[string]TargetReadinessSnapshot, error) {
					reads++
					deadline, ok := ctx.Deadline()
					if !ok || !deadline.Equal(placementDeadline) || len(targets) != 1 || !sameTargetPlacement(targets[0], target) {
						t.Fatalf("readiness lost the shared deadline or scoped identity: deadline=%v targets=%+v", deadline, targets)
					}
					if kind == "store error" {
						return nil, failure
					}
					return map[string]TargetReadinessSnapshot{target.InstanceID: state}, nil
				})
				var err error
				switch path {
				case "reconcile":
					err = b.ReconcileLiveTargets(t.Context(), target.AppID)
				case "refresh":
					err = b.RefreshLiveTargets(t.Context(), target.AppID)
				case "validate":
					b.RecordTarget(target.AppID, target)
					_, err = b.ValidateLiveTarget(t.Context(), target.AppID, target.InstanceID)
				case "at capacity":
					_, _, _, err = b.recordAdmission(t.Context(), target.AppID, target.DeploymentID, "", "", "", "", 0, true, 0)
				}
				wantReady := kind == "disabled probe" || kind == "ready"
				if (err != nil) != (kind == "store error") || (kind == "store error" && !errors.Is(err, failure)) || reads != 1 || b.CapacityCount(target.AppID) != 1 || b.Pick(target.AppID).OK != wantReady || b.ServiceEndpointRoutable(target.AppID, placementEndpoint(target)) != wantReady {
					t.Fatalf("discovery bypassed readiness: reads=%d capacity=%d pick=%+v err=%v", reads, b.CapacityCount(target.AppID), b.Pick(target.AppID), err)
				}
			})
		}
	}
}

func TestTargetHydrationFencesConcurrentPickerChanges(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, change := range []string{"admission", "eviction", "picker replacement", "picker create then delete", "weights"} {
			name := "absent/" + change
			if cached {
				name = "cached/" + change
			}
			t.Run(name, func(t *testing.T) {
				old := placementFixture("app", "instance")
				started, release := make(chan struct{}), make(chan struct{})
				b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(ctx context.Context, _ []string) (map[string]TargetPlacementSnapshot, error) {
					close(started)
					select {
					case <-release:
						return map[string]TargetPlacementSnapshot{old.AppID: placementSnapshot(old.AppID, old)}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})
				if cached {
					b.RecordTarget(old.AppID, old)
				}
				done := make(chan error, 1)
				go func() { done <- b.RefreshLiveTargets(t.Context(), old.AppID) }()
				<-started
				fresh := old
				fresh.WakeID = "replacement"
				switch change {
				case "admission":
					b.RecordTarget(old.AppID, fresh)
				case "eviction":
					b.EvictRoutedTarget(old)
				case "picker replacement":
					b.EvictTarget(old.AppID)
					b.RecordTarget(old.AppID, fresh)
				case "picker create then delete":
					b.RecordTarget(old.AppID, fresh)
					b.EvictTarget(old.AppID)
				case "weights":
					b.WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{{ID: "other", TrafficPercent: 100}}})
					if err := b.RefreshDeploymentWeights(t.Context(), old.AppID); err != nil {
						t.Fatal(err)
					}
				}
				close(release)
				if err := <-done; !errors.Is(err, ErrTargetPlacementChanged) {
					t.Fatalf("concurrent mutation did not fence the read: %v", err)
				}
				if (change == "eviction" || change == "picker create then delete") && b.CapacityCount(old.AppID) != 0 {
					t.Fatal("discarded hydration resurrected an evicted picker")
				}
				if change == "admission" || change == "picker replacement" {
					if pick := b.Pick(old.AppID); !pick.OK || pick.Target.WakeID != fresh.WakeID {
						t.Fatalf("fresh admission was overwritten: %+v", pick)
					}
				}
			})
		}
	}
}

type hydrationObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *hydrationObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func TestTargetHydrationCanceledFollowerDoesNotWaitForLeader(t *testing.T) {
	target := placementFixture("app", "instance")
	started, release := make(chan struct{}), make(chan struct{})
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(ctx context.Context, _ []string) (map[string]TargetPlacementSnapshot, error) {
		close(started)
		select {
		case <-release:
			return map[string]TargetPlacementSnapshot{target.AppID: placementSnapshot(target.AppID, target)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	leader := make(chan error, 1)
	go func() { leader <- b.RefreshLiveTargets(t.Context(), target.AppID) }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := &hydrationObservedContext{Context: ctx, observed: make(chan struct{})}
	follower := make(chan error, 1)
	go func() { follower <- b.ReconcileLiveTargets(observed, target.AppID) }()
	select {
	case <-observed.observed:
	case <-time.After(api.TrafficPlacementReadTimeout / 2):
		t.Fatal("follower never joined the in-flight hydration")
	}
	cancel()
	select {
	case err := <-follower:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(api.TrafficPlacementReadTimeout / 2):
		t.Fatal("canceled follower waited for another request's database read")
	}
	close(release)
	if err := <-leader; err != nil || !b.Pick(target.AppID).OK {
		t.Fatalf("follower cancellation damaged leader: %v", err)
	}
}

func TestTargetHydrationReconcilesMissingCohortBesideHealthyResident(t *testing.T) {
	old := placementFixture("app", "old")
	current := placementFixture("app", "current")
	current.DeploymentID = "second"
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		return map[string]TargetPlacementSnapshot{old.AppID: placementSnapshot(old.AppID, old, current)}, nil
	}).WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{{ID: old.DeploymentID, TrafficPercent: 50}, {ID: current.DeploymentID, TrafficPercent: 50}}})
	b.RecordTarget(old.AppID, old)
	if err := b.RefreshDeploymentWeights(t.Context(), old.AppID); err != nil {
		t.Fatal(err)
	}
	if err := b.ReconcileLiveTargets(t.Context(), old.AppID); err != nil || b.CapacityCount(old.AppID) != 2 || !b.PickForDeployment(old.AppID, current.DeploymentID).OK {
		t.Fatalf("healthy first cohort hid a missed running second cohort: capacity=%d pick=%+v err=%v", b.CapacityCount(old.AppID), b.PickForDeployment(old.AppID, current.DeploymentID), err)
	}
}

func TestTargetHydrationValidationSnapshotDoesNotAliasReadiness(t *testing.T) {
	target, state := repairReadyTarget()
	b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
		return map[string]TargetPlacementSnapshot{target.AppID: placementSnapshot(target.AppID, target)}, nil
	}).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		return map[string]TargetReadinessSnapshot{target.InstanceID: state}, nil
	})
	b.RecordTarget(target.AppID, target)
	snapshot, ok := b.cachedTargetPlacement(target.AppID, target.InstanceID)
	if !ok || !snapshot.routeReady() {
		t.Fatal("ready snapshot was not captured")
	}
	b.SetInstanceReadinessForTarget(target.AppID, target.InstanceID, target.WakeID, target.NodeID, "primary_app", "unready", time.Now(), 10)
	if !snapshot.routeReady() || b.Pick(target.AppID).OK {
		t.Fatal("notification mutated a captured validation snapshot or failed to withdraw the cached target")
	}
	withdrawn, ok := b.cachedTargetPlacement(target.AppID, target.InstanceID)
	if !ok || withdrawn.routeReady() {
		t.Fatal("withdrawn snapshot was not captured")
	}
	withdrawn.ReadinessGates.States["primary_app"] = ReadinessState{Ready: true, UpdatedAt: time.Now(), EventID: 11}
	if b.Pick(target.AppID).OK {
		t.Fatal("a caller's snapshot mutation re-enabled shared routing")
	}
}
