// adr: 20261016123221
package acceptance_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func telemetryAt(t *testing.T, s any, now time.Time) state.OperationTelemetry {
	t.Helper()
	snapshot, err := s.(state.OperationTelemetryStore).OperationTelemetry(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testOperationTelemetry(t *testing.T, s operationLimitStore) {
	ctx := t.Context()
	f := operationLimitSetup(t, s, api.OperationLimits{DeadlineSeconds: 60})
	ops := []state.Operation{f.admit(t, "one"), f.admit(t, "two"), f.admit(t, "three")}
	now := time.Now()
	got := telemetryAt(t, s, now)
	if got.States[api.OperationAccepted].Count != 3 || got.Overdue != 0 || len(got.Outcomes) != 0 || got.OutcomeUnknownAccounts != 0 {
		t.Fatalf("fresh admissions: %+v", got)
	}
	if age := now.Sub(got.States[api.OperationAccepted].OldestUpdate); age < 0 || age > time.Minute {
		t.Fatalf("oldest accepted update %s ago", age)
	}

	// A lost response makes the outcome unknown.
	claimed, err := s.ClaimInvocationWithCap(ctx, ops[0].CurrentInvocationID, f.instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, claimed.ID, "response lost", time.Millisecond, 10, state.WithClaimAttempt(claimed.Attempts)); err != nil {
		t.Fatal(err)
	}
	got = telemetryAt(t, s, time.Now())
	unknown := state.OperationOutcomeKey{EventType: "reconciliation_required", FailureCode: "execution_outcome_unknown"}
	if got.States[api.OperationAccepted].Count != 2 || got.States[api.OperationRequiresReconciliation].Count != 1 ||
		got.Outcomes[unknown] != 1 || got.OutcomeUnknownAccounts != 1 {
		t.Fatalf("after a lost response: %+v", got)
	}

	// Four minutes past the deadline the two active Operations are overdue;
	// the one awaiting reconciliation is not.
	if got = telemetryAt(t, s, ops[1].DeadlineAt.Add(4*time.Minute)); got.Overdue != 2 {
		t.Fatalf("four minutes past the deadline counted %d overdue", got.Overdue)
	}
	if got = telemetryAt(t, s, ops[1].DeadlineAt.Add(time.Minute)); got.Overdue != 0 {
		t.Fatalf("one minute past the deadline counted %d overdue", got.Overdue)
	}
	// Sixteen minutes on, the outcome has left the fifteen-minute window.
	if got = telemetryAt(t, s, time.Now().Add(16*time.Minute)); got.Outcomes[unknown] != 0 || got.OutcomeUnknownAccounts != 0 {
		t.Fatalf("outcome window: %v accounts=%d", got.Outcomes, got.OutcomeUnknownAccounts)
	}
}

func TestOperationTelemetry(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testOperationTelemetry(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		s, _ := pgStore(t)
		testOperationTelemetry(t, s)
	})
}

func TestOperationTelemetryCountsWaitKinds(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f := waitOperationSetup(t, store, api.OperationApproverAccount, time.Hour)
		op := f.start(t)
		f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		got := telemetryAt(t, store, time.Now())
		if got.States[api.OperationWaiting].Count != 1 || got.Waiting[api.OperationWaitApproval] != 1 || got.Waiting[api.OperationWaitTimer] != 0 {
			t.Fatalf("approval wait: %+v", got)
		}
	})
}
