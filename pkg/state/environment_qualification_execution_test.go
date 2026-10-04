// adr: 568 — retirement evidence does not rewrite a prior terminal outcome.
package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestQualificationRetirementPreservesPriorTerminalOutcomes(t *testing.T) {
	for _, state := range []State{StateParked, StateStopped, StateFailed, StateEvictingAccountDeleting} {
		store := NewMemStore()
		frame := EnvironmentQualificationExecution{InstanceID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), WakeID: uuid.NewString(), CleanupToken: uuid.NewString()}
		// A pre-contract terminal admission remains physically uncertain. Its
		// original parents need not exist for the cleanup capability to work.
		store.instances[frame.InstanceID] = Instance{ID: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID, NodeID: frame.NodeID, WakeID: frame.WakeID, State: string(state)}
		store.qualificationExecutions[frame.InstanceID] = EnvironmentQualificationExecutionStatus{Execution: frame, DispatchStarted: true}
		if err := store.RetireEnvironmentQualificationExecution(t.Context(), frame, EnvironmentQualificationRetirement{Kind: QualificationNeverDispatched}); !errors.Is(err, ErrConflict) {
			t.Fatal("legacy terminal state erased physical uncertainty", err)
		}
		// Fabricated storage evidence, not a native retirement assertion.
		proof := EnvironmentQualificationRetirement{Kind: QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
		if err := store.RetireEnvironmentQualificationExecution(t.Context(), frame, proof); err != nil {
			t.Fatal(err)
		}
		ins, err := store.InstanceByID(t.Context(), frame.InstanceID)
		status, statusErr := store.EnvironmentQualificationExecution(t.Context(), frame.InstanceID)
		if err != nil || statusErr != nil || ins.State != string(state) || ins.TerminalAt == nil || status.RetiredAt == nil || !ins.TerminalAt.Equal(*status.RetiredAt) {
			t.Fatalf("rewrote prior %s outcome or omitted retirement: %s %v %v", state, ins.State, err, statusErr)
		}
	}
	for _, state := range []State{StateColdBooting, StateWaking, StateRunning, StateDraining, StateWarm, StateSnapshotting, StateMigrating} {
		if retired, valid := qualificationRetiredState(state); !valid || retired != StateStopped {
			t.Fatalf("could not retire %s: %s %v", state, retired, valid)
		}
	}
}
