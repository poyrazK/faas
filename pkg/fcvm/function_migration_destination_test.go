package fcvm

// adr: 20261018030615
// Injected process/load witnesses are portable, not native acceptance.

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func functionDestinationHostFixture(t *testing.T) (*Manager, *consumedSnapshotTestVMM, AdmittedWakeRequest, runtimeadmission.FunctionMigrationDestinationGrant) {
	t.Helper()
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("dedicated function qualification build only")
	}
	m, v, req := admittedSnapshotFixture(t, false)
	m.WithFunctionExecutionActivity(activity.NewWithDefaults())
	req.Request.Runtime = "node22"
	e := req.Request.SnapshotRestore.Clone()
	e.Capture.Memory.StorageKey = strings.Replace(e.Capture.Memory.StorageKey, "/captures/", "/warm/captures/", 1)
	e.Capture.VMState.StorageKey = strings.Replace(e.Capture.VMState.StorageKey, "/captures/", "/warm/captures/", 1)
	e.Capture.PrivateDrive.StorageKey = strings.Replace(e.Capture.PrivateDrive.StorageKey, "/captures/", "/warm/captures/", 1)
	req.Request.Instance = e.Capture.Parent.Binding.InstanceID
	req.Request.Snapshot.StorageKey, req.Request.Snapshot.VMStateStorageKey = e.Capture.Memory.StorageKey, e.Capture.VMState.StorageKey
	req.Request.SnapshotRestore = &e
	now := time.Now()
	parent := e.Capture.Parent
	source := runtimeadmission.FunctionMigrationGrant{Version: 1, Token: e.CaptureToken, CaptureToken: e.CaptureToken, Purpose: runtimeadmission.FunctionMigrationCapture, Parent: parent.Clone(),
		SourceWakeID: uuid.NewString(), LeaseToken: runtimeadmission.FunctionMigrationLeasePrefix + uuid.NewString(), LeaseCreatedAtUnixNano: parent.CompletedAtUnixNano,
		LeaseExpiresAtUnixNano: now.Add(time.Minute).UnixNano(), SourceStartedAtUnixNano: parent.CompletedAtUnixNano,
		MemoryKey: e.Capture.Memory.StorageKey, VMStateKey: e.Capture.VMState.StorageKey, PrivateDriveKey: e.Capture.PrivateDrive.StorageKey, FCVersion: e.FCVersion,
		IssuedAtUnixNano: e.Capture.CapturedAtUnixNano - 1, ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	req.Binding.InstanceID = req.Request.Instance
	req.Binding.IssuedAtUnixNano, req.Binding.ExpiresAtUnixNano = now.UnixNano(), now.Add(50*time.Second).UnixNano()
	req.Binding.SnapshotEvidenceHash, _ = e.Hash()
	req.NativeInputHash, _ = NativeWakeInputHash(req.Request)
	req.Binding.PayloadHash = req.NativeInputHash
	g := runtimeadmission.FunctionMigrationDestinationGrant{Version: 1, Source: source, Restore: e, Binding: req.Binding, WakeID: uuid.NewString()}
	if err := g.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	req.Migration = &g
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Request.Instance) })
	return m, v, req, g
}
func allowFunctionDestination(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	return errors.Join(ctx.Err(), g.Validate(time.Now()))
}

func TestFunctionDestinationHostReadyCommitAndDuplicate(t *testing.T) {
	m, v, req, g := functionDestinationHostFixture(t)
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); err == nil || v.loads != 0 {
		t.Fatal("ordinary admitted wake bypassed destination owner")
	}
	inst, r, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination)
	if err != nil {
		t.Fatal(err)
	}
	if !functionMigrationActive(inst) || v.loads != 1 || g.CheckReceipt(r, time.Now()) != nil {
		t.Fatal("destination opened before ownership or lacks measured consumption")
	}
	if reply := m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Begin, 1)); reply.Status != functionlifetime.Busy {
		t.Fatal("ready destination admitted execution before commit", reply.Status)
	}
	again, receipt, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination)
	if err != nil || again != inst || !receipt.Equal(r) || v.loads != 1 {
		t.Fatal("duplicate restore replayed callbacks", err)
	}
	if _, _, err := m.ActivateFunctionMigrationDestination(t.Context(), g, func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error {
		return runtimeadmission.ErrStale
	}); err == nil || !functionMigrationActive(inst) {
		t.Fatal("activation ignored durable commit")
	}
	if _, _, err := m.ActivateFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err != nil || functionMigrationActive(inst) {
		t.Fatal("committed destination stayed closed", err)
	}
	if reply := m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Begin, 1)); reply.Status != functionlifetime.OK {
		t.Fatal("committed destination refused execution", reply.Status)
	}
	duplicate, receipt, err := m.ActivateFunctionMigrationDestination(t.Context(), g, allowFunctionDestination)
	if err != nil || duplicate != inst || !receipt.Equal(r) || v.loads != 1 {
		t.Fatal("lost activation ACK could not resolve while serving", err)
	}
	if reply := m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Finish, 1)); reply.Status != functionlifetime.OK {
		t.Fatal("destination lost execution ownership", reply.Status)
	}
	if err := m.RetireFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err == nil || m.LiveCount() != 1 {
		t.Fatal("rollback destroyed committed destination")
	}
}

func TestFunctionDestinationRejectsRehearsalWakeBeforeNetworkSetup(t *testing.T) {
	m, v, req, _ := functionDestinationHostFixture(t)
	if _, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination); err != nil {
		t.Fatal(err)
	}
	bypass := req.Request
	bypass.RehearsalRunID = uuid.NewString()
	if _, err := m.Wake(t.Context(), bypass); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatalf("unpublished destination reached rehearsal setup: %v", err)
	}
	if v.loads != 1 || v.bootCount != 0 {
		t.Fatal("rejected rehearsal wake replayed physical work")
	}
}

func TestFunctionDestinationHostFailuresNeverFallbackOrReplay(t *testing.T) {
	for _, scenario := range []string{"load", "probe-or-ACK", "post-restore-policy", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, req, g := functionDestinationHostFixture(t)
			checks := 0
			check := func(ctx context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
				checks++
				if scenario == "post-restore-policy" && checks > 1 {
					return runtimeadmission.ErrStale
				}
				return allowFunctionDestination(ctx, g)
			}
			if scenario == "load" || scenario == "cleanup" {
				v.loadErr = io.EOF
			}
			if scenario == "cleanup" {
				v.killErr = io.EOF
			}
			if scenario == "probe-or-ACK" {
				v.observeSnapshot = func(context.Context) error { return io.EOF }
			}
			_, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, check)
			if err == nil || v.bootCount != 0 || v.loads != 1 {
				t.Fatal("failed restore cold-booted or returned readiness", err)
			}
			if _, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination); err == nil || v.loads != 1 {
				t.Fatal("uncertain restore replayed")
			}
			if scenario == "cleanup" {
				if m.LeasedCount() == 0 {
					t.Fatal("unconfirmed cleanup released capacity")
				}
				v.killErr = nil
			}
			if err := m.RetireFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err != nil || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("retirement incomplete", err)
			}
		})
	}
}
func TestFunctionDestinationHostCancellationJoinsRestore(t *testing.T) {
	m, v, req, g := functionDestinationHostFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	v.observeSnapshot = func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	adopted := make(chan error, 1)
	go func() {
		_, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination)
		adopted <- err
	}()
	select {
	case <-entered:
	case err := <-adopted:
		t.Fatal("restore failed before the cancellation witness", err)
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not reach the cancellation witness")
	}
	if err := m.RetireFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err != nil {
		t.Fatal(err)
	}
	if err := <-adopted; err == nil {
		t.Fatal("cancelled restore returned readiness")
	}
	close(release)
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || calls.Load() != 1 {
		t.Fatal("cancellation lost physical ownership")
	}
}
func TestFunctionDestinationHostRestartAndProductionGate(t *testing.T) {
	m, v, req := admittedSnapshotFixture(t, false)
	if !functionlifetime.NativeQualificationBuild && m.FunctionMigrationDestinationVersion() != 0 {
		t.Fatal("production advertised function migration")
	}
	if !functionlifetime.NativeQualificationBuild {
		if _, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination); err == nil || v.loads != 0 {
			t.Fatal("production restored a function destination")
		}
		return
	}
	_, _, _, g := functionDestinationHostFixture(t)
	if err := m.RetireFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err == nil {
		t.Fatal("restart claimed cleanup without original physical owner")
	}
}

func TestFunctionDestinationHostCancelBeforeAdoptionFencesLateRPC(t *testing.T) {
	m, v, req, g := functionDestinationHostFixture(t)
	if err := m.RetireFunctionMigrationDestination(t.Context(), g, allowFunctionDestination); err != nil {
		t.Fatal("original incarnation could not confirm no-effects cancellation", err)
	}
	if _, _, err := m.AdoptFunctionMigrationDestination(t.Context(), req, allowFunctionDestination); err == nil || v.loads != 0 || m.LeasedCount() != 0 {
		t.Fatal("late RPC restored after confirmed absence", err)
	}
}
