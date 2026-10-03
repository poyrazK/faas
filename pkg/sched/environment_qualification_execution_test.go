// adr: 493 — no generic VM acknowledgement releases a qualification attempt.
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Storage/scheduler fixture only: these fabricated receipts do not validate
// native retirement. Production must obtain them from the fenced host journal.
type qualificationRuntimeVMM struct {
	*fakeVMM
	proof          state.EnvironmentQualificationRetirement
	changeEvidence func(*EnvironmentQualificationRetirementEvidence)
	beforeBoot     func(state.EnvironmentQualificationExecution) error
	lastRetired    state.EnvironmentQualificationExecution
}

func newQualificationRuntimeVMM(v *fakeVMM) *qualificationRuntimeVMM {
	return &qualificationRuntimeVMM{fakeVMM: v, proof: state.EnvironmentQualificationRetirement{
		Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: uuid.NewString(),
		KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}}
}

func (v *qualificationRuntimeVMM) CreateEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution, spec AppSpec) (*WakeOutcome, error) {
	if v.beforeBoot != nil {
		if err := v.beforeBoot(frame); err != nil {
			return nil, err
		}
	}
	return v.fakeVMM.CreateColdBoot(ctx, frame.NodeID, frame.InstanceID, spec)
}

func (v *qualificationRuntimeVMM) RetireEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (EnvironmentQualificationRetirementEvidence, error) {
	v.lastRetired = frame
	if err := v.fakeVMM.Destroy(ctx, frame.NodeID, frame.InstanceID); err != nil {
		return EnvironmentQualificationRetirementEvidence{}, err
	}
	evidence := EnvironmentQualificationRetirementEvidence{Execution: frame, Retirement: v.proof}
	if v.changeEvidence != nil {
		v.changeEvidence(&evidence)
	}
	return evidence, nil
}

func TestEnvironmentQualificationRefusesGenericVMBeforeAdmission(t *testing.T) {
	store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	v := &fakeVMM{}
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { t.Fatal("generic VM reached qualification"); return nil }); !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := store.InstanceByID(t.Context(), request.ReservedInstanceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("refused capability admitted reservation", err)
	}
	if v.coldBoots != 0 || v.destroys != 0 {
		t.Fatal("generic VM performed effects")
	}
}

func TestEnvironmentQualificationDispatchPrecedesVMAndInvalidRetirementKeepsCharge(t *testing.T) {
	for _, failure := range []string{"wrong_frame", "missing_resources", "not_found"} {
		t.Run(failure, func(t *testing.T) {
			store, _, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
			v := newQualificationRuntimeVMM(&fakeVMM{})
			v.beforeBoot = func(frame state.EnvironmentQualificationExecution) error {
				stored, err := store.EnvironmentQualificationExecution(t.Context(), frame.InstanceID)
				if err != nil || !stored.DispatchStarted || stored.Execution != frame {
					t.Fatal("VM effects preceded durable dispatch", err)
				}
				return nil
			}
			switch failure {
			case "wrong_frame":
				v.changeEvidence = func(e *EnvironmentQualificationRetirementEvidence) { e.Execution.WakeID = uuid.NewString() }
			case "missing_resources":
				v.changeEvidence = func(e *EnvironmentQualificationRetirementEvidence) { e.Retirement.ResourcesRemoved = false }
			case "not_found":
				v.destroyErr = status.Error(codes.NotFound, "generic absence")
			}
			e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
			if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { return nil }); err == nil {
				t.Fatal("invalid native retirement acknowledged")
			}
			stored, err := store.EnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID)
			ins, insErr := store.InstanceByID(t.Context(), request.ReservedInstanceID)
			if err != nil || insErr != nil || stored.RetiredAt != nil || ins.State != string(state.StateRunning) || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
				t.Fatal("uncertainty released reservation", stored, ins, err, insErr)
			}
			v.changeEvidence, v.destroyErr = nil, nil
			if err := e.RecoverEnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID); err != nil {
				t.Fatal(err)
			}
			if e.ledger.ResidentRAM() != 0 || v.lastRetired != stored.Execution {
				t.Fatal("recovery lost original frame")
			}
		})
	}
}

func TestEnvironmentQualificationRecoveryUsesOriginalFrameAfterSourcePurge(t *testing.T) {
	store, source, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	v := newQualificationRuntimeVMM(&fakeVMM{destroyErr: errors.New("native retirement unavailable")})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { return nil }); err == nil {
		t.Fatal("missing native evidence passed")
	}
	stored, err := store.EnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := store.NodeSetLifecycle(t.Context(), stored.Execution.NodeID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	v.destroyErr = nil
	if err := e.RecoverEnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID); err != nil {
		t.Fatal(err)
	}
	if v.lastRetired != stored.Execution || e.ledger.ResidentRAM() != 0 {
		t.Fatal("source purge substituted current owner or released wrong reservation")
	}
	if err := e.RecoverEnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID); err != nil {
		t.Fatal("committed recovery replay failed", err)
	}
}
