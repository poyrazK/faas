package sched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type initialBurstVerifier struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (v *initialBurstVerifier) Verify(ctx context.Context, _, _ string) error {
	v.calls.Add(1)
	if v.entered != nil {
		select {
		case v.entered <- struct{}{}:
		default:
		}
	}
	if v.release != nil {
		select {
		case <-v.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return v.err
}

func TestInitialWakeBurstOverlapsAfterVerification(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold_boot", true: "restore"}[restore], func(t *testing.T) {
			store := state.NewMemStore()
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
			if restore {
				if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 1, StorageKey: SnapshotMemKey(dep.ID)}); err != nil {
					t.Fatal(err)
				}
			}
			vmm := &fakeVMM{bootStarted: make(chan struct{}, 2), bootRelease: make(chan struct{})}
			verifier := &initialBurstVerifier{entered: make(chan struct{}, 2), release: make(chan struct{})}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithVerifier(verifier)
			result := make(chan CoordOutcome, 1)
			go func() {
				out, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 2)
				if err != nil {
					out.Err = err
				}
				result <- out
			}()
			<-verifier.entered
			select {
			case <-vmm.bootStarted:
				t.Error("VM dispatched before layer verification")
			case <-time.After(20 * time.Millisecond):
			}
			if got := e.Ledger().Concurrency(app.ID); got != 1 {
				t.Errorf("reservations before first verification = %d, want 1", got)
			}
			close(verifier.release)
			// Neither VM may complete yet: both RPCs must start before either
			// completion is released. The old sequential path cannot pass this.
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			for range 2 {
				select {
				case <-vmm.bootStarted:
				case <-deadline.C:
					close(vmm.bootRelease)
					<-result
					t.Fatal("sibling restore waited for first completion")
				}
			}
			close(vmm.bootRelease)
			out := <-result
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			if out.Instance == nil || len(out.Additional) != 1 || out.Instance.InstanceID == out.Additional[0].InstanceID {
				t.Fatalf("burst result: %+v", out)
			}
			if verifier.calls.Load() != 2 {
				t.Fatalf("verification calls=%d, want every instance verified", verifier.calls.Load())
			}
			if e.Ledger().Concurrency(app.ID) != 2 {
				t.Fatal("wrong residency after burst")
			}
		})
	}
}

func TestInitialWakeBurstRefusalDoesNotDispatchSiblings(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 3)
	vmm := &fakeVMM{}
	failure := errors.New("layer storage unavailable")
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithVerifier(&initialBurstVerifier{err: failure})
	out, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 3)
	if err == nil || out.Instance != nil || len(out.Additional) != 0 {
		t.Fatalf("verification refusal: %+v, %v", out, err)
	}
	if vmm.coldBoots != 0 || vmm.restores != 0 || e.Ledger().Concurrency(app.ID) != 0 {
		t.Fatal("refused first admission created sibling capacity")
	}
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil || len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Fatalf("refused first admission rows=%+v, err=%v", rows, err)
	}
}

func TestInitialWakeBurstHonorsAppCapAndExistingInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 1)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	first, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 1000)
	if err != nil || first.Instance == nil || len(first.Additional) != 0 {
		t.Fatalf("app cap result=%+v err=%v", first, err)
	}
	second, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 1000)
	if err != nil || second.Instance == nil || second.Instance.InstanceID != first.Instance.InstanceID || len(second.Additional) != 0 {
		t.Fatalf("existing instance result=%+v err=%v", second, err)
	}
	if vmm.coldBoots != 1 || e.Ledger().Concurrency(app.ID) != 1 {
		t.Fatal("exceeded app cap or duplicated existing VM")
	}
}

type partialInitialVMM struct {
	*fakeVMM
	failPrimary bool
}

func (v *partialInitialVMM) CreateColdBoot(ctx context.Context, node, instance string, spec AppSpec) (*WakeOutcome, error) {
	_, primary := ctx.Value(admissionReadyKey{}).(*admissionReadySignal)
	if primary == v.failPrimary {
		return nil, errors.New("one boot failed")
	}
	return v.fakeVMM.CreateColdBoot(ctx, node, instance, spec)
}

func TestInitialWakeBurstKeepsSuccessfulPartialCapacity(t *testing.T) {
	for _, failPrimary := range []bool{true, false} {
		t.Run(map[bool]string{true: "primary_fails", false: "sibling_fails"}[failPrimary], func(t *testing.T) {
			store := state.NewMemStore()
			_, app, _ := seedApp(t, store, api.PlanPro, 256, 3)
			vmm := &partialInitialVMM{fakeVMM: &fakeVMM{}, failPrimary: failPrimary}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			out, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 2)
			if err != nil || out.Instance == nil || len(out.Additional) != 0 {
				t.Fatalf("partial outcome=%+v err=%v", out, err)
			}
			if e.Ledger().Concurrency(app.ID) != 1 {
				t.Fatal("failed boot retained a reservation")
			}
			rows, err := store.ListInstancesForApp(context.Background(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			states := map[string]int{}
			for _, row := range rows {
				states[row.State]++
			}
			if states[string(state.StateRunning)] != 1 || states[string(state.StateFailed)] != 1 {
				t.Fatalf("partial states=%v", states)
			}
		})
	}
}

func TestInitialWakeBurstSharesLifecycleWithCanceledLeader(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 3)
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 2), bootRelease: make(chan struct{})}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan CoordOutcome, 2)
	go func() {
		out, err := e.EnsureWakeCapacity(ctx, app.ID, TriggerGateway, 2)
		if err != nil {
			out.Err = err
		}
		results <- out
	}()
	for range 2 {
		select {
		case <-vmm.bootStarted:
		case <-time.After(time.Second):
			close(vmm.bootRelease)
			t.Fatal("missing concurrent boot")
		}
	}
	cancel()
	go func() {
		out, err := e.EnsureWakeCapacity(context.Background(), app.ID, TriggerGateway, 2)
		if err != nil {
			out.Err = err
		}
		results <- out
	}()
	deadline := time.Now().Add(time.Second)
	for {
		e.wakeCoord.mu.Lock()
		waiting := 0
		for _, call := range e.wakeCoord.inflight[app.ID] {
			waiting += call.waiters
		}
		e.wakeCoord.mu.Unlock()
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(vmm.bootRelease)
			t.Fatal("follower did not join shared wake")
		}
		time.Sleep(time.Millisecond)
	}
	close(vmm.bootRelease)
	first, second := <-results, <-results
	if first.Err != nil || second.Err != nil || first.Instance == nil || second.Instance == nil || len(first.Additional) != 1 || len(second.Additional) != 1 {
		t.Fatalf("shared outcomes=%+v/%+v", first, second)
	}
	if first.Instance.InstanceID != second.Instance.InstanceID || first.Additional[0].InstanceID != second.Additional[0].InstanceID {
		t.Fatal("followers did not share the same burst")
	}
	if e.Ledger().Concurrency(app.ID) != 2 {
		t.Fatal("duplicate follower capacity")
	}
}
