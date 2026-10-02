// adr: 375
package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func repairReadyTarget() (Target, TargetReadinessSnapshot) {
	at := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	states := map[string]ReadinessState{
		"primary_app":   {Ready: true, UpdatedAt: at, EventID: 1},
		"sidecar:proxy": {Ready: true, UpdatedAt: at, EventID: 2},
	}
	target := Target{AppID: "app", InstanceID: "instance", DeploymentID: "deployment", NodeID: "node", RequiresReadiness: true,
		ReadinessGates: &ReadinessGates{RequiredSources: []string{"primary_app", "sidecar:proxy"}, States: states}}
	snapshot := TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID,
		RequiredSources: append([]string(nil), target.ReadinessGates.RequiredSources...), States: cloneReadinessGates(target.ReadinessGates).States}
	return target, snapshot
}

func assertRepairRoutability(t *testing.T, backend *PGBackend, ready bool) {
	t.Helper()
	snapshot, err := backend.ServiceEndpoints(t.Context(), "app")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	if ready {
		want = 1
	}
	endpoint := ServiceEndpoint{InstanceID: "instance", NodeID: "node", DeploymentID: "deployment", Port: 8080}
	if backend.Pick("app").OK != ready || backend.HealthyCount("app") != want || len(snapshot.Endpoints) != want ||
		backend.ServiceEndpointRoutable("app", endpoint) != ready || backend.CapacityCount("app") != 1 {
		t.Fatalf("readiness routing differs: ready=%t healthy=%d endpoints=%+v capacity=%d", ready, backend.HealthyCount("app"), snapshot, backend.CapacityCount("app"))
	}
}

func TestTargetReadinessRepairRefusesAndRecoversResident(t *testing.T) {
	for _, reason := range []string{"store failure", "missing result", "wrong owner", "wrong instance", "wrong deployment", "missing source", "zero observation", "primary unready", "sidecar unready"} {
		t.Run(reason, func(t *testing.T) {
			target, good := repairReadyTarget()
			bad := good
			bad.States = cloneReadinessGates(target.ReadinessGates).States
			var readErr error
			switch reason {
			case "store failure":
				readErr = errors.New("store unavailable")
			case "missing result":
				bad = TargetReadinessSnapshot{}
			case "wrong owner":
				bad.AppID = "other"
			case "wrong instance":
				bad.InstanceID = "other"
			case "wrong deployment":
				bad.DeploymentID = "other"
			case "missing source":
				delete(bad.States, "sidecar:proxy")
			case "zero observation":
				bad.States["primary_app"] = ReadinessState{Ready: true}
			default:
				source := "primary_app"
				if reason == "sidecar unready" {
					source = "sidecar:proxy"
				}
				current := bad.States[source]
				bad.States[source] = ReadinessState{UpdatedAt: current.UpdatedAt.Add(time.Second), EventID: 3}
				good.States[source] = ReadinessState{Ready: true, UpdatedAt: current.UpdatedAt.Add(2 * time.Second), EventID: 4}
			}
			loaded := bad
			backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
				return map[string]TargetReadinessSnapshot{"instance": loaded}, readErr
			})
			backend.RecordTarget("app", target)
			assertRepairRoutability(t, backend, true)
			if err := backend.ReconcileTargetReadiness(t.Context()); (err != nil) != (readErr != nil) {
				t.Fatalf("repair error=%v store error=%v", err, readErr)
			}
			assertRepairRoutability(t, backend, false)
			loaded, readErr = good, nil
			if err := backend.ReconcileTargetReadiness(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertRepairRoutability(t, backend, true)
		})
	}
}

func TestTargetReadinessRepairPreservesNewerNotification(t *testing.T) {
	target, old := repairReadyTarget()
	started, release := make(chan struct{}), make(chan struct{})
	backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		close(started)
		<-release
		return map[string]TargetReadinessSnapshot{"instance": old}, nil
	})
	backend.RecordTarget("app", target)
	done := make(chan error, 1)
	go func() { done <- backend.ReconcileTargetReadiness(t.Context()) }()
	<-started
	backend.SetInstanceReadinessSource("app", "instance", "primary_app", "unready", old.States["primary_app"].UpdatedAt.Add(time.Second), 4)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertRepairRoutability(t, backend, false)
	backend.SetInstanceReadinessSource("app", "instance", "primary_app", "ready", old.States["primary_app"].UpdatedAt, 100)
	assertRepairRoutability(t, backend, false)
}

func TestTargetReadinessRepairDoesNotChangeReplacedOrEvictedTarget(t *testing.T) {
	for _, action := range []string{"replace", "evict"} {
		t.Run(action, func(t *testing.T) {
			target, stale := repairReadyTarget()
			started, release := make(chan struct{}), make(chan struct{})
			backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
				close(started)
				<-release
				return map[string]TargetReadinessSnapshot{"instance": stale}, nil
			})
			backend.RecordTarget("app", target)
			done := make(chan error, 1)
			go func() { done <- backend.ReconcileTargetReadiness(t.Context()) }()
			<-started
			if action == "evict" {
				backend.EvictInstance("app", "instance")
			} else {
				target.ReadinessGates.States["primary_app"] = ReadinessState{UpdatedAt: stale.States["primary_app"].UpdatedAt.Add(time.Second), EventID: 3}
				backend.RecordTarget("app", target)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if backend.Pick("app").OK || backend.HealthyCount("app") != 0 || backend.TargetReadinessRefreshStatus().Checked != 0 {
				t.Fatal("stale verification changed a replaced or evicted target")
			}
			want := 1
			if action == "evict" {
				want = 0
			}
			if backend.CapacityCount("app") != want {
				t.Fatal("stale verification changed resident capacity")
			}
		})
	}
}

func TestTargetReadinessRepairLeaseExpiresOnRoutingPath(t *testing.T) {
	target, snapshot := repairReadyTarget()
	backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		return map[string]TargetReadinessSnapshot{"instance": snapshot}, nil
	})
	target.ReadinessVerifiedUntil = time.Now().Add(-time.Second)
	backend.RecordTarget("app", target)
	assertRepairRoutability(t, backend, false)
	if err := backend.ReconcileTargetReadiness(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertRepairRoutability(t, backend, true)
}

func TestTargetReadinessRepairRecoversVerifiedDisabledProbe(t *testing.T) {
	target, _ := repairReadyTarget()
	target.RequiresReadiness, target.ReadinessGates, target.ReadinessUnavailable = false, nil, true
	reads := 0
	backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		reads++
		return map[string]TargetReadinessSnapshot{"instance": {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID}}, nil
	})
	backend.RecordTarget("app", target)
	assertRepairRoutability(t, backend, false)
	for range 2 {
		if err := backend.ReconcileTargetReadiness(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	assertRepairRoutability(t, backend, true)
	if reads != 1 {
		t.Fatal("verified disabled probe remained dependent on recurring reads")
	}
}

func TestTargetReadinessRepairFairBoundedBatchesAndDisabledProbes(t *testing.T) {
	seen, calls := make(map[string]bool), 0
	backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(_ context.Context, targets []Target) (map[string]TargetReadinessSnapshot, error) {
		calls++
		if len(targets) == 0 || len(targets) > api.TrafficReadinessBatchSize {
			t.Fatalf("unbounded or empty batch=%d", len(targets))
		}
		out := make(map[string]TargetReadinessSnapshot, len(targets))
		for _, target := range targets {
			if target.InstanceID == "no-probe" {
				t.Fatal("verified disabled probe gained an ongoing store dependency")
			}
			seen[target.InstanceID] = true
			out[target.InstanceID] = TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID,
				RequiredSources: target.ReadinessGates.RequiredSources, States: target.ReadinessGates.States}
		}
		return out, nil
	})
	for i := range 2*api.TrafficReadinessBatchSize + 1 {
		target, _ := repairReadyTarget()
		target.AppID, target.InstanceID, target.DeploymentID = fmt.Sprintf("app-%d", i%3), fmt.Sprintf("instance-%03d", i), fmt.Sprintf("deployment-%d", i%2)
		backend.RecordTarget(target.AppID, target)
	}
	backend.RecordTarget("app", Target{AppID: "app", InstanceID: "no-probe", DeploymentID: "deployment", NodeID: "node"})
	for range 3 {
		if err := backend.ReconcileTargetReadiness(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || len(seen) != 2*api.TrafficReadinessBatchSize+1 || !backend.Pick("app").OK {
		t.Fatalf("repair omitted targets or disabled probes: calls=%d seen=%d", calls, len(seen))
	}
}

func TestTargetReadinessRepairDeadlineAndStop(t *testing.T) {
	t.Run("late read success", func(t *testing.T) {
		target, snapshot := repairReadyTarget()
		backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(ctx context.Context, _ []Target) (map[string]TargetReadinessSnapshot, error) {
			<-ctx.Done()
			return map[string]TargetReadinessSnapshot{"instance": snapshot}, nil
		})
		backend.RecordTarget("app", target)
		if err := backend.ReconcileTargetReadiness(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired read accepted: %v", err)
		}
		assertRepairRoutability(t, backend, false)
	})
	for _, running := range []string{"repair", "admission"} {
		t.Run(running+" canceled", func(t *testing.T) {
			target, _ := repairReadyTarget()
			started := make(chan struct{})
			backend := NewPGBackend(nil, nil, nil).WithTargetReadinessLoader(func(ctx context.Context, _ []Target) (map[string]TargetReadinessSnapshot, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			if running == "repair" {
				backend.RecordTarget("app", target)
				go func() { backend.RunTargetReadinessReconciler(ctx); done <- nil }()
			} else {
				go func() { done <- backend.loadAdmissionReadiness(ctx, &target) }()
			}
			<-started
			cancel()
			select {
			case err := <-done:
				if running == "admission" && (!errors.Is(err, context.Canceled) || !target.ReadinessUnavailable) {
					t.Fatalf("admission canceled without refusal: target=%+v err=%v", target, err)
				}
			case <-time.After(time.Second):
				t.Fatal("readiness read outlived cancellation")
			}
		})
	}
}
