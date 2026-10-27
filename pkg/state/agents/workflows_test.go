// adr: 20261027090010
package agents_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

// An agent step's run carries its workflow link; once it ends, it is listed
// for delivery until its workflow event exists, and never once the workflow
// run is no longer active.
func TestWorkflowRunEndsAreListedUntilDelivered(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	workflowRun := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs VALUES ($1, 'awaiting_event')`, workflowRun); err != nil {
		t.Fatal(err)
	}
	event := api.WorkflowAgentEventName(workflowRun, "review", 1)
	link := &agents.WorkflowLink{RunID: workflowRun, Step: "review", Event: event}
	run := admit(t, s, agents.Admission{Trigger: "workflow", DedupeKey: "workflow:" + workflowRun + ":1", Workflow: link})
	again, existing, err := s.AdmitRun(ctx, agents.Admission{AccountID: account, Agent: "triage", Trigger: "workflow",
		DedupeKey: "workflow:" + workflowRun + ":1", Workflow: link, Limits: roomy})
	if err != nil || !existing || again.ID != run.ID {
		t.Fatalf("re-admission %+v %v %v", again, existing, err)
	}
	if ends, err := s.UndeliveredWorkflowRunEnds(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("an unfinished run was listed: %+v %v", ends, err)
	}
	if _, _, err := s.ClaimRun(ctx, "agentd-1", "tok-1", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRunning(ctx, run.ID, "tok-1", "agents-1", "agent-x", 1000); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if err := s.FinishRun(ctx, account, run.ID, "tok-1", agents.Finish{State: agents.StateSucceeded, ExitCode: &exit,
		Result: json.RawMessage(`{"verdict":"ship it"}`)}); err != nil {
		t.Fatal(err)
	}
	ends, err := s.UndeliveredWorkflowRunEnds(ctx, 10)
	var verdict map[string]string
	if err != nil || len(ends) != 1 || ends[0].Link != *link || ends[0].Output.RunID != run.ID || ends[0].Output.State != agents.StateSucceeded ||
		json.Unmarshal(ends[0].Output.Result, &verdict) != nil || verdict["verdict"] != "ship it" {
		t.Fatalf("ends %+v %v", ends, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_events (run_id, event_name) VALUES ($1, $2)`, workflowRun, event); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredWorkflowRunEnds(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("a delivered end was listed again: %+v %v", ends, err)
	}
	// A workflow run that ended meanwhile is not woken.
	other := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO workflow_runs VALUES ($1, 'failed')`, other); err != nil {
		t.Fatal(err)
	}
	cancelled := admit(t, s, agents.Admission{Trigger: "workflow", DedupeKey: "workflow:" + other + ":1",
		Workflow: &agents.WorkflowLink{RunID: other, Step: "review", Event: api.WorkflowAgentEventName(other, "review", 1)}})
	if _, err := s.CancelRun(ctx, account, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	if ends, err := s.UndeliveredWorkflowRunEnds(ctx, 10); err != nil || len(ends) != 0 {
		t.Fatalf("an ended workflow run was listed: %+v %v", ends, err)
	}
}
