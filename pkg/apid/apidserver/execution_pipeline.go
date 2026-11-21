package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithExecutionPipelinesEnabled(enabled bool) *server {
	s.executionPipelinesEnabled = enabled
	s.refreshRunAgentTools()
	return s
}
func (s *server) pipelineResponse(ctx context.Context, row state.ExecutionWorkflowJob) (api.ManagedExecutionWorkflowResponse, error) {
	out := managedExecutionWorkflowResponse(row)
	out.ID = row.ID
	jobs, ok := s.store.(state.ExecutionPipelineStore)
	if !ok {
		return out, nil
	}
	runs, ok := s.store.(state.ExecutionWorkflowStore)
	if !ok {
		return out, nil
	}
	var err error
	out.Pipeline, err = runpipeline.Response(ctx, row, jobs, runs)
	return out, err
}
func (s *server) createExecutionPipeline(w http.ResponseWriter, r *http.Request, acct state.Account, access executionAccess, request api.CreateManagedExecutionWorkflowRequest) {
	if r.URL.Path != "/v2/execution-workflows" {
		api.WriteProblem(w, api.ErrValidation("task pipelines require POST /v2/execution-workflows"))
		return
	}
	principal := access.principal
	if principal == nil {
		id := acct.ID
		principal = &id
	}
	submit := s.executionPipelineSubmitter(r, acct)
	row, replayed, err := submit.Submit(r.Context(), acct.ID, principal, access.broad, access.controlWide, request)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	out, err := s.pipelineResponse(r.Context(), row)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("pipeline status unavailable"))
		return
	}
	if !replayed {
		s.auditManagedExecutionWorkflow(r, acct.ID, row)
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *server) executionPipelineSubmitter(r *http.Request, acct state.Account) runpipeline.Submitter {
	jobs, _ := s.store.(state.ExecutionPipelineStore)
	receipts, _ := s.store.(state.ExecutionWorkflowJobStore)
	catalog := s.executionTaskService(r)
	return runpipeline.Submitter{Jobs: jobs, Receipts: receipts, Enabled: s.executionAPIEnabled && s.executionV2Enabled && s.executionPipelinesEnabled && s.executionTasksEnabled,
		Compiler: runpipeline.Compiler{Lookup: func(ctx context.Context, name string, version int) (state.ExecutionTaskVersion, error) {
			if catalog.Store == nil {
				return state.ExecutionTaskVersion{}, state.ErrExecutionTaskUnavailable
			}
			return catalog.Store.ExecutionTaskVersionByName(ctx, acct.ID, name, version)
		}, Open: catalog.Open},
		Validate: func(plan api.CreateManagedExecutionWorkflowRequest) error {
			for _, step := range plan.Steps {
				if err := s.requireExecutionRequestFeatures(step.Request); err != nil {
					return err
				}
			}
			if err := validateManagedExecutionWorkflowRequests(plan, acct); err != nil {
				return api.ErrValidation(err.Error())
			}
			return nil
		},
		Seal: func(raw []byte) ([]byte, string, error) {
			recipients, kid, err := workflowPlanRecipients()
			if err != nil || !holdsIdentityFor(accountIdentities(), kid) {
				return nil, "", fmt.Errorf("workflow identity unavailable")
			}
			sealed, err := executionpayload.SealWorkflowPlanTo(recipients, raw)
			return sealed, kid, err
		},
	}
}

func (s *server) cancelExecutionPipeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	access, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	workflow := r.PathValue("workflow_id")
	if workflow == "" || api.ValidateExecutionWorkflowMetadata(workflow, "") != nil {
		api.WriteProblem(w, api.ErrValidation("invalid workflow_id"))
		return
	}
	jobs, ok := s.store.(state.ExecutionPipelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("pipeline storage unavailable"))
		return
	}
	var principal *string
	if !access.broad {
		principal = access.principal
	}
	if err := jobs.CancelExecutionPipeline(r.Context(), acct.ID, workflow, principal, time.Now().UTC()); err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	s.getExecutionWorkflow(w, r, acct)
}
func (s *server) pipelineWorker() runpipeline.Worker {
	jobs, _ := s.store.(state.ExecutionPipelineStore)
	runs, _ := s.store.(state.ExecutionStore)
	workflows, _ := s.store.(state.ExecutionWorkflowStore)
	return runpipeline.Worker{Jobs: jobs, Runs: runs, WorkflowRuns: workflows, Enabled: func() bool {
		return s.executionAPIEnabled && s.executionV2Enabled && s.executionPipelinesEnabled && s.executionTasksEnabled
	},
		Open: func(ctx context.Context, j state.ExecutionWorkflowJob) (runpipeline.Plan, error) {
			raw, err := executionpayload.DecodeWorkflowPlan(ctx, accountIdentities(), j.SealedPlan, j.PayloadKID)
			if err != nil {
				return runpipeline.Plan{}, err
			}
			defer clear(raw)
			var plan runpipeline.Plan
			err = json.Unmarshal(raw, &plan)
			return plan, err
		},
		Admit: s.admitExecutionPipelineStep}
}
func (s *server) admitExecutionPipelineStep(ctx context.Context, c state.ExecutionWorkflowJobClaim, meta state.ExecutionPipeline, i, attempt int, previous string, request api.CreateExecutionRequest, agentFiles []runfiles.AgentFilePin) error {
	account, err := s.store.AccountByID(ctx, c.AccountID)
	if err != nil {
		return err
	}
	if err = s.requireExecutionRequestFeatures(request); err != nil {
		return runpipeline.PermanentError("step feature unavailable")
	}
	request.WorkflowID = c.WorkflowID
	request.StepLabel = runpipeline.Label(c.PlanID, i, attempt)
	access := executionAccess{principal: c.RunsPrincipalID, broad: meta.Policy.Wide, controlWide: meta.Policy.ControlWide}
	if access.principal != nil {
		access.principalID = *access.principal
	}
	params, _, err := s.prepareExecutionAdmission(ctx, executionStoreAdmissionReader{s.store}, account, access, request)
	if err != nil {
		if len(agentFiles) != 0 && errors.Is(err, state.ErrExecutionFileNotReady) {
			return runpipeline.PermanentError("scoped input upload is no longer available")
		}
		var p *api.Problem
		if errors.As(err, &p) && p.Status >= 400 && p.Status < 500 && p.Status != 429 {
			return runpipeline.PermanentError("step admission refused")
		}
		return err
	}
	// The file store checks the prepared storage key again while atomically
	// admitting the attempt, so a replacement after this check fails closed.
	for _, pin := range agentFiles {
		if !slices.ContainsFunc(params.Request.StoredInputs, pin.Matches) {
			return runpipeline.PermanentError("scoped input upload generation changed")
		}
	}
	if params.DeadlineAt.After(meta.DeadlineAt) {
		params.DeadlineAt = meta.DeadlineAt
	}
	params.Pipeline = &state.ExecutionPipelineAdmission{JobID: c.ID, LeaseToken: c.ClaimToken, Step: i, Attempt: attempt, PreviousRunID: previous}
	if pin := meta.Policy.Steps[i].Task; pin != nil {
		endpoint := ""
		if meta.Policy.Callback != nil {
			endpoint = meta.Policy.Callback.EndpointID
		}
		params.Task = &state.ExecutionTaskAdmission{PipelineEndpointID: endpoint, VersionID: pin.ID, RequestDigest: sha256.Sum256([]byte(c.PlanID + request.StepLabel))}
	}
	runs, ok := s.store.(state.ExecutionStore)
	if !ok {
		return api.ErrCapacity("Run storage unavailable")
	}
	_, err = runs.CreateExecution(ctx, params)
	if len(agentFiles) != 0 && errors.Is(err, state.ErrExecutionFileNotReady) {
		return runpipeline.PermanentError("scoped input upload is no longer available")
	}
	return err
}
func (s *server) driveExecutionPipelines(ctx context.Context, owner string) {
	worker := s.pipelineWorker()
	if worker.Jobs == nil || worker.Runs == nil || worker.WorkflowRuns == nil {
		return
	}
	for i := 0; i < executionWorkflowWorkerBatch; i++ {
		claim, err := worker.Jobs.ClaimExecutionPipeline(ctx, owner, time.Now().UTC(), executionWorkflowWorkerLease)
		if errors.Is(err, state.ErrNotFound) || errors.Is(err, context.Canceled) {
			return
		}
		if err != nil {
			if s.log != nil {
				s.log.Error("pipeline claim failed", "err", err)
			}
			return
		}
		if err = worker.Drive(ctx, claim); err != nil && s.log != nil {
			s.log.Error("pipeline continuation deferred", "workflow_id", claim.WorkflowID, "err", err)
		}
	}
}

func (s *server) refreshRunAgentTools() {
	if s.agentAPI != nil {
		s.agentAPI.TaskToolEnabled = s.executionTasksEnabled && s.executionAPIEnabled && s.executionV2Enabled
		s.agentAPI.PipelineToolEnabled = s.executionPipelinesEnabled && s.agentAPI.TaskToolEnabled
	}
}
