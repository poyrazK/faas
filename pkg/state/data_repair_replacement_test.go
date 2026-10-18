package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testManagedDataRepairReplacementState(t *testing.T, s repairTestStore, original DataRepairAdmission) {
	t.Helper()
	ctx := t.Context()
	create := func() Operation {
		t.Helper()
		in := original
		in.ID, in.Plan.PlanID = uuid.NewString(), ""
		in.Plan.PlanID = in.ID
		in.Plan.PlanExpiresAt = time.Now().Add(time.Minute)
		op, _, err := s.CreateDataRepairOperation(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	advance := func(op Operation, phase string) Operation {
		t.Helper()
		next, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: phase})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	approve := func(op Operation) Operation {
		t.Helper()
		op = advance(advance(op, api.DataRepairRehearsing), api.DataRepairAwaitingApproval)
		next, err := s.DecideOperationApproval(ctx, OperationApprovalInput{AccountID: op.AccountID, OperationID: op.ID,
			Step: "repair", ApproverKind: api.OperationApproverAccount, ExpectedGeneration: 1, Decision: api.OperationApprovalApproved,
			Actor: api.OperationApprovalActor{Kind: api.OperationApproverAccount, ID: uuid.NewString()}})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	parent := approve(create())
	approval := parent.DataRepair.Approval
	childID := uuid.NewString()
	in := original
	in.ID, in.Plan.PlanID, in.Plan.PlanDigest = childID, childID, strings.Repeat("c", 64)
	in.Plan.Candidates, in.Plan.Conflicts = 459, 41
	in.Plan.ReplacesPlanID, in.Plan.ReplacesPlanDigest = parent.ID, parent.DataRepair.PlanDigest
	if _, _, err := s.CreateDataRepairOperation(ctx, in); !errors.Is(err, ErrConflict) {
		t.Fatal("child admitted without parent reservation", err)
	}
	reserved, err := s.AdvanceDataRepairOperation(ctx, parent.AccountID, parent.ID, DataRepairAdvance{
		ExpectedSequence: parent.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: childID})
	if err != nil || reserved.DataRepair.ReplacementOperationID != childID || !reflect.DeepEqual(reserved.DataRepair.Approval, approval) {
		t.Fatal("reservation lost identity or approval history", err)
	}
	for _, phase := range []string{api.DataRepairApplying, api.DataRepairRehearsing, api.DataRepairFailed} {
		if _, err := s.AdvanceDataRepairOperation(ctx, parent.AccountID, parent.ID, DataRepairAdvance{ExpectedSequence: reserved.LatestSequence, Phase: phase}); !errors.Is(err, ErrConflict) {
			t.Fatal("reserved parent gained another authority", phase, err)
		}
	}
	for _, mode := range []string{"child", "binding", "digest", "selection"} {
		wrong := in
		switch mode {
		case "child":
			wrong.ID = uuid.NewString()
			wrong.Plan.PlanID = wrong.ID
		case "binding":
			wrong.Binding = strings.Repeat("d", 64)
		case "digest":
			wrong.Plan.ReplacesPlanDigest = strings.Repeat("e", 64)
		case "selection":
			wrong.Plan.Candidates, wrong.Plan.Conflicts = 461, 39
		}
		if _, _, err := s.CreateDataRepairOperation(ctx, wrong); !errors.Is(err, ErrConflict) {
			t.Fatal("changed reserved replacement admitted", mode, err)
		}
	}
	child, created, err := s.CreateDataRepairOperation(ctx, in)
	if err != nil || !created || child.DataRepair.Approval != nil || child.DataRepair.Phase != api.DataRepairPrepared || child.LatestSequence != 1 {
		t.Fatalf("replacement child inherited authority: %+v %v", child, err)
	}
	finished, err := s.OperationByID(ctx, parent.AccountID, "", parent.ID)
	if err != nil || finished.DataRepair.Phase != api.DataRepairReplaced || finished.State != api.OperationCancelled ||
		finished.LatestSequence != reserved.LatestSequence+1 || !reflect.DeepEqual(finished.DataRepair.Approval, approval) ||
		finished.DataRepair.PlanDigest != parent.DataRepair.PlanDigest || finished.DataRepair.Candidates != 460 {
		t.Fatalf("parent replacement history: %+v %v", finished, err)
	}
	replay, created, err := s.CreateDataRepairOperation(ctx, in)
	childJSON, _ := json.Marshal(child)
	replayJSON, _ := json.Marshal(replay)
	if err != nil || created || !bytes.Equal(childJSON, replayJSON) {
		t.Fatal("replacement admission replay changed history", err)
	}
	if _, err := s.AdvanceDataRepairOperation(ctx, child.AccountID, child.ID, DataRepairAdvance{ExpectedSequence: 1, Phase: api.DataRepairApplying}); !errors.Is(err, ErrConflict) {
		t.Fatal("child applied with parent approval", err)
	}
	for _, phase := range []string{api.DataRepairRehearsing, api.DataRepairApplying, api.DataRepairUnknown, api.DataRepairSucceeded} {
		op := create()
		if phase == api.DataRepairRehearsing {
			op = advance(op, phase)
		} else {
			op = advance(approve(op), api.DataRepairApplying)
			if phase == api.DataRepairUnknown || phase == api.DataRepairSucceeded {
				op = advance(op, api.DataRepairUnknown)
			}
			if phase == api.DataRepairSucceeded {
				at := time.Now()
				op, err = s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: phase, AppliedAt: &at})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := s.AdvanceDataRepairOperation(ctx, op.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence,
			Phase: api.DataRepairReplacing, ReplacementOperationID: uuid.NewString()}); !errors.Is(err, ErrConflict) {
			t.Fatal("unsettled or committed repair was replaced", phase, err)
		}
	}
	// Reservation and apply compete on the same sequence/row lock.
	competing := approve(create())
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, phase := range []string{api.DataRepairReplacing, api.DataRepairApplying} {
		wg.Go(func() {
			in := DataRepairAdvance{ExpectedSequence: competing.LatestSequence, Phase: phase}
			if phase == api.DataRepairReplacing {
				in.ReplacementOperationID = uuid.NewString()
			}
			_, err := s.AdvanceDataRepairOperation(ctx, competing.AccountID, competing.ID, in)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("replacement/apply claims", wins)
	}
	empty := original
	empty.ID = uuid.NewString()
	empty.Plan.PlanID = empty.ID
	empty.Plan.Candidates, empty.Plan.Conflicts = 0, 500
	emptyOp, _, err := s.CreateDataRepairOperation(ctx, empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceDataRepairOperation(ctx, emptyOp.AccountID, emptyOp.ID, DataRepairAdvance{
		ExpectedSequence: emptyOp.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: uuid.NewString()}); !errors.Is(err, ErrConflict) {
		t.Fatal("empty plan acquired a replacement reservation", err)
	}
	// An expired/failed parent retains its reservation across ordinary result
	// cleanup. Its original source may expire while the remote response is lost;
	// the already admitted parent's exact ownership is sufficient to recover.
	terminal := advance(create(), api.DataRepairFailed)
	lateID := uuid.NewString()
	terminal, err = s.AdvanceDataRepairOperation(ctx, terminal.AccountID, terminal.ID, DataRepairAdvance{
		ExpectedSequence: terminal.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: lateID})
	if err != nil || terminal.State != api.OperationFailed {
		t.Fatal("reservation revived terminal execution", err)
	}
	if _, err := s.(OperationRetentionStore).PruneOperationState(ctx, time.Now().Add(365*24*time.Hour), api.OperationRetentionPageMax); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperationByID(ctx, terminal.AccountID, "", original.Plan.SourceOperationID); !errors.Is(err, ErrNotFound) {
		t.Fatal("source fixture did not expire", err)
	}
	kept, err := s.OperationByID(ctx, terminal.AccountID, "", terminal.ID)
	if err != nil || kept.DataRepair.ReplacementOperationID != lateID {
		t.Fatal("pending replacement was pruned", err)
	}
	late := original
	late.ID, late.Plan.PlanID, late.Plan.PlanDigest = lateID, lateID, strings.Repeat("d", 64)
	late.Plan.Candidates, late.Plan.Conflicts = 459, 41
	late.Plan.ReplacesPlanID, late.Plan.ReplacesPlanDigest = terminal.ID, terminal.DataRepair.PlanDigest
	late.Plan.PlanExpiresAt = time.Now().Add(-time.Second)
	recovered, _, err := s.CreateDataRepairOperation(ctx, late)
	if err != nil || recovered.State != api.OperationFailed || recovered.DataRepair.Phase != api.DataRepairFailed ||
		recovered.DataRepair.Approval != nil || !recovered.DataRepair.PlanExpiresAt.Equal(late.Plan.PlanExpiresAt) {
		t.Fatalf("expired child recovery refreshed authority: %+v %v", recovered, err)
	}
}
