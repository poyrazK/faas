package state

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type repairTestStore = contractTestStore

func TestManagedDataRepairState(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s repairTestStore = NewMemStore()
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				s = NewPgStore(pool)
			}
			ctx := t.Context()
			acct, err := s.CreateAccount(ctx, "repair-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if mem, ok := s.(*MemStore); ok {
				old := acct.ID
				acct.ID = uuid.MustParse(old).String()
				delete(mem.accounts, old)
				mem.accounts[acct.ID] = acct
			}
			app, err := s.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "repair-state"})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:repair"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "repair-customer", "Repair Customer", 100)
			if err != nil {
				t.Fatal(err)
			}
			def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "import", ProgressStages: []string{"importing"}, Method: "POST", Path: "/import", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
			if err != nil {
				t.Fatal(err)
			}
			source, _, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "source", Input: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CancelOperation(ctx, acct.ID, "", source.ID, 1); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			in := DataRepairAdmission{AccountID: acct.ID, AppID: app.ID, ID: id, Binding: strings.Repeat("b", 64), Plan: api.OperationDataRepair{PlanID: id, PlanDigest: strings.Repeat("a", 64), SourceOperationID: source.ID, Candidates: 460, Conflicts: 40, PlanExpiresAt: time.Now().Add(time.Minute)}}
			forged := in
			forged.Plan.PhaseStartedAt = time.Now().UTC().Add(-time.Hour)
			if _, _, err := s.CreateDataRepairOperation(ctx, forged); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("caller supplied phase clock accepted: %v", err)
			}
			op, created, err := s.CreateDataRepairOperation(ctx, in)
			if err != nil || !created {
				t.Fatalf("admit %+v %v", op, err)
			}
			if op.CurrentInvocationID != "" || op.DefinitionID != "" || op.DataRepairID != op.ID {
				t.Fatal("repair has customer execution authority")
			}
			if op.DataRepair.PhaseStartedAt.IsZero() {
				t.Fatal("admission lost phase clock")
			}
			if _, created, err = s.CreateDataRepairOperation(ctx, in); err != nil || created {
				t.Fatalf("replay: %v %v", created, err)
			}
			changed := in
			changed.Plan.PlanDigest = strings.Repeat("c", 64)
			if _, _, err = s.CreateDataRepairOperation(ctx, changed); !errors.Is(err, ErrOperationInputConflict) {
				t.Fatalf("changed identity: %v", err)
			}
			if _, err = s.AdvanceDataRepairOperation(ctx, acct.ID, op.ID, DataRepairAdvance{ExpectedSequence: 1, Phase: api.DataRepairApplying}); !errors.Is(err, ErrConflict) {
				t.Fatalf("unapproved apply: %v", err)
			}
			if _, err = s.CancelOperation(ctx, acct.ID, "", op.ID, 1); !errors.Is(err, ErrConflict) {
				t.Fatalf("generic cancel: %v", err)
			}
			if _, err = s.RecoverOperation(ctx, acct.ID, "", op.ID, api.OperationRecoveryRequest{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("generic recover: %v", err)
			}
			advance := func(phase string) {
				t.Helper()
				var err error
				previous := op.DataRepair.PhaseStartedAt
				op, err = s.AdvanceDataRepairOperation(ctx, acct.ID, op.ID, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: phase})
				if err != nil {
					t.Fatalf("advance %s: %v", phase, err)
				}
				if op.DataRepair.PhaseStartedAt.IsZero() || op.DataRepair.PhaseStartedAt.Before(previous) {
					t.Fatalf("phase %s lost its transition clock", phase)
				}
			}
			advance(api.DataRepairRehearsing)
			if _, err = s.AdvanceDataRepairOperation(ctx, acct.ID, op.ID, DataRepairAdvance{ExpectedSequence: 1, Phase: api.DataRepairAwaitingApproval}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale claim: %v", err)
			}
			advance(api.DataRepairAwaitingApproval)
			decision := OperationApprovalInput{AccountID: acct.ID, OperationID: op.ID, Step: "repair", ApproverKind: api.OperationApproverAccount, ExpectedGeneration: 1, Decision: api.OperationApprovalApproved, Actor: api.OperationApprovalActor{Kind: api.OperationApproverAccount, ID: uuid.NewString()}}
			wrong := decision
			wrong.PlatformTenantID = tenant.ID
			wrong.ApproverKind = api.OperationApproverCustomer
			wrong.Actor.Kind = api.OperationApproverCustomer
			if _, err = s.DecideOperationApproval(ctx, wrong); !errors.Is(err, ErrOperationApproverMismatch) {
				t.Fatalf("customer approval: %v", err)
			}
			op, err = s.DecideOperationApproval(ctx, decision)
			if err != nil {
				t.Fatal(err)
			}
			before, phaseStarted := op.LatestSequence, op.DataRepair.PhaseStartedAt
			op, err = s.DecideOperationApproval(ctx, decision)
			if err != nil || op.LatestSequence != before || !op.DataRepair.PhaseStartedAt.Equal(phaseStarted) {
				t.Fatalf("approval replay: %v", err)
			}
			decision.Decision = api.OperationApprovalRejected
			if _, err = s.DecideOperationApproval(ctx, decision); !errors.Is(err, ErrOperationApprovalDecided) {
				t.Fatalf("approval changed: %v", err)
			}
			// Two coordinators cannot own the apply transition simultaneously.
			var wg sync.WaitGroup
			var mu sync.Mutex
			wins := 0
			sequence := op.LatestSequence
			for range 2 {
				wg.Go(func() {
					_, err := s.AdvanceDataRepairOperation(ctx, acct.ID, id, DataRepairAdvance{ExpectedSequence: sequence, Phase: api.DataRepairApplying})
					mu.Lock()
					defer mu.Unlock()
					if err == nil {
						wins++
					} else if !errors.Is(err, ErrConflict) {
						t.Errorf("claim: %v", err)
					}
				})
			}
			wg.Wait()
			if wins != 1 {
				t.Fatalf("apply claims: %d", wins)
			}
			op, err = s.OperationByID(ctx, acct.ID, "", id)
			if err != nil {
				t.Fatal(err)
			}
			advance(api.DataRepairUnknown)
			at := time.Now().UTC()
			op, err = s.AdvanceDataRepairOperation(ctx, acct.ID, id, DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairSucceeded, AppliedAt: &at})
			if err != nil {
				t.Fatal(err)
			}
			if op.State != api.OperationSucceeded || op.DataRepair.AppliedAt == nil {
				t.Fatal("receipt outcome not projected")
			}
			page, err := s.ListAccountOperations(ctx, acct.ID, api.OperationListOptions{AppID: app.ID, Scope: dep.Scope, Name: "gregale-data-repair"})
			if err != nil || len(page.Operations) != 1 {
				t.Fatalf("history: %+v %v", page, err)
			}
			executions, err := s.OperationExecutions(ctx, acct.ID, id, 0, 10)
			if err != nil || len(executions.Executions) != 1 || executions.Executions[0].DataRepairID != id || executions.Executions[0].InvocationID != "" {
				t.Fatalf("execution: %+v %v", executions, err)
			}
			timeline, err := s.OperationTimeline(ctx, acct.ID, id, OperationTimelineCursor{}, 10)
			if err != nil || len(timeline.Response.Entries) != 7 {
				t.Fatalf("timeline: %+v %v", timeline, err)
			}
			if pg, ok := s.(*PgStore); ok {
				t.Run("scheduled_rehearsal", func(t *testing.T) { testDataRepairRehearsalState(t, pg, in) })
			}
			t.Run("resolution", func(t *testing.T) { testManagedDataRepairResolutionState(t, s, in) })
			t.Run("replacement", func(t *testing.T) { testManagedDataRepairReplacementState(t, s, in) })
		})
	}
}

func testDataRepairRehearsalState(t *testing.T, s *PgStore, in DataRepairAdmission) {
	t.Helper()
	ctx := t.Context()
	create := func() Operation {
		t.Helper()
		in.ID = uuid.NewString()
		in.Plan.PlanID = in.ID
		in.Plan.PlanExpiresAt = time.Now().UTC().Add(5 * time.Minute)
		op, _, err := s.CreateDataRepairOperation(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op := create()
	queued, task, err := s.EnqueueDataRepairRehearsal(ctx, in.AccountID, op.ID, op.LatestSequence, strings.Repeat("c", 64))
	if err != nil || task.Phase != RepairRehearsalQueued {
		t.Fatalf("enqueue: %+v %v", task, err)
	}
	other := create()
	if _, _, err = s.EnqueueDataRepairRehearsal(ctx, in.AccountID, other.ID, other.LatestSequence, strings.Repeat("c", 64)); !errors.Is(err, ErrConflict) {
		t.Fatalf("account capacity: %v", err)
	}
	if _, err = s.AdvanceDataRepairOperation(ctx, in.AccountID, op.ID, DataRepairAdvance{ExpectedSequence: queued.LatestSequence, Phase: api.DataRepairPrepared}); !errors.Is(err, ErrConflict) {
		t.Fatal("generic recovery released scheduled task", err)
	}
	if _, err = s.CancelDataRepairRehearsal(ctx, in.AccountID, op.ID, queued.LatestSequence); err != nil {
		t.Fatal(err)
	}
	task, err = s.DataRepairRehearsal(ctx, task.ID)
	if err != nil || task.Phase != RepairRehearsalCancelled || task.Frame != nil {
		t.Fatal("queued cancellation", err)
	}
	op = other
	op, task, err = s.EnqueueDataRepairRehearsal(ctx, in.AccountID, op.ID, op.LatestSequence, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	spec := core.RunnerSpec{BaseKey: "repair/base", LayerKey: "repair/layer", KernelSHA256: strings.Repeat("1", 64), BaseSHA256: strings.Repeat("2", 64), LayerSHA256: strings.Repeat("3", 64)}
	f := core.RehearsalFrame{TaskID: task.ID, OperationID: op.ID, Scope: task.Scope, Binding: task.Binding, NodeID: node.ID, Incarnation: uuid.NewString(), PlanID: task.PlanID, PlanDigest: task.PlanDigest, ChecksSHA256: task.ChecksSHA256, PlanExpiresAt: task.PlanExpiresAt, Deadline: time.Now().UTC().Add(time.Minute), Plan: api.PlanPro, Runner: spec}
	changed := f
	changed.Binding = strings.Repeat("e", 64)
	if _, err = s.ClaimDataRepairRehearsal(ctx, task.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed binding claimed", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 2 {
		wg.Go(func() {
			_, e := s.ClaimDataRepairRehearsal(ctx, task.ID, f)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				wins++
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("claims=%d", wins)
	}
	task, err = s.DataRepairRehearsal(ctx, task.ID)
	if err != nil || task.PhaseStartedAt.IsZero() {
		t.Fatal("running phase clock missing", err)
	}
	runningSince := task.PhaseStartedAt
	task, err = s.DispatchDataRepairRehearsal(ctx, task.ID, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !task.PhaseStartedAt.Equal(runningSince) {
		t.Fatal("dispatch reset running phase age")
	}
	if _, err = s.DispatchDataRepairRehearsal(ctx, task.ID, strings.Repeat("d", 64)); !errors.Is(err, ErrConflict) {
		t.Fatal("same task dispatched twice", err)
	}
	task, err = s.MarkDataRepairRehearsalCleanup(ctx, task.ID)
	if err != nil || task.PhaseStartedAt.Before(runningSince) || task.FailureCode != "repair_cleanup_unconfirmed" {
		t.Fatal("cleanup phase clock/reason missing", err)
	}
	cleanupSince := task.PhaseStartedAt
	task, err = s.MarkDataRepairRehearsalCleanup(ctx, task.ID)
	if err != nil || !task.PhaseStartedAt.Equal(cleanupSince) {
		t.Fatal("retirement retry reset cleanup age", err)
	}
	at := time.Now().UTC()
	f = *task.Frame
	e := core.Evidence{Passed: true, TaskID: task.ID, NodeID: node.ID, Runner: core.MicroVMRunner, RunnerSHA256: spec.Digest(), PlanDigest: f.PlanDigest, ChecksSHA256: f.ChecksSHA256, SnapshotSHA256: f.SnapshotSHA256, RetiredAt: &at}
	bad := e
	bad.RetiredAt = nil
	if _, err = s.RetireDataRepairRehearsal(ctx, task.ID, &bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unretired evidence accepted", err)
	}
	task, err = s.RetireDataRepairRehearsal(ctx, task.ID, &e)
	if err != nil || task.Phase != RepairRehearsalPublishing || task.FailureCode != "" || task.PhaseStartedAt.Before(cleanupSince) {
		t.Fatal("publication intent", err)
	}
	op, err = s.OperationByID(ctx, in.AccountID, "", op.ID)
	if err != nil || op.DataRepair.Phase != api.DataRepairRehearsing {
		t.Fatal("approval preceded source evidence", err)
	}
	op, err = s.CompleteDataRepairRehearsal(ctx, task.ID)
	if err != nil || op.DataRepair.Phase != api.DataRepairAwaitingApproval {
		t.Fatal("completion", err)
	}
	replay, err := s.CompleteDataRepairRehearsal(ctx, task.ID)
	if err != nil || replay.LatestSequence != op.LatestSequence {
		t.Fatal("completion replay added events", err)
	}
	// A cancellation changes the record without changing the active phase.
	op = create()
	op, task, err = s.EnqueueDataRepairRehearsal(ctx, in.AccountID, op.ID, op.LatestSequence, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	f.TaskID, f.OperationID, f.PlanID, f.PlanDigest = task.ID, op.ID, task.PlanID, task.PlanDigest
	f.PlanExpiresAt, f.SnapshotSHA256 = task.PlanExpiresAt, ""
	task, err = s.ClaimDataRepairRehearsal(ctx, task.ID, f)
	if err != nil {
		t.Fatal(err)
	}
	phaseStarted := task.PhaseStartedAt
	op, err = s.OperationByID(ctx, in.AccountID, "", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelDataRepairRehearsal(ctx, in.AccountID, op.ID, op.LatestSequence); err != nil {
		t.Fatal(err)
	}
	task, err = s.DataRepairRehearsal(ctx, task.ID)
	if err != nil || !task.CancelRequested || task.Phase != RepairRehearsalRunning || !task.PhaseStartedAt.Equal(phaseStarted) {
		t.Fatal("cancellation reset phase age", err)
	}
	if _, err := s.RetireDataRepairRehearsal(ctx, task.ID, nil); err != nil {
		t.Fatal(err)
	}
}
