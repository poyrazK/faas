package sched

// adr: 396 — failed or unfinished teardown must retain scheduler admission.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostport"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type teardownAccountingCase struct {
	name    string
	initial state.State
	final   state.State
	stop    func(context.Context, *Engine, state.Instance) error
}

func teardownAccountingCases() []teardownAccountingCase {
	cases := []teardownAccountingCase{
		{"liveness", state.StateRunning, state.StateStopped, func(ctx context.Context, e *Engine, i state.Instance) error {
			return e.DestroyForLivenessFailure(ctx, i.ID, "liveness_timeout")
		}},
		{"workload_oom", state.StateRunning, state.StateStopped, func(ctx context.Context, e *Engine, i state.Instance) error {
			return e.DestroyForWorkloadOOMFailure(ctx, i.ID, 384, 256)
		}},
		{"force_restart", state.StateRunning, state.StateStopped, func(ctx context.Context, e *Engine, i state.Instance) error {
			_, err := e.ForceRestart(ctx, i.ID, "accounting_test")
			return err
		}},
		{"disk_pressure", state.StateRunning, state.StateStopped, func(ctx context.Context, e *Engine, i state.Instance) error {
			return e.RecycleForDiskPressure(ctx, i.ID, 100, 100)
		}},
		{"egress_abuse", state.StateRunning, state.StateStopped, func(ctx context.Context, e *Engine, i state.Instance) error {
			return e.RecycleForEgressAbuse(ctx, i.ID, EgressAbuseFanout, 1500, 1200)
		}},
	}
	for _, reason := range []StuckReason{StuckWakingTimeout, StuckColdBootTimeout, StuckSnapshotTimeout} {
		cases = append(cases, teardownAccountingCase{
			name: string(reason), initial: expectedStateForReason(reason), final: terminalStateForReason(reason),
			stop: func(ctx context.Context, e *Engine, i state.Instance) error {
				return e.KillStuck(ctx, i.ID, i.AppID, reason)
			},
		})
	}
	return cases
}

type heldAccountingDestroy struct {
	*fakeVMM
	entered, release chan struct{}
	once             sync.Once
}

func (v *heldAccountingDestroy) Destroy(ctx context.Context, nodeID, instanceID string) error {
	v.once.Do(func() { close(v.entered) })
	select {
	case <-v.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return v.fakeVMM.Destroy(ctx, nodeID, instanceID)
}

func admitAccountingInstance(t *testing.T, s state.Store, e *Engine, app state.App, dep state.Deployment, initial state.State) state.Instance {
	t.Helper()
	i, err := s.CreateInstance(context.Background(), app.ID, dep.ID, string(initial), app.RAMMB, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	if err := e.Ledger().Admit(Request{
		Instance: i.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: effectiveAppCPUMillicores(app),
		MaxConcurrency: app.MaxConcurrency, NodeID: i.NodeID,
	}); err != nil {
		t.Fatal(err)
	}
	if initial == state.StateSnapshotting {
		e.Ledger().BeginSnapshot(i.ID)
	}
	return i
}

func assertAccountingRetained(t *testing.T, s state.Store, e *Engine, i state.Instance) {
	t.Helper()
	fresh, err := s.InstanceByID(context.Background(), i.ID)
	if err != nil || fresh.State != i.State {
		t.Fatalf("resident state changed before confirmed teardown: row=%+v err=%v", fresh, err)
	}
	limits, _ := api.LimitsFor(api.PlanPro)
	conc := 0
	if state.State(i.State).CountsForConcurrency() {
		conc = 1
	}
	if !e.Ledger().ResidentFor(i.ID) || e.Ledger().ResidentRAMForNode(i.NodeID) != i.RAMMB+api.PerVMOverheadMB ||
		e.Ledger().UsedVCPUForNode(i.NodeID) != limits.VCPU ||
		e.Ledger().UsedCPUMillicoresForNode(i.NodeID) != api.DefaultAppCPUMillicores ||
		e.Ledger().Concurrency(i.AppID) != conc || e.Ledger().ConcurrencyForDeployment(i.AppID, i.DeploymentID) != conc {
		t.Fatalf("lost reservation: resident=%v RAM=%d vCPU=%d CPU=%d concurrency=%d", e.Ledger().ResidentFor(i.ID),
			e.Ledger().ResidentRAMForNode(i.NodeID), e.Ledger().UsedVCPUForNode(i.NodeID),
			e.Ledger().UsedCPUMillicoresForNode(i.NodeID), e.Ledger().Concurrency(i.AppID))
	}
}

func TestEngineTeardownRetainsAccountingUntilConfirmed(t *testing.T) {
	for _, tc := range teardownAccountingCases() {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			s := state.NewMemStore()
			_, app, dep := seedApp(t, s, api.PlanPro, 512, 1)
			unconfirmed := errors.New("unconfirmed teardown")
			v := &heldAccountingDestroy{fakeVMM: &fakeVMM{destroyErr: unconfirmed}, entered: make(chan struct{}), release: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(v.release) }) }
			t.Cleanup(release)
			ops := wire.NewOpsMetrics("schedd")
			e := newEngine(t, s, v, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
			i := admitAccountingInstance(t, s, e, app, dep, tc.initial)
			if _, err := s.AcquireHostPortLeases(ctx, i.NodeID, i.ID, []hostport.Request{{Protocol: hostport.TCP, GuestPort: 8080}}); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- tc.stop(ctx, e, i) }()
			select {
			case <-v.entered:
			case <-ctx.Done():
				t.Fatal("destroy did not start")
			}
			assertAccountingRetained(t, s, e, i)
			release()
			select {
			case err := <-result:
				if !errors.Is(err, unconfirmed) {
					t.Fatalf("failed teardown acknowledged: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("stop did not return")
			}
			assertAccountingRetained(t, s, e, i)
			leases, err := s.ListHostPortLeases(ctx, i.NodeID, i.ID)
			if err != nil || len(leases) != 1 {
				t.Fatalf("failed destroy released host port: leases=%v err=%v", leases, err)
			}
			if readCounterValue(t, ops.LivenessRestarts(app.ID, dep.ID)) != 0 || readCounterValue(t, ops.WorkloadOOMKills(app.ID, dep.ID)) != 0 {
				t.Fatal("failed teardown reported a completed restart")
			}
			finalDep, err := s.DeploymentByID(ctx, dep.ID)
			if err != nil || finalDep.ErrorCode != "" {
				t.Fatalf("failed teardown stamped deployment outcome: %+v err=%v", finalDep, err)
			}

			// A fresh schedd must rebuild the retained capacity from durable rows.
			restarted := newEngine(t, s, v, &fakeNotifier{}, "1.10.0")
			if err := restarted.SeedLedger(ctx); err != nil {
				t.Fatal(err)
			}
			assertAccountingRetained(t, s, restarted, i)
			if tc.initial.CountsForConcurrency() {
				if outcome, err := restarted.Wake(ctx, app.ID, "", "", ""); err == nil && outcome.InstanceID != "" && outcome.InstanceID != i.ID {
					t.Fatal("replacement admitted while old guest remains reserved")
				}
				if err := restarted.Ledger().Admit(Request{
					Instance: "replacement", AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
					RAMMB: app.RAMMB, VCPU: 1, MaxConcurrency: app.MaxConcurrency, NodeID: i.NodeID,
				}); err == nil {
					t.Fatal("replacement capacity admitted while old guest remains reserved")
				}
			}

			v.mu.Lock()
			v.destroyErr = nil
			v.mu.Unlock()
			if err := tc.stop(ctx, restarted, i); err != nil {
				t.Fatalf("retry teardown: %v", err)
			}
			if restarted.Ledger().ResidentFor(i.ID) || restarted.Ledger().ResidentRAM() != 0 || restarted.Ledger().UsedVCPU() != 0 || restarted.Ledger().Concurrency(app.ID) != 0 {
				t.Fatal("confirmed teardown retained admission")
			}
			fresh, err := s.InstanceByID(ctx, i.ID)
			if err != nil || fresh.State != string(tc.final) {
				t.Fatalf("confirmed outcome: state=%q err=%v, want %q", fresh.State, err, tc.final)
			}
			// The waking timeout's existing fallback is COLD_BOOTING; it retains
			// the host-port lease until the subsequent terminal transition.
			if !tc.final.CountsForRAM() {
				leases, err = s.ListHostPortLeases(ctx, i.NodeID, i.ID)
				if err != nil || len(leases) != 0 {
					t.Fatalf("confirmed teardown retained ports: leases=%v err=%v", leases, err)
				}
			}
		})
	}
}

type accountingReadFailureStore struct {
	state.Store
	failAt, reads int
	err           error
}

func (s *accountingReadFailureStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	s.reads++
	if s.reads == s.failAt {
		return state.Instance{}, s.err
	}
	return s.Store.InstanceByID(ctx, id)
}

func TestEngineTeardownReadFailureRetainsAccounting(t *testing.T) {
	for _, tc := range teardownAccountingCases() {
		for _, read := range []int{1, 2} {
			// KillStuck performs its only read under the app lock.
			if tc.initial != state.StateRunning && read == 2 {
				continue
			}
			for _, readErr := range []error{state.ErrNotFound, errors.New("database unavailable")} {
				t.Run(tc.name+"/"+readErr.Error()+"/"+strconv.Itoa(read), func(t *testing.T) {
					base := state.NewMemStore()
					_, app, dep := seedApp(t, base, api.PlanPro, 512, 1)
					s := &accountingReadFailureStore{Store: base, failAt: read, err: readErr}
					v := &fakeVMM{}
					e := newEngine(t, s, v, &fakeNotifier{}, "1.10.0")
					i := admitAccountingInstance(t, base, e, app, dep, tc.initial)
					if err := tc.stop(t.Context(), e, i); !errors.Is(err, readErr) {
						t.Fatalf("read failure not propagated: %v", err)
					}
					assertAccountingRetained(t, base, e, i)
					if v.destroys != 0 {
						t.Fatal("destroyed without establishing current ownership")
					}
				})
			}
		}
	}
}

func TestEngineTeardownStateRaceRetainsAccounting(t *testing.T) {
	for _, tc := range teardownAccountingCases() {
		for _, moved := range []state.State{state.StateRunning, state.StateWaking, state.StateSnapshotting, state.StateDraining} {
			if tc.initial == moved {
				continue
			}
			t.Run(tc.name+"/"+string(moved), func(t *testing.T) {
				s := state.NewMemStore()
				_, app, dep := seedApp(t, s, api.PlanPro, 512, 1)
				v := &fakeVMM{}
				e := newEngine(t, s, v, &fakeNotifier{}, "1.10.0")
				i := admitAccountingInstance(t, s, e, app, dep, moved)
				err := tc.stop(t.Context(), e, i)
				if tc.name == "force_restart" {
					if !errors.Is(err, state.ErrInstanceNotRunning) {
						t.Fatalf("race error: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				assertAccountingRetained(t, s, e, i)
				if v.destroys != 0 {
					t.Fatal("state race destroyed another operation's guest")
				}
			})
		}
	}
}
