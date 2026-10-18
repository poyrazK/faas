package fcvm

// adr: 20261018030612

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type functionWarmVMM struct {
	*freezingVMM
	capture func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error)
	recover func(context.Context, Lease) error
}

func (v *functionWarmVMM) SnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.capture(ctx, lease, spec)
}

func (v *functionWarmVMM) RecoverFunctionResources(ctx context.Context, lease Lease) error {
	return v.recover(ctx, lease)
}

func TestFunctionResourceWarmRecoveryAdmission(t *testing.T) {
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node build only")
	}
	for _, runtime := range []string{"node22", "node24"} {
		for _, outcome := range []string{"success", "capture-failure", "cancelled-rpc"} {
			t.Run(runtime+"/"+outcome, func(t *testing.T) {
				m, lease, _ := functionExecutionManager(t)
				m.live["f"].Runtime = runtime
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boom := errors.New("capture publication failed")
				v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
				m.vmm = v
				v.capture = func(_ context.Context, got Lease, spec SnapshotSpec) (SnapshotInfo, error) {
					if got != lease || !spec.BeforeCheckpoint || spec.ResumeBeforePublish {
						t.Error("qualified capture did not drain before paused publication")
					}
					hook := executionRequest(functionlifetime.Begin, 1)
					hook.Hook = "before_checkpoint"
					if m.applyFunctionExecution(lease, hook, time.Now()).Status != functionlifetime.OK {
						t.Error("checkpoint lost its owned hook admission")
					}
					m.applyFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1), time.Now())
					if err := m.functionPauseBarrier("f"); err != nil {
						t.Error(err)
					}
					if outcome == "cancelled-rpc" {
						cancel()
					}
					if outcome == "capture-failure" {
						return SnapshotInfo{}, boom
					}
					return SnapshotInfo{MemBytes: 4096}, nil
				}
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				v.recover = func(ctx context.Context, got Lease) error {
					if got != lease || ctx.Err() != nil {
						t.Error("recovery lost its original owner or inherited RPC cancellation")
					}
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				done := make(chan error, 1)
				go func() {
					info, err := m.WarmSnapshot(ctx, "f", SnapshotSpec{})
					if outcome != "success" && !info.IsZero() {
						t.Error("failed capture returned publishable evidence")
					}
					done <- err
				}()
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("capture did not reach recovery")
				}
				if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 2)); got.Status != functionlifetime.Busy {
					t.Fatal("ordinary request entered before pool probes and recovery ACK")
				}
				hook := executionRequest(functionlifetime.Begin, 3)
				hook.Hook = "before_checkpoint"
				if got := m.receiveFunctionExecution(lease, hook); got.Status != functionlifetime.Busy {
					t.Fatal("checkpoint hook entered during recovery")
				}
				select {
				case <-done:
					t.Fatal("capture returned before recovery ACK")
				default:
				}
				unblock()
				err := <-done
				if outcome == "success" && err != nil || outcome == "capture-failure" && !errors.Is(err, boom) || outcome == "cancelled-rpc" && !errors.Is(err, context.Canceled) {
					t.Fatalf("capture outcome: %v", err)
				}
				if m.LiveCount() != 1 || m.LeasedCount() != 1 {
					t.Fatal("confirmed continuing VM lost its resource ownership")
				}
				if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 4)); got.Status != functionlifetime.OK {
					t.Fatal("recovered VM did not reopen admission")
				}
				m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Finish, 4))
			})
		}
	}
}

func TestFunctionResourceWarmUncertainRecoveryRetires(t *testing.T) {
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node build only")
	}
	for _, fault := range []string{"lost-ack", "probe-failure", "deadline", "resume-failure", "unfinished-checkpoint", "cleanup-failure"} {
		t.Run(fault, func(t *testing.T) {
			m, lease, a := functionExecutionManager(t)
			v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
			m.vmm = v
			v.capture = func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
				if fault == "unfinished-checkpoint" {
					r := executionRequest(functionlifetime.Begin, 1)
					r.Hook = "before_checkpoint"
					m.applyFunctionExecution(lease, r, time.Now())
					return SnapshotInfo{}, io.EOF
				}
				return SnapshotInfo{MemBytes: 4096}, nil
			}
			calls := 0
			v.recover = func(ctx context.Context, _ Lease) error {
				calls++
				if fault == "deadline" {
					<-ctx.Done()
					return ctx.Err()
				}
				if fault == "lost-ack" {
					return io.EOF
				}
				return errors.New("resource probe rejected")
			}
			if fault == "resume-failure" {
				v.resumeErr = errors.New("resume failed")
			}
			if fault == "cleanup-failure" {
				v.killErr = errors.New("exit unconfirmed")
			}
			info, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{})
			if !errors.Is(err, ErrFunctionWarmCapture) || !info.IsZero() || calls > 1 {
				t.Fatal("uncertain recovery succeeded, published evidence or replayed a callback")
			}
			if fault == "resume-failure" || fault == "unfinished-checkpoint" {
				if calls != 0 {
					t.Fatal("ran recovery before resume/quiescence was established")
				}
			}
			if fault == "cleanup-failure" {
				if m.LiveCount() != 1 || m.LeasedCount() != 1 || m.pendingCleanup["f"] == nil {
					t.Fatal("unconfirmed retirement released ownership")
				}
				if m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 2)).Status == functionlifetime.OK {
					t.Fatal("failed recovery reopened admission during uncertain cleanup")
				}
				v.killErr = nil
				m.functionExecutionWatchdogPass(t.Context(), time.Now())
			}
			if m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("failed recovery did not retire its physical VM")
			}
			if n, seen := a.Inflight("f"); n != 0 || seen {
				t.Fatal("confirmed retirement retained execution occupancy")
			}
		})
	}
}

type measuredFunctionWarmVMM struct {
	*capturedRuntimeVMM
	recoveryErr error
}

func (v *measuredFunctionWarmVMM) RecoverFunctionResources(context.Context, Lease) error {
	return v.recoveryErr
}

func TestFunctionResourceWarmMeasuredAuthority(t *testing.T) {
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node build only")
	}
	m, original, req := consumedRuntimeFixture(t)
	v := &measuredFunctionWarmVMM{capturedRuntimeVMM: &capturedRuntimeVMM{consumedRuntimeVMM: original}}
	m.vmm = v
	inst, parent, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	inst.Runtime = "node22"
	m.WithFunctionExecutionActivity(activity.NewWithDefaults())
	grant := managedSnapshotGrant(m, parent, true)
	if _, _, _, _, err := m.beginSnapshotFlight(t.Context(), inst.Lease.Instance, SnapshotSpec{admittedGrant: &grant}, snapshotWarm); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("measured warm capture inferred ungranted checkpoint authority", err)
	}
	if reply := m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Begin, 1)); reply.Status != functionlifetime.OK {
		t.Fatal("fixture could not register active execution", reply)
	}
	for _, reserved := range []bool{false, true} {
		var held *instanceFlight
		if reserved {
			_, _, held, err = m.registerLiveInstanceFlight(t.Context(), inst.Lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
		}
		busy := managedSnapshotGrant(m, parent, true)
		busy.BeforeCheckpoint = true
		info, ack, err := m.CaptureAdmitted(t.Context(), busy)
		if held != nil {
			m.finishInstanceFlight(inst.Lease.Instance, held)
		}
		if !errors.Is(err, ErrInstanceBusy) || !info.IsZero() || ack.Grant.Token != "" || m.LiveCount() != 1 || m.LeasedCount() != 1 || v.seen.StorageKey != "" || !functionExecutionBusy(inst) {
			t.Fatal("measured pre-capture refusal interrupted owned execution", err)
		}
	}
	m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Finish, 1))
	grant.BeforeCheckpoint = true
	info, ack, err := m.CaptureAdmitted(t.Context(), grant)
	if err != nil || ack.Check(grant, time.Now()) != nil || !info.Capture.Parent.Equal(parent) || !v.seen.BeforeCheckpoint || v.seen.ResumeBeforePublish {
		t.Fatal("qualified warm capture lost explicit checkpoint authority or retained identity", err)
	}
	// The outer capture authority flight must end before retirement joins
	// teardown; a failed resource ACK cannot deadlock on its own caller.
	v.recoveryErr = io.EOF
	grant = managedSnapshotGrant(m, parent, true)
	grant.BeforeCheckpoint = true
	info, ack, err = m.CaptureAdmitted(t.Context(), grant)
	if !errors.Is(err, io.EOF) || !info.IsZero() || ack.Grant.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("measured warm failure did not unwind authority and retire", err)
	}
}

func TestFunctionResourceWarmRecoveryJoinedByDestroy(t *testing.T) {
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node build only")
	}
	m, _, _ := functionExecutionManager(t)
	v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
	m.vmm = v
	v.capture = func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
		return SnapshotInfo{MemBytes: 4096}, nil
	}
	entered := make(chan struct{})
	v.recover = func(ctx context.Context, _ Lease) error { close(entered); <-ctx.Done(); return ctx.Err() }
	done := make(chan error, 1)
	go func() { _, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{}); done <- err }()
	<-entered
	if err := m.Destroy(t.Context(), "f"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("destroy did not cancel and join the original recovery", err)
	}
}

func TestFunctionResourceWarmCapabilityAndProductionGate(t *testing.T) {
	m, _, _ := functionExecutionManager(t)
	if functionlifetime.NativeQualificationBuild {
		if _, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{}); !errors.Is(err, runtimeadmission.ErrUnavailable) {
			t.Fatal("warm capture admitted a VMM without single-attempt recovery", err)
		}
		return
	}
	v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
	m.vmm = v
	v.capture = func(_ context.Context, _ Lease, spec SnapshotSpec) (SnapshotInfo, error) {
		if spec.BeforeCheckpoint || !spec.ResumeBeforePublish {
			t.Error("ordinary build activated resource checkpointing")
		}
		return SnapshotInfo{MemBytes: 4096}, nil
	}
	v.recover = func(context.Context, Lease) error { t.Error("ordinary build activated resource recovery"); return nil }
	if _, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{}); err != nil {
		t.Fatal(err)
	}
}
