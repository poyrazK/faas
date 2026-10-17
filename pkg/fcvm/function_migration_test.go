package fcvm

// adr: 1575

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func functionMigrationFixture(t *testing.T) (*Manager, Lease, *functionWarmVMM, FunctionMigrationBinding) {
	t.Helper()
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node source only")
	}
	m, lease, _ := functionExecutionManager(t)
	m.live["f"].wakeID = uuid.NewString()
	binding := FunctionMigrationBinding{InstanceID: "f", LeaseToken: uuid.NewString(), SourceNodeID: uuid.NewString(), SourceWakeID: m.live["f"].wakeID}
	v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
	m.vmm = v
	v.capture = func(_ context.Context, got Lease, spec SnapshotSpec) (SnapshotInfo, error) {
		if got != lease || !spec.BeforeCheckpoint || spec.ResumeBeforePublish || spec.functionMigration == nil || *spec.functionMigration != binding {
			t.Error("source capture lost owned identity or checkpoint boundary")
		}
		if err := m.functionPauseBarrier("f"); err != nil {
			return SnapshotInfo{}, err
		}
		return SnapshotInfo{MemBytes: 4096, VMStateBytes: 1024}, nil
	}
	v.recover = func(context.Context, Lease) error { return nil }
	return m, lease, v, binding
}

func prepareFunctionSource(t *testing.T, m *Manager, binding FunctionMigrationBinding) {
	t.Helper()
	if _, err := m.PrepareFunctionMigrationSource(t.Context(), binding, SnapshotSpec{}, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionMigrationSourceRecoveryAdmission(t *testing.T) {
	for _, runtime := range []string{"node22", "node24"} {
		t.Run(runtime, func(t *testing.T) {
			m, lease, v, binding := functionMigrationFixture(t)
			m.live["f"].Runtime = runtime
			prepareFunctionSource(t, m, binding)
			if m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("preparation released paused source ownership")
			}
			if got := m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1)); got.Status != functionlifetime.Busy {
				t.Fatal("request entered prepared source", got)
			}
			for name, operation := range map[string]func() error{
				"forward":   func() error { _, err := m.AwaitFrozen(t.Context(), "f"); return err },
				"resume":    func() error { return m.ResumeVM(t.Context(), "f") },
				"thaw":      func() error { return m.Thaw(t.Context(), "f") },
				"freeze":    func() error { _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); return err },
				"warm":      func() error { _, err := m.WarmSnapshot(t.Context(), "f", SnapshotSpec{}); return err },
				"migration": func() error { _, err := m.SnapshotKeepAlive(t.Context(), "f", SnapshotSpec{}); return err },
			} {
				if err := operation(); !errors.Is(err, ErrInstanceBusy) {
					t.Fatalf("%s bypassed source gate: %v", name, err)
				}
			}
			calls := 0
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			v.recover = func(ctx context.Context, got Lease) error {
				calls++
				if got != lease || ctx.Err() != nil {
					t.Error("recovery lost original owner or inherited expired RPC")
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if _, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return runtimeadmission.ErrUnavailable }); !errors.Is(err, runtimeadmission.ErrUnavailable) || calls != 0 {
				t.Fatal("resumed without durable rollback fence", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				retired, err := m.RecoverFunctionMigrationSource(ctx, binding, func(context.Context) error { return nil })
				if retired {
					t.Error("confirmed recovery reported retirement")
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("no source recovery")
			}
			cancel()
			if m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 2)).Status != functionlifetime.Busy {
				t.Fatal("ordinary request entered before final recovery ACK")
			}
			hook := executionRequest(functionlifetime.Begin, 3)
			hook.Hook = "before_checkpoint"
			if m.receiveFunctionExecution(lease, hook).Status != functionlifetime.Busy {
				t.Fatal("checkpoint reentered recovery")
			}
			if _, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil }); !errors.Is(err, ErrInstanceBusy) {
				t.Fatal("duplicate recovery interrupted original attempt", err)
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if retired, err := m.RecoverFunctionMigrationSource(t.Context(), binding, nil); err != nil || retired {
					t.Fatal(retired, err)
				}
			}
			if calls != 1 {
				t.Fatal("replayed application recovery callback", calls)
			}
			if m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 4)).Status != functionlifetime.OK {
				t.Fatal("confirmed recovery kept admission closed")
			}
			m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Finish, 4))
			m.ForgetFunctionMigrationSource(binding)
			if len(m.functionMigrationSources) != 0 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("lease cleanup lost live source or retained callback state")
			}
		})
	}
}

func TestFunctionMigrationSourceUncertainRecovery(t *testing.T) {
	for _, fault := range []string{"lost-ack", "probe", "deadline", "resume", "checkpoint", "cleanup"} {
		t.Run(fault, func(t *testing.T) {
			m, lease, v, binding := functionMigrationFixture(t)
			if fault == "checkpoint" {
				v.capture = func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) { return SnapshotInfo{}, io.EOF }
			}
			_, prepareErr := m.PrepareFunctionMigrationSource(t.Context(), binding, SnapshotSpec{}, func(context.Context) error { return nil })
			if (prepareErr != nil) != (fault == "checkpoint") {
				t.Fatal(prepareErr)
			}
			calls := 0
			v.recover = func(ctx context.Context, _ Lease) error {
				calls++
				if fault == "deadline" {
					<-ctx.Done()
					return ctx.Err()
				}
				return io.EOF
			}
			if fault == "resume" {
				v.resumeErr = errors.New("resume failed")
			}
			if fault == "cleanup" {
				v.killErr = errors.New("exit unconfirmed")
			}
			retired, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil })
			if fault == "cleanup" {
				if err == nil || retired || m.LiveCount() != 1 || m.LeasedCount() != 1 || m.pendingCleanup["f"] == nil {
					t.Fatal("unconfirmed retirement lost capacity", retired, err)
				}
				if m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 2)).Status == functionlifetime.OK {
					t.Fatal("failed recovery reopened admission")
				}
				v.killErr = nil
				retired, err = m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil })
			}
			if err != nil || !retired || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("uncertain source was not physically retired", retired, err)
			}
			want := 1
			if fault == "resume" || fault == "checkpoint" {
				want = 0
			}
			if calls != want {
				t.Fatal("replayed callback or ran before checkpoint/resume", calls)
			}
			if retired, err := m.RecoverFunctionMigrationSource(t.Context(), binding, nil); err != nil || !retired {
				t.Fatal("retirement was not idempotent", retired, err)
			}
		})
	}
}

func TestFunctionMigrationSourceDestroyAndStaleBinding(t *testing.T) {
	m, _, v, binding := functionMigrationFixture(t)
	prepareFunctionSource(t, m, binding)
	stale := binding
	stale.SourceWakeID = uuid.NewString()
	if _, err := m.RecoverFunctionMigrationSource(t.Context(), stale, func(context.Context) error { return nil }); !errors.Is(err, runtimeadmission.ErrStale) {
		t.Fatal("stale wake acquired physical source", err)
	}
	entered := make(chan struct{})
	v.recover = func(ctx context.Context, _ Lease) error { close(entered); <-ctx.Done(); return ctx.Err() }
	done := make(chan error, 1)
	go func() {
		_, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no recovery")
	}
	if err := m.Destroy(t.Context(), "f"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Destroy did not join recovery")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("Destroy released before original source exited")
	}
	if _, err := m.Wake(t.Context(), admissionWake("f")); err != nil {
		t.Fatal(err)
	}
	if err := m.RetireFunctionMigrationSource(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("late acknowledgement retired replacement")
	}
}

func TestFunctionMigrationSourceProductionAndMeasuredGate(t *testing.T) {
	m, _, _ := functionExecutionManager(t)
	binding := FunctionMigrationBinding{InstanceID: "f", LeaseToken: uuid.NewString(), SourceNodeID: uuid.NewString(), SourceWakeID: uuid.NewString()}
	m.live["f"].wakeID = binding.SourceWakeID
	register := func(context.Context) error { t.Error("unqualified prepare registered durable source"); return nil }
	if _, err := m.PrepareFunctionMigrationSource(t.Context(), binding, SnapshotSpec{}, register); err == nil {
		t.Fatal("unqualified prepare succeeded")
	}
	if len(m.functionMigrationSources) != 0 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("refusal changed physical ownership")
	}
	if functionlifetime.NativeQualificationBuild {
		v := &functionWarmVMM{freezingVMM: m.vmm.(*freezingVMM)}
		m.vmm = v
		m.live["f"].runtimeAdmissionReceipt.Binding.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
		if _, err := m.PrepareFunctionMigrationSource(t.Context(), binding, SnapshotSpec{}, register); err == nil {
			t.Fatal("measured migration inferred missing authority")
		}
	}
}

func TestFunctionMigrationSourceCancelledCheckpointRetires(t *testing.T) {
	m, lease, v, binding := functionMigrationFixture(t)
	entered := make(chan struct{})
	v.capture = func(ctx context.Context, _ Lease, _ SnapshotSpec) (SnapshotInfo, error) {
		hook := executionRequest(functionlifetime.Begin, 1)
		hook.Hook = "before_checkpoint"
		if m.receiveFunctionExecution(lease, hook).Status != functionlifetime.OK { //nolint:contextcheck // Simulate the receiver's synchronous ledger operation, which accepts no context.
			t.Error("checkpoint lost hook ownership")
		}
		close(entered)
		<-ctx.Done()
		return SnapshotInfo{}, ctx.Err()
	}
	v.recover = func(context.Context, Lease) error {
		t.Error("replayed after_restore after uncertain checkpoint cleanup")
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.PrepareFunctionMigrationSource(ctx, binding, SnapshotSpec{}, func(context.Context) error { return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no checkpoint")
	}
	if _, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil }); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("cancel recovery raced checkpoint", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retired, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil })
	if err != nil || !retired || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cancelled checkpoint lost ownership or resumed uncertain work", retired, err)
	}
}

func TestFunctionMigrationSourceRegistrationLostAckDoesNotReplay(t *testing.T) {
	m, lease, v, binding := functionMigrationFixture(t)
	v.capture = func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
		t.Error("captured before registration acknowledgement")
		return SnapshotInfo{}, nil
	}
	v.recover = func(context.Context, Lease) error {
		t.Error("ran callback for a registration-only failure")
		return nil
	}
	if _, err := m.PrepareFunctionMigrationSource(t.Context(), binding, SnapshotSpec{}, func(context.Context) error { return io.EOF }); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if retired, err := m.RecoverFunctionMigrationSource(t.Context(), binding, nil); retired || err != nil {
		t.Fatal("lost known no-effects result", retired, err)
	}
	if m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Begin, 1)).Status != functionlifetime.OK {
		t.Fatal("registration failure damaged healthy admission")
	}
	m.receiveFunctionExecution(lease, executionRequest(functionlifetime.Finish, 1))
}
