package sched

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// The automation agent step (ADR-20261027090010 §9). The orchestrator admits one agent
// run per step attempt, as the agent API admits a seller's run, linked to the
// step and to a reserved workflow event, then parks the step on that event.
// agentd owns the run until it ends; a watcher then delivers the run's end as
// the event, and the step completes from it. Every write stays with its
// owner: the orchestrator writes workflow state, agentd the run.

// workflowAgentDeliveryBatch bounds the run ends delivered per pass.
const workflowAgentDeliveryBatch = 50

// WorkflowAgents admits agent step runs and reports their ends;
// *agentstore.Store implements it.
type WorkflowAgents interface {
	AdmitRun(ctx context.Context, a agentstore.Admission) (agentstore.Run, bool, error)
	UndeliveredWorkflowRunEnds(ctx context.Context, limit int) ([]agentstore.WorkflowRunEnd, error)
}

// WithAgents lets the orchestrator run agent steps. Without it an agent step
// fails before it starts.
func (o *WorkflowOrchestrator) WithAgents(agents WorkflowAgents) *WorkflowOrchestrator {
	o.agents = agents
	return o
}

// workflowRunHasAgentSteps reports whether a run's definition has an agent
// step. A definition that does not decode has none; initAndAdvanceRun fails
// the run for it.
func workflowRunHasAgentSteps(run *state.WorkflowRun) bool {
	if !bytes.Contains(run.DefinitionSnapshot, []byte(`"agent"`)) {
		return false
	}
	var spec api.WorkflowSpec
	return json.Unmarshal(run.DefinitionSnapshot, &spec) == nil && api.WorkflowUsesAgentSteps(spec)
}

func (o *WorkflowOrchestrator) executeAgentStep(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (bool, error) {
	if o.agents == nil {
		return o.failWorkflowStepBeforeStart(ctx, run, step, spec, nil, "workflow agent steps are unavailable on this scheduler")
	}
	attempt := step.Attempt + 1
	event := api.WorkflowAgentEventName(run.ID, step.StepName, attempt)
	if step.Status == state.WorkflowStepStatusPending {
		if message, err := o.admitAgentStepRun(ctx, run, step, spec, steps, attempt, event); err != nil || message != "" {
			if err != nil {
				return false, err
			}
			return o.failWorkflowStepBeforeStart(ctx, run, step, spec, nil, message)
		}
	}
	timeout := api.WorkflowAgentStepTimeout(spec)
	evt, deadline, err := o.store.ParkWorkflowEvent(ctx, run.ID, step.StepName, event, timeout)
	if err != nil {
		return false, err
	}
	if evt == nil && !time.Now().UTC().Before(deadline) {
		var timedOut bool
		if evt, timedOut, err = o.store.ResolveWorkflowEventWait(ctx, run.ID, step.StepName, event, timeout, false); err != nil || timedOut {
			return timedOut, err
		}
	}
	if evt == nil {
		if step.StartedAt == nil {
			o.emitAudit(ctx, events.WorkflowAwaitingEvent, map[string]any{"run_id": run.ID, "app_id": run.AppID,
				"workflow_name": run.WorkflowName, "step_name": step.StepName, "agent": spec.Agent, "timeout": timeout.String()})
		}
		return false, nil
	}
	return o.finishAgentStep(ctx, run, step, spec, evt.Payload)
}

// admitAgentStepRun renders the step's run request and admits it. Admission
// is idempotent per step attempt, so a scheduler that stops between
// admission and parking admits nothing twice. A non-empty message is a
// request that can never run, and fails the step.
func (o *WorkflowOrchestrator) admitAgentStepRun(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep, attempt int, event string) (string, error) {
	request, message := workflowAgentRequest(run, step, spec, steps)
	if message != "" {
		return message, nil
	}
	app, err := o.store.AppByID(ctx, run.AppID)
	if err != nil {
		return "", fmt.Errorf("read workflow app: %w", err)
	}
	account, err := o.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return "", fmt.Errorf("read workflow account: %w", err)
	}
	_, _, err = o.agents.AdmitRun(ctx, agentstore.Admission{AccountID: account.ID, Agent: spec.Agent, Version: request.Version,
		TenantID: run.PlatformTenantID, Trigger: "workflow", DedupeKey: workflowAgentDedupeKey(run.ID, step.StepName, attempt),
		Prompt: request.Prompt, Inputs: request.Inputs, Checkout: agentCheckout(request.Checkout), GitHub: agentGitHub(request.GitHub),
		Workflow: &agentstore.WorkflowLink{RunID: run.ID, Step: step.StepName, Event: event}, Limits: agentstore.PlanLimits(account.Plan)})
	if message := agentAdmissionFailure(err); message != "" {
		return message, nil
	}
	return "", err
}

// workflowAgentRequest renders the step input into a run request. Without
// an input template the workflow run's input becomes the run's inputs.
func workflowAgentRequest(run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (api.WorkflowAgentRunInput, string) {
	var request api.WorkflowAgentRunInput
	if len(spec.Input) == 0 {
		request.Inputs = run.Input
	} else {
		rendered, err := workflowStepInput(run, step, spec, steps, nil)
		if err != nil {
			return request, "agent step input could not be rendered: " + err.Error()
		}
		decoder := json.NewDecoder(bytes.NewReader(rendered))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return request, "agent step input must be a run request with version, prompt, inputs, checkout or github"
		}
	}
	if err := request.Validate(); err != nil {
		return request, err.Error()
	}
	return request, ""
}

// agentAdmissionFailure explains an admission refusal that retrying cannot
// fix; it is empty for success and for errors worth retrying.
func agentAdmissionFailure(err error) string {
	var limit *agentstore.LimitError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, agentstore.ErrNotFound):
		return "the agent step names an agent or version the account does not have"
	case errors.Is(err, agentstore.ErrTenantNotFound), errors.Is(err, agentstore.ErrTenantInactive):
		return "the workflow run's platform tenant cannot run agents"
	case errors.Is(err, agentstore.ErrDeliveryTargetMissing):
		return "the agent declares a GitHub comment; the step input names no github target"
	case errors.Is(err, agentstore.ErrGitHubUnavailable), errors.Is(err, agentstore.ErrCheckoutUnavailable):
		return "the account has no such GitHub installation"
	case errors.As(err, &limit):
		return fmt.Sprintf("agent run refused by the plan limit %s (%d)", limit.Limit, limit.Max)
	}
	return ""
}

// finishAgentStep completes the step from its run's end: the step succeeds
// with the run and fails with it.
func (o *WorkflowOrchestrator) finishAgentStep(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, payload json.RawMessage) (bool, error) {
	var out api.WorkflowAgentStepOutput
	if err := json.Unmarshal(payload, &out); err != nil || out.RunID == "" {
		return o.failWorkflowStepBeforeStart(ctx, run, step, spec, nil, "the agent run's end could not be read")
	}
	if out.State == agentstore.StateSucceeded {
		if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt, payload, nil); err != nil {
			return false, err
		}
		o.emitAudit(ctx, events.WorkflowStepSucceeded, map[string]any{"run_id": run.ID, "app_id": run.AppID,
			"workflow_name": run.WorkflowName, "step_name": step.StepName, "status": state.WorkflowStepStatusSucceeded, "agent_run_id": out.RunID})
		return true, nil
	}
	message := fmt.Sprintf("agent run %s ended %s", out.RunID, out.State)
	if out.Outcome != nil {
		message += ": " + out.Outcome.Code
	}
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusFailed, step.Attempt, payload, &message); err != nil {
		return false, err
	}
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, &message); err != nil {
		return false, err
	}
	o.emitAudit(ctx, events.WorkflowStepFailed, map[string]any{"run_id": run.ID, "app_id": run.AppID,
		"workflow_name": run.WorkflowName, "step_name": step.StepName, "status": state.WorkflowStepStatusFailed, "error": message})
	return true, nil
}

// DeliverAgentRunEnds delivers finished agent step runs to their workflow
// runs as the reserved events their steps wait for. The event is the
// receipt: a pass that stops early leaves the rest for the next one.
func (o *WorkflowOrchestrator) DeliverAgentRunEnds(ctx context.Context) error {
	if o.agents == nil {
		return nil
	}
	ends, err := o.agents.UndeliveredWorkflowRunEnds(ctx, workflowAgentDeliveryBatch)
	if err != nil {
		return err
	}
	var failed error
	for _, end := range ends {
		payload, err := json.Marshal(end.Output)
		if err == nil {
			err = o.ProcessEvent(ctx, end.Link.RunID, end.Link.Event, payload)
		}
		if err != nil {
			failed = errors.Join(failed, fmt.Errorf("deliver agent run %s: %w", end.Output.RunID, err))
		}
	}
	return failed
}

// workflowAgentDedupeKey is one step attempt's run dedupe key. The step name
// is hashed so any step name fits the key's bound.
func workflowAgentDedupeKey(runID, stepName string, attempt int) string {
	sum := sha256.Sum256([]byte(stepName))
	return fmt.Sprintf("workflow:%s:%s:%d", runID, hex.EncodeToString(sum[:8]), attempt)
}

func agentCheckout(c *api.AgentCheckout) *agentstore.Checkout {
	if c == nil {
		return nil
	}
	return &agentstore.Checkout{Repository: c.Repository, Commit: c.Commit, InstallationID: c.InstallationID}
}

func agentGitHub(g *api.AgentGitHubTarget) *agentstore.GitHubTarget {
	if g == nil {
		return nil
	}
	return &agentstore.GitHubTarget{Repository: g.Repository, Number: g.Number, InstallationID: g.InstallationID}
}
