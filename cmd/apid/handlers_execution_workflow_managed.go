package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/executionworkflow"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createManagedExecutionWorkflow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) || !s.requireExecutionAPI(w) {
		return
	}
	access, problem := executionAccessForRequest(r)
	if problem != nil {
		writeExecutionAccessError(w, problem)
		return
	}
	var request api.CreateManagedExecutionWorkflowRequest
	if err := decodeJSONSized(r, &request, api.ExecutionWorkflowManagedPlanMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.ExecutionWorkflowManagedPlanMaxBytes, api.ExecutionWorkflowManagedPlanMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("invalid managed workflow request body"))
		return
	}
	if err := api.ValidateCreateManagedExecutionWorkflow(request); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid managed workflow", err.Error()))
		return
	}
	if err := validateManagedExecutionWorkflowRequests(request, acct); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeExecutionPayloadInvalid, "Invalid managed workflow step", err.Error()))
		return
	}
	store, ok := s.store.(state.ExecutionWorkflowJobStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("this store cannot persist managed execution workflows"))
		return
	}
	if setSecretRecipient == nil || mfaIdentities == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient and identity are required for managed workflows"))
		return
	}
	identities := mfaIdentities()
	if len(identities) == 0 {
		api.WriteProblem(w, api.ErrCapacity("host age recipient and identity are required for managed workflows"))
		return
	}
	canonical, err := json.Marshal(request)
	if err != nil || len(canonical) > api.ExecutionWorkflowManagedPlanMaxBytes {
		api.WriteProblem(w, api.ErrValidation("managed workflow plan exceeds the maximum encoded size"))
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient and identity are required for managed workflows"))
		return
	}
	keyPairReady := false
	for _, identity := range identities {
		if identity != nil && identity.Recipient().String() == recipient.String() {
			keyPairReady = true
			break
		}
	}
	if !keyPairReady {
		api.WriteProblem(w, api.ErrCapacity("the host age recipient has no matching identity for managed workflows"))
		return
	}
	sealed, err := executionpayload.SealWorkflowPlan(recipient, canonical)
	if err != nil {
		if s.log != nil {
			s.log.Error("managed execution workflow sealing failed", "account_id", acct.ID, "err", err)
		}
		api.WriteProblem(w, api.ErrCapacity("managed workflow plan could not be sealed on this host"))
		return
	}
	digest := sha256.Sum256(canonical)
	planID := hex.EncodeToString(digest[:12])
	now := time.Now().UTC()
	ownerPrincipal := access.principal
	if ownerPrincipal == nil {
		// Account-wide session workflows still need the receipt uniqueness fence
		// used to recover safely if a worker loses its lease after admission.
		accountPrincipal := acct.ID
		ownerPrincipal = &accountPrincipal
	}
	row, err := store.CreateExecutionWorkflowJob(r.Context(), state.CreateExecutionWorkflowJobParams{
		AccountID: acct.ID, RunsPrincipalID: ownerPrincipal,
		WorkflowID: request.WorkflowID, PlanID: planID,
		StepCount: len(request.Steps), SealedPlan: sealed, PayloadKID: recipient.String(), CreatedAt: now,
	})
	if err != nil {
		if errors.Is(err, state.ErrExecutionWorkflowQueueFull) {
			api.WriteProblem(w, api.ErrCapacity("the account's managed workflow queue is full; wait for a workflow to finish before submitting more"))
			return
		}
		if errors.Is(err, state.ErrExecutionWorkflowJobExists) {
			if existing, getErr := store.ExecutionWorkflowJobByKey(r.Context(), acct.ID, request.WorkflowID, ownerPrincipal); getErr == nil && existing.PlanID == planID {
				writeJSON(w, http.StatusAccepted, managedExecutionWorkflowResponse(existing))
				return
			}
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Workflow already submitted", "this workflow id already has a managed plan for the current Runs principal"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("managed workflow could not be persisted"))
		return
	}
	s.auditManagedExecutionWorkflow(r, acct.ID, row)
	writeJSON(w, http.StatusAccepted, managedExecutionWorkflowResponse(row))
}

func validateManagedExecutionWorkflowRequests(request api.CreateManagedExecutionWorkflowRequest, acct state.Account) error {
	limits, allowed := acct.Plan.ExecutionLimits()
	if !allowed || !limits.Allowed {
		return api.ErrExecutionsNotAllowed(acct.Plan)
	}
	for index, step := range request.Steps {
		if _, err := executionworkflow.CompileResultSchema(step.ResultSchema); err != nil {
			return fmt.Errorf("step %q: %w", step.Label, err)
		}
		stepRequest := step.Request
		if len(step.ArtifactInputs) > api.ExecutionArtifactInputMaxFiles {
			return fmt.Errorf("step %q has %d artifact inputs; limit is %d", step.Label, len(step.ArtifactInputs), api.ExecutionArtifactInputMaxFiles)
		}
		if len(stepRequest.Files)+len(step.ArtifactInputs) > api.ExecutionBundleMaxFiles {
			return fmt.Errorf("step %q would contain %d bundle files; limit is %d", step.Label, len(stepRequest.Files)+len(step.ArtifactInputs), api.ExecutionBundleMaxFiles)
		}
		if len(step.ArtifactInputs) > 0 && (strings.TrimSpace(stepRequest.Source) != "" || strings.TrimSpace(stepRequest.Entrypoint) == "") {
			return fmt.Errorf("step %q artifact inputs require an entrypoint files bundle", step.Label)
		}
		for _, input := range step.ArtifactInputs {
			if len(input.Name) > api.ExecutionArtifactMaxPathBytes || len(input.Path) > api.ExecutionArtifactMaxPathBytes {
				return fmt.Errorf("step %q artifact input path exceeds the %d-byte limit", step.Label, api.ExecutionArtifactMaxPathBytes)
			}
		}
		if step.InputFromPreviousResult {
			stepRequest.Input = json.RawMessage("null")
		}
		resolved, problem := stepRequest.Resolve(acct.Plan)
		if problem != nil {
			return fmt.Errorf("step %q: %s", step.Label, problem.Detail)
		}
		if resolved.SourceBytes() > limits.MaxSourceBytes {
			return fmt.Errorf("step %q source is %d bytes; account limit is %d", step.Label, resolved.SourceBytes(), limits.MaxSourceBytes)
		}
		if !step.InputFromPreviousResult && !step.IncludeDependencyResults && len(resolved.Input) > limits.MaxInputBytes {
			return fmt.Errorf("step %q input is %d bytes; account limit is %d", step.Label, len(resolved.Input), limits.MaxInputBytes)
		}
		if len(step.Request.OutputFiles) > api.ExecutionArtifactMaxFiles {
			return fmt.Errorf("step %q requests more than %d output files", step.Label, api.ExecutionArtifactMaxFiles)
		}
		if index == 0 && step.InputFromPreviousResult {
			return fmt.Errorf("step %q cannot use a previous result because it is first", step.Label)
		}
		if _, err := api.NormalizeExecutionIntegrationIDs(step.Request.IntegrationIDs); err != nil {
			return fmt.Errorf("step %q integration_ids must contain at most 25 distinct UUIDs", step.Label)
		}
	}
	return nil
}

func managedExecutionWorkflowResponse(row state.ExecutionWorkflowJob) api.ManagedExecutionWorkflowResponse {
	lastError := strings.TrimSpace(row.LastError)
	lastError = strings.TrimPrefix(lastError, managedWorkflowFatalErrorPrefix)
	return api.ManagedExecutionWorkflowResponse{
		WorkflowID: row.WorkflowID, PlanID: row.PlanID, Status: row.Status,
		StepCount: row.StepCount, NextStep: row.NextStep,
		Error: lastError, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *server) auditManagedExecutionWorkflow(r *http.Request, accountID string, row state.ExecutionWorkflowJob) {
	actor := auditActor
	data := map[string]any{"workflow_id": row.WorkflowID, "plan_id": row.PlanID, "step_count": row.StepCount}
	if _, key, ok := authmw.AccountFromContext(r); ok {
		if key != nil {
			actor = "api:" + key.ID
			data["actor_via"] = "api"
			data["actor_key_id"] = key.ID
		} else {
			actor = "dashboard:" + accountID
			data["actor_via"] = "dashboard"
		}
	}
	s.audit.EmitAs(r.Context(), actor, "execution.workflow.created", &accountID, data)
}
