package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testManagedDataRepairResolutionState(t *testing.T, s repairTestStore, original DataRepairAdmission) {
	t.Helper()
	ctx := t.Context()
	create := func() Operation {
		t.Helper()
		in := original
		in.ID = uuid.NewString()
		in.Plan.PlanID = in.ID
		op, _, err := s.CreateDataRepairOperation(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	advance := func(op Operation, phase string, at *time.Time) Operation {
		t.Helper()
		got, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{
			ExpectedSequence: op.LatestSequence, Phase: phase, ApplyClosedAt: at,
		})
		if err != nil {
			t.Fatal(phase, err)
		}
		return got
	}
	for _, committed := range []bool{false, true} {
		op := create()
		op = advance(advance(op, api.DataRepairRehearsing, nil), api.DataRepairAwaitingApproval, nil)
		var err error
		op, err = s.DecideOperationApproval(ctx, OperationApprovalInput{AccountID: op.AccountID, OperationID: op.ID,
			Step: "repair", ApproverKind: api.OperationApproverAccount, ExpectedGeneration: 1, Decision: api.OperationApprovalApproved,
			Actor: api.OperationApprovalActor{Kind: api.OperationApproverAccount, ID: uuid.NewString()}})
		if err != nil {
			t.Fatal(err)
		}
		op = advance(advance(op, api.DataRepairApplying, nil), api.DataRepairUnknown, nil)
		op = advance(op, api.DataRepairResolving, nil)
		for _, phase := range []string{api.DataRepairApplying, api.DataRepairUnknown, api.DataRepairFailed, api.DataRepairClosed, api.DataRepairPrepared} {
			if _, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: phase}); !errors.Is(err, ErrConflict) {
				t.Fatal("unresolved reservation changed authority", phase, err)
			}
		}
		if _, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: uuid.NewString()}); !errors.Is(err, ErrConflict) {
			t.Fatal("unresolved reservation allowed replacement", err)
		}
		at := time.Now().UTC()
		if committed {
			op, err = s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairSucceeded, AppliedAt: &at})
			if err != nil || op.State != api.OperationSucceeded || op.DataRepair.ApplyClosedAt != nil {
				t.Fatal("committed outcome was not projected", err)
			}
			continue
		}
		op = advance(op, api.DataRepairClosed, &at)
		if op.State != api.OperationFailed || op.FailureCode != "repair_not_committed" || op.DataRepair.AppliedAt != nil || !op.DataRepair.ApplyClosedAt.Equal(at) {
			t.Fatal("confirmed closure was not projected")
		}
		childID := uuid.NewString()
		reserved, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: childID})
		if err != nil || reserved.State != api.OperationFailed || !reserved.DataRepair.ApplyClosedAt.Equal(at) {
			t.Fatal("closed outcome could not reserve replacement", err)
		}
	}
}
