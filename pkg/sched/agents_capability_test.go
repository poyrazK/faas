// adr: 20261027090010 — schedd holds agent work while the internal agents
// capability (ADR-20261023115741) is off for the app's account: an
// agent-target trigger is not polled, and a run with an agent step is put
// back undispatched. Held work resumes when the capability is released.
package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestAgentTriggerHeldWhileAgentsIsOff(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "agent-held", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var appID, accountID pgtype.UUID
	if appID.Scan(app.ID) != nil || accountID.Scan(account.ID) != nil {
		t.Fatal("ids")
	}
	trigger := sqlc.Trigger{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, AppID: appID, AccountID: accountID,
		Kind: string(api.TriggerKindKafka), Slug: "orders", Enabled: true, Agent: pgtype.Text{String: "order-review", Valid: true}}
	sched.InstallPollerFactory(t, "kafka", func(sqlc.Trigger) (sched.TriggerSource, error) {
		t.Fatal("an agent-target trigger was polled while agents is off")
		return nil, nil
	})
	// agents is internal: with no operator value it is off for everyone.
	capabilities, err := capflags.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	agents := &fakeAgents{}
	loop := sched.MakeLoopForDLQ().WithCapabilitiesForTest(store, capabilities)
	loop.WithTriggerAgents(agents)
	if err := loop.DispatchOneTrigger(ctx, trigger, store, nil); err != nil {
		t.Fatal(err)
	}
	if len(agents.admissions) != 0 {
		t.Fatalf("admissions %d, want none while agents is off", len(agents.admissions))
	}
}

func TestWorkflowRunWithAgentStepHeldWhileAgentsIsOff(t *testing.T) {
	sched.GuardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		run := agentStepRun(t, store)
		capabilities, err := capflags.Load(nil)
		if err != nil {
			t.Fatal(err)
		}
		decide := func(ctx context.Context, appID string) bool {
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				t.Fatal(err)
			}
			acct, err := store.AccountByID(ctx, app.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			return capabilities.Enabled("agents", platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
		}
		agents, executor := &fakeAgents{}, &recordingExecutor{}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil).WithAgents(agents).WithAgentsAllowedForTest(decide)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetWorkflowRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(agents.admissions) != 0 || len(executor.inputs) != 0 {
			t.Fatalf("admissions %d calls %d, want none while agents is off", len(agents.admissions), len(executor.inputs))
		}
		if got.Status != state.WorkflowRunStatusPending || !got.ScheduledFor.After(time.Now().Add(30*time.Second)) {
			t.Fatalf("run %s due %s, want pending and deferred", got.Status, got.ScheduledFor)
		}
	})
}
