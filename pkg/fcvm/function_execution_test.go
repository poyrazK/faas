// adr: 20261018030611
package fcvm

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

func functionExecutionManager(t *testing.T) (*Manager, Lease, *activity.ActivityTracker) {
	t.Helper()
	m := newFreezeManager(t, &freezingVMM{fakeVMM: &fakeVMM{}})
	m.mu.Lock()
	inst := m.live["f"]
	inst.Runtime, inst.Lease.Plan = "node22", api.PlanFree
	lease := inst.Lease
	m.mu.Unlock()
	a := activity.NewWithDefaults()
	m.WithFunctionExecutionActivity(a).WithFreeze(FreezeConfig{Activity: a})
	return m, lease, a
}

func executionRequest(kind string, sequence uint64) functionlifetime.Request {
	r := functionlifetime.Request{Version: functionlifetime.Version, Owner: 1, Kind: kind, Sequence: sequence}
	if kind == functionlifetime.Begin {
		r.BudgetMS = 30_000
	}
	return r
}

func TestFunctionResourceCaptureAndThawGate(t *testing.T) {
	m, _, _ := functionExecutionManager(t)
	if functionlifetime.NativeNode("node22") {
		for _, mode := range []snapshotCaptureMode{snapshotTerminal, snapshotMigration} {
			for _, before := range []bool{false, true} {
				if before && mode == snapshotTerminal {
					continue
				}
				_, _, _, _, err := m.beginSnapshotFlight(t.Context(), "f", SnapshotSpec{BeforeCheckpoint: before}, mode)
				if !errors.Is(err, ErrInstanceBusy) {
					t.Fatalf("capture before=%v mode=%v: %v", before, mode, err)
				}
			}
		}
	}
	_, _, _, flight, err := m.beginSnapshotFlight(t.Context(), "f", SnapshotSpec{BeforeCheckpoint: true}, snapshotTerminal)
	if err != nil {
		t.Fatal(err)
	}
	m.finishInstanceFlight("f", flight)
	v := m.vmm.(*freezingVMM)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	hooks := len(v.resumeHookCalls)
	if err := m.Thaw(t.Context(), "f"); err != nil {
		t.Fatal(err)
	}
	want := 0
	if functionlifetime.NativeNode("node22") {
		want = 1
	}
	if got := len(v.resumeHookCalls) - hooks; got != want {
		t.Fatalf("automatic resource thaw hooks=%d want=%d", got, want)
	}
}

func TestFunctionExecutionAcknowledgementsAndLateFrames(t *testing.T) {
	m, lease, a := functionExecutionManager(t)
	now := time.Now()
	begin := executionRequest(functionlifetime.Begin, 1)
	for range 2 {
		if got := m.applyFunctionExecution(lease, begin, now); got.Status != functionlifetime.OK || got.Plan != api.PlanFree {
			t.Fatalf("begin: %+v", got)
		}
	}
	if n, _ := a.Inflight("f"); n != 1 {
		t.Fatalf("lost ACK duplicated ownership: %d", n)
	}
	for range 2 {
		m.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1), now)
	}
	if n, _ := a.Inflight("f"); n != 0 {
		t.Fatal("completion was not idempotent")
	}
	if got := m.applyFunctionExecution(lease, begin, now); got.Status != functionlifetime.Stale {
		t.Fatal("replayed a completed reservation")
	}
	// A cancellation tombstone precedes an uncertain delayed registration.
	m.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 3), now)
	if got := m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 3), now); got.Status != functionlifetime.Stale {
		t.Fatal("late registration resurrected a cancelled execution")
	}
	old := lease
	m.mu.Lock()
	m.live["f"].Lease.processGeneration++
	lease = m.live["f"].Lease
	m.mu.Unlock()
	if got := m.applyFunctionExecution(old, executionRequest(functionlifetime.Begin, 4), now); got.Status != functionlifetime.Stale {
		t.Fatal("old VM generation mutated the replacement")
	}
	if got := m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 4), now); got.Status != functionlifetime.OK {
		t.Fatalf("current generation refused: %+v", got)
	}
	m.applyFunctionExecution(old, executionRequest(functionlifetime.Finish, 4), now)
	if n, _ := a.Inflight("f"); n != 1 {
		t.Fatal("stale completion released a live execution")
	}
}

func TestFunctionExecutionBlocksEveryPausePath(t *testing.T) {
	m, lease, a := functionExecutionManager(t)
	m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1), time.Now())
	if _, err := m.Park(t.Context(), "f", SnapshotSpec{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("park with execution: %v", err)
	}
	if _, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("warm with execution: %v", err)
	}
	if _, err := m.SnapshotKeepAlive(t.Context(), "f", SnapshotSpec{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("migration with execution: %v", err)
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err == nil {
		t.Fatal("froze active background execution")
	}
	if err := m.functionPauseBarrier("f"); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("final pause barrier ignored execution")
	}
	m.mu.Lock()
	eligible := m.quietEligibleLocked("f", m.live["f"])
	m.mu.Unlock()
	if eligible {
		t.Fatal("quiet pause ignored execution")
	}
	if n, _ := a.Inflight("f"); n != 1 || m.LiveCount() != 1 {
		t.Fatal("busy refusal released VM ownership")
	}
	m.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1), time.Now())
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	// Capture can resume a qualified pre-frozen function only while its
	// original flight keeps ordinary execution registration closed.
	ctx, _, _, flight, err := m.beginSnapshotFlight(t.Context(), "f", SnapshotSpec{BeforeCheckpoint: true}, snapshotTerminal)
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceFlight("f", flight)
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if got := m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 2), time.Now()); got.Status != functionlifetime.Busy {
		t.Fatal("registration entered a capture flight")
	}
}

func TestFunctionExecutionCheckpointHookAdmissionSealsAtomically(t *testing.T) {
	m, lease, _ := functionExecutionManager(t)
	_, _, _, flight, err := m.beginSnapshotFlight(t.Context(), "f", SnapshotSpec{BeforeCheckpoint: true}, snapshotTerminal)
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceFlight("f", flight)
	if got := m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1), time.Now()); got.Status != functionlifetime.Busy {
		t.Fatal("ordinary request entered checkpoint")
	}
	hook := executionRequest(functionlifetime.Begin, 2)
	hook.Hook = "before_checkpoint"
	if got := m.applyFunctionExecution(lease, hook, time.Now()); got.Status != functionlifetime.OK {
		t.Fatalf("owned hook rejected: %+v", got)
	}
	if err := m.functionPauseBarrier("f"); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("capture passed incomplete hook cleanup")
	}
	hook.Sequence = 3
	if got := m.applyFunctionExecution(lease, hook, time.Now()); got.Status != functionlifetime.Busy {
		t.Fatal("hook registered after final barrier sealed admission")
	}
	m.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 2), time.Now())
	if err := m.functionPauseBarrier("f"); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionExecutionRunnerRestartAndWatchdogRetire(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "runner-restart"}[restart], func(t *testing.T) {
			m, lease, a := functionExecutionManager(t)
			now := time.Now()
			m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1), now)
			if restart {
				request := executionRequest(functionlifetime.Begin, 1)
				request.Owner = 2
				if got := m.applyFunctionExecution(lease, request, now); got.Status != functionlifetime.Stale {
					t.Fatal("new runner acknowledged orphaned interpreter work")
				}
			} else {
				now = now.Add(api.FunctionExecutionLeaseDuration(api.PlanFree, 30*time.Second) + time.Second)
			}
			m.WithLivenessSink(func(ctx context.Context, id, reason string) {
				if id != "f" || reason != LivenessReasonInfrastructure || m.LiveCount() != 0 {
					t.Error("reported retirement before physical cleanup")
				}
				// The scheduler's synchronous destroy must join completed
				// teardown, while replacement admission is still fenced.
				if err := m.Destroy(ctx, id); err != nil {
					t.Error(err)
				}
				if _, _, err := m.beginInstanceBoot(ctx, id); err == nil {
					t.Error("replacement admitted before ownership notification completed")
				}
			})
			m.functionExecutionWatchdogPass(t.Context(), now)
			if m.LiveCount() != 0 {
				t.Fatal("expired ownership did not retire its VM")
			}
			if n, seen := a.Inflight("f"); n != 0 || seen {
				t.Fatal("retired VM retained execution occupancy")
			}
		})
	}
}

func TestFunctionExecutionRestartNeverAdoptsUnprovenOwnership(t *testing.T) {
	m, lease, _ := functionExecutionManager(t)
	m.applyFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1), time.Now())
	// A fresh manager has no live incarnation proof. Native recovery keeps
	// pre-existing processes quarantined rather than treating count loss as idle.
	fresh := newTestManager(&fakeRunner{}, &fakeVMM{})
	fresh.WithFunctionExecutionActivity(activity.NewWithDefaults())
	if got := fresh.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1), time.Now()); got.Status != functionlifetime.Stale {
		t.Fatal("restart adopted an unproven VM")
	}
}

func TestFunctionExecutionStartupOwnershipIsNotAnIdleWindow(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	a := activity.NewWithDefaults()
	m.WithFunctionExecutionActivity(a)
	ctx, flight, err := m.beginInstanceBoot(t.Context(), "starting")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceFlight("starting", flight)
	lease := m.beginProcessAttempt(Lease{Instance: "starting", Slot: 1, Plan: api.PlanFree})
	m.beginFunctionExecutionBoot(lease, WakeRequest{Runtime: "node22"})
	if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1)); got.Status != functionlifetime.OK {
		t.Fatal("startup handler could not acquire boot-scoped ownership")
	}
	if err := m.functionPauseBarrier("starting"); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("startup lease was invisible to capture")
	}
	m.functionExecutionWatchdogPass(t.Context(), time.Now().Add(time.Hour))
	if ctx.Err() == nil {
		t.Fatal("expired startup ownership did not cancel its boot")
	}
	if n, _ := a.Inflight("starting"); n != 1 {
		t.Fatal("deadline released unconfirmed boot cleanup")
	}
}

func TestFunctionExecutionLostAcknowledgementRetainsOwnership(t *testing.T) {
	m, lease, a := functionExecutionManager(t)
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() { _, _ = m.serveFunctionExecution(lease, server); _ = server.Close(); close(done) }()
	if err := functionlifetime.Write(client, executionRequest(functionlifetime.Begin, 1)); err != nil {
		t.Fatal(err)
	}
	_ = client.Close() // registration reached vmmd; its reply was lost
	<-done
	if n, _ := a.Inflight("f"); n != 1 {
		t.Fatal("transport EOF released an unconfirmed execution")
	}
	if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1)); got.Status != functionlifetime.OK {
		t.Fatalf("same reservation retry: %+v", got)
	}
	if n, _ := a.Inflight("f"); n != 1 {
		t.Fatal("retry counted the same execution twice")
	}
	if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1)); got.Status != functionlifetime.OK {
		t.Fatal("explicit completion was not acknowledged")
	}
}

func TestFunctionExecutionAdmissionAndFreezeCannotBothWin(t *testing.T) {
	for range 20 {
		m, lease, _ := functionExecutionManager(t)
		start := make(chan struct{})
		began := make(chan functionlifetime.Reply, 1)
		frozen := make(chan error, 1)
		go func() {
			<-start
			began <- m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1))
		}()
		go func() { <-start; _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); frozen <- err }()
		close(start)
		reply, err := <-began, <-frozen
		if reply.Status == functionlifetime.OK && err == nil {
			t.Fatal("execution dispatch and physical pause both acquired admission")
		}
		if reply.Status != functionlifetime.OK && reply.Status != functionlifetime.Busy {
			t.Fatalf("unexpected admission outcome: %+v", reply)
		}
	}
}

func TestFunctionExecutionUnconfirmedRetirementKeepsOwnership(t *testing.T) {
	m, lease, a := functionExecutionManager(t)
	m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1))
	m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Retire, 0))
	vmm := m.vmm.(*freezingVMM).fakeVMM
	vmm.killErr = errors.New("child exit not confirmed")
	m.functionExecutionWatchdogPass(t.Context(), time.Now())
	if n, _ := a.Inflight("f"); n != 1 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("uncertain physical cleanup released execution ownership")
	}
	m.mu.Lock()
	retained := m.pendingCleanup["f"] != nil
	m.mu.Unlock()
	if !retained {
		t.Fatal("failed retirement discarded recovery ownership")
	}
	vmm.killErr = nil
	m.functionExecutionWatchdogPass(t.Context(), time.Now())
	if n, _ := a.Inflight("f"); n != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("confirmed retry did not retire the original VM")
	}
}
