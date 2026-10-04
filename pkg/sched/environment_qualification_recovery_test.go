// adr: 532 — abandoned attempts retain capacity until exact native retirement.
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deliberately stale advisory discovery. The real store must reject cleanup
// while the original lease is current, before an attempt-aware native RPC.
type staleQualificationRecoveryStore struct {
	*state.MemStore
	row state.EnvironmentQualificationExecutionStatus
}

func (s *staleQualificationRecoveryStore) ListEnvironmentQualificationExecutionsForRecovery(context.Context, string, string, int) ([]state.EnvironmentQualificationExecutionStatus, error) {
	return []state.EnvironmentQualificationExecutionStatus{s.row}, nil
}

func TestEnvironmentQualificationRecoveryPageFencesLiveLeaseAndRetriesUncertainRetirement(t *testing.T) {
	store, source, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	injected := errors.New("native retirement unavailable")
	v := newQualificationRuntimeVMM(&fakeVMM{destroyErr: injected})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { return nil }); err == nil {
		t.Fatal("uncertain retirement was acknowledged")
	}
	stored, err := store.EnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	before := v.destroys
	stale, err := NewEngine(t.Context(), &staleQualificationRecoveryStore{MemStore: store, row: stored}, e.ledger, v, &fakeNotifier{}, "test-fc", testLog())
	if err != nil {
		t.Fatal(err)
	}
	page, err := stale.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, "", 1)
	if err != nil || page.Examined != 1 || page.Skipped != 1 || page.Retired != 0 || v.destroys != before || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Fatal("stale discovery revoked a current lease or released charge", page, err)
	}
	if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
		t.Fatal(err)
	}
	page, err = e.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, "", 1)
	if !errors.Is(err, injected) || page.Examined != 1 || page.Retired != 0 || page.NextCursor != stored.Execution.InstanceID || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Fatal("uncertain cleanup lost its holding or pinned the cursor", page, err)
	}
	end, err := e.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, page.NextCursor, 1)
	if err != nil || end.Examined != 0 || end.NextCursor != "" {
		t.Fatal("failed page could not advance to the next recovery pass", end, err)
	}
	v.destroyErr = nil
	page, err = e.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, end.NextCursor, 1)
	if err != nil || page.Examined != 1 || page.Retired != 1 || v.lastRetired != stored.Execution || e.ledger.ResidentRAM() != 0 {
		t.Fatal("recovery did not consume exact retirement evidence", page, err)
	}
	after := v.destroys
	page, err = e.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, "", 1)
	if err != nil || page.Examined != 0 || v.destroys != after {
		t.Fatal("retired execution reentered recovery", page, err)
	}
}

func TestEnvironmentQualificationRecoveryPageRefusesGenericDestroy(t *testing.T) {
	store, source, request := qualificationExecutionFixture(t, api.ExecutionModeRequest, time.Minute)
	v := newQualificationRuntimeVMM(&fakeVMM{destroyErr: errors.New("retirement interrupted")})
	e := newEngine(t, store, v, &fakeNotifier{}, "test-fc")
	if err := e.WithEnvironmentWorkloadQualificationRuntime(t.Context(), request, func(context.Context, state.Instance) error { return nil }); err == nil {
		t.Fatal("fixture lacks unfinished execution")
	}
	stored, err := store.EnvironmentQualificationExecution(t.Context(), request.ReservedInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProject(t.Context(), source.ProjectID); err != nil {
		t.Fatal(err)
	}
	legacy := &fakeVMM{}
	recovery, err := NewEngine(t.Context(), store, e.ledger, legacy, &fakeNotifier{}, "test-fc", testLog())
	if err != nil {
		t.Fatal(err)
	}
	page, err := recovery.RecoverEnvironmentQualificationExecutions(t.Context(), stored.Execution.NodeID, "", 1)
	if !errors.Is(err, state.ErrConflict) || page.Examined != 1 || page.Skipped != 0 || page.Retired != 0 || legacy.destroys != 0 || e.ledger.ResidentRAM() != 512+api.PerVMOverheadMB {
		t.Fatal("generic VMM released a dispatched holding", page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.RecoverEnvironmentQualificationExecutions(ctx, stored.Execution.NodeID, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled recovery page continued", err)
	}
}
