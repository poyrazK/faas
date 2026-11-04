// adr: 20261027090010 — on Postgres with the real agent store, an agent step's run
// that succeeds resumes its step and ends the workflow run.
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestWorkflowAgentStepSucceedsWithTheAgentStore(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store, agents := state.NewPgStore(pool), agentstore.New(pool)
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "agent-step-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agents.CreateAgent(ctx, account.ID, "pr-triage", "", agentstore.VersionSpec{Harness: agentstore.Harness{Preset: "claude-code"},
		PromptTemplate: "Triage {{inputs.title}}", TimeoutSec: 900}, 10); err != nil {
		t.Fatal(err)
	}
	spec := api.WorkflowSpec{Name: "triage", Steps: []api.WorkflowStepSpec{{Name: "review", Agent: "pr-triage",
		Input: json.RawMessage(`{"inputs":{"number":"{{input.data.pull_request.number}}","title":"{{input.data.pull_request.title}}"}}`)}}}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot, ScheduledFor: time.Now().UTC().Add(-time.Second),
		Input: json.RawMessage(`{"data":{"pull_request":{"number":4321,"title":"Retry the wake"}}}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	orchestrator := sched.NewWorkflowOrchestrator(store, &recordingExecutor{}, nil, nil, nil).WithAgents(agents)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stepStatus(t, store, run.ID, "review"); got != state.WorkflowStepStatusAwaitingEvent {
		t.Fatalf("review %s, want awaiting_event", got)
	}

	claim, found, err := agents.ClaimRun(ctx, "agentd-1", "tok-1", time.Minute, func(string) int { return 5 })
	if err != nil || !found {
		t.Fatalf("claim %+v %v %v", claim, found, err)
	}
	if err := agents.MarkRunning(ctx, claim.RunID, "tok-1", "default-local", "agent-"+claim.RunID, 200_000); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if err := agents.FinishRun(ctx, account.ID, claim.RunID, "tok-1", agentstore.Finish{State: agentstore.StateSucceeded, ExitCode: &exit,
		Result: json.RawMessage(`{"summary":"Triage Retry the wake","risk":"low","labels":["keyless"],"relay_status":401,` +
			`"relay_body":"{\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"invalid x-api-key\"}}"}`)}); err != nil {
		t.Fatal(err)
	}

	deliverCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := orchestrator.DeliverAgentRunEnds(deliverCtx); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	got, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil || got.Status != state.WorkflowRunStatusSucceeded {
		t.Fatalf("workflow run %+v %v; review %s", got, err, stepStatus(t, store, run.ID, "review"))
	}
}
