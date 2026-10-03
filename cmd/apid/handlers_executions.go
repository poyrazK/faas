package main

// Public disposable execution admission and lifecycle handlers (ADR-171).
// The API owns validation and persistence; schedd owns claim, restore,
// execution, and teardown. Source/input are sealed before they cross the
// apid boundary and are never returned by customer reads.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/executionprofiles"
	"github.com/onebox-faas/faas/pkg/state"
)

func executionAPIDisabledProblem() *api.Problem {
	return api.NewProblem(
		http.StatusNotImplemented,
		api.CodeNotImplemented,
		"Execution API unavailable",
		"the disposable execution runtime is not enabled on this control-plane host",
	).WithDocs("https://gregale.dev/docs/executions")
}

func (s *server) requireExecutionAPI(w http.ResponseWriter) bool {
	if s.executionAPIEnabled {
		return true
	}
	api.WriteProblem(w, executionAPIDisabledProblem())
	return false
}

func requireExecutionEntitlement(w http.ResponseWriter, acct state.Account) bool {
	if acct.Plan.ExecutionsAllowed() {
		return true
	}
	api.WriteProblem(w, api.ErrExecutionsNotAllowed(acct.Plan))
	return false
}

// executionResponse projects the durable, payload-free state row into the
// caller-facing DTO. The usage envelope is exposed only after terminal state;
// source and input never enter this projection.
func executionResponse(row state.Execution) api.ExecutionResponse {
	resp := api.ExecutionResponse{
		WorkflowID:         row.WorkflowID,
		StepLabel:          row.StepLabel,
		Profile:            row.Profile.Normalized(),
		RuntimeImageDigest: row.RuntimeImageDigest,
		Packages:           executionprofiles.Packages(row.Profile),
		ID:                 row.ID,
		Status:             row.Status,
		Runtime:            row.Runtime,
		Limits:             row.Limits,
		OutputTruncated:    row.OutputTruncated,
		CreatedAt:          row.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	resp.Artifacts = api.CloneExecutionArtifacts(row.Artifacts)
	if len(row.Result) > 0 {
		resp.Result = append(json.RawMessage(nil), row.Result...)
	}
	if row.Stdout != "" {
		resp.Stdout = row.Stdout
	}
	if row.Stderr != "" {
		resp.Stderr = row.Stderr
	}
	if row.ExitCode != nil {
		value := *row.ExitCode
		resp.ExitCode = &value
	}
	if row.StartedAt != nil && !row.StartedAt.IsZero() {
		value := row.StartedAt.UTC().Format(time.RFC3339Nano)
		resp.StartedAt = &value
	}
	if row.FinishedAt != nil && !row.FinishedAt.IsZero() {
		value := row.FinishedAt.UTC().Format(time.RFC3339Nano)
		resp.FinishedAt = &value
	}
	if row.Status.Terminal() {
		usage := api.ExecutionUsage{
			WallTimeMS:   row.Usage.WallTimeMS,
			CPUTimeMS:    row.Usage.CPUTimeMS,
			PeakMemoryMB: row.Usage.PeakMemoryMB,
		}
		resp.Usage = &usage
	}
	if row.FailureCode != nil && row.FailureMessage != nil {
		resp.Failure = &api.ExecutionFailure{
			Code:    *row.FailureCode,
			Message: *row.FailureMessage,
		}
	}
	return resp
}

// createExecution handles POST /v1/executions. It performs every caller-
// controlled check before sealing or writing durable state, then persists the
// immutable deadline and encrypted payload in one store call.
func (s *server) createExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) {
		return
	}
	if !s.requireExecutionAPI(w) {
		return
	}
	access, accessProblem := executionAccessForRequest(r)
	if accessProblem != nil {
		writeExecutionAccessError(w, accessProblem)
		return
	}

	var request api.CreateExecutionRequest
	if err := decodeJSONSized(r, &request, api.ExecutionSealedPayloadMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.ExecutionSealedPayloadMaxBytes, api.ExecutionSealedPayloadMaxBytes+1))
			return
		}
		api.WriteProblem(w, api.ErrValidation("invalid execution request body"))
		return
	}
	if err := api.ValidateExecutionWorkflowMetadata(request.WorkflowID, request.StepLabel); err != nil {
		api.WriteProblem(w, api.NewProblem(
			http.StatusUnprocessableEntity,
			api.CodeExecutionPayloadInvalid,
			"Invalid workflow metadata",
			err.Error(),
		))
		return
	}
	artifactGrants, problem := s.resolveExecutionArtifactInputs(r.Context(), acct, access, &request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	resolved, problem := request.Resolve(acct.Plan)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	integrationIDs, err := api.NormalizeExecutionIntegrationIDs(request.IntegrationIDs)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("integration_ids must contain at most 25 distinct UUIDs"))
		return
	}

	if setSecretRecipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient is not loaded; refusing to seal execution payload"))
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient is not loaded; refusing to seal execution payload"))
		return
	}
	sealed, err := executionpayload.SealRequest(recipient, resolved)
	if err != nil {
		if errors.Is(err, executionpayload.ErrInvalid) {
			api.WriteProblem(w, api.NewProblem(
				http.StatusUnprocessableEntity,
				api.CodeExecutionPayloadInvalid,
				"Invalid execution payload",
				"source or input could not be sealed under the execution payload contract",
			).WithDocs("https://gregale.dev/docs/executions#request"))
			return
		}
		if s.log != nil {
			s.log.Error("execution payload sealing failed", "account_id", acct.ID, "err", err)
		}
		api.WriteProblem(w, api.ErrCapacity("execution payload could not be sealed on this host"))
		return
	}

	admittedAt := time.Now().UTC()
	params := state.CreateExecutionParams{
		AccountID:              acct.ID,
		WorkflowID:             request.WorkflowID,
		StepLabel:              request.StepLabel,
		OutboundIntegrationIDs: integrationIDs,
		RunsPrincipalID:        access.principal,
		Request:                resolved,
		SourceBytes:            resolved.SourceBytes(),
		InputBytes:             len(resolved.Input),
		AdmittedAt:             admittedAt,
		DeadlineAt:             admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload:          sealed,
		PayloadKID:             recipient.String(),
	}
	for _, grant := range artifactGrants {
		params.ArtifactGrantRedemptions = append(params.ArtifactGrantRedemptions, state.ExecutionArtifactGrantRedemption{
			GrantID: grant.ID, TokenHash: grant.TokenHash,
		})
	}
	row, err := s.store.CreateExecution(r.Context(), params)
	if err != nil {
		s.writeExecutionCreateError(w, acct, err)
		return
	}
	s.auditExecutionRequest(r, acct.ID, "execution.created", row)
	for _, grant := range artifactGrants {
		s.auditExecutionArtifactGrant(r, acct.ID, "execution.artifact_grant_redeemed", grant, row.ID)
	}
	writeJSON(w, http.StatusAccepted, executionResponse(row))
}

// listExecutions handles GET /v1/executions. Results are account-scoped and
// newest-first; the status filter is pushed into the store so pagination does
// not skip matching rows hidden behind other execution states.
func (s *server) listExecutions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	access, accessProblem := executionAccessForRequest(r)
	if accessProblem != nil {
		writeExecutionAccessError(w, accessProblem)
		return
	}
	limitProblem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 50, 200, "executions")
	if limitProblem != nil {
		api.WriteProblem(w, limitProblem)
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			api.WriteProblem(w, api.NewProblem(
				http.StatusBadRequest,
				api.CodeValidation,
				"Bad offset",
				"offset must be a non-negative integer",
			))
			return
		}
		offset = parsed
	}

	status := api.ExecutionStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "" && !status.Valid() {
		api.WriteProblem(w, api.NewProblem(
			http.StatusBadRequest,
			api.CodeValidation,
			"Bad execution status",
			"status must be one of queued, restoring, running, succeeded, failed, timed_out, out_of_memory, or cancelled",
		))
		return
	}
	workflowID := r.URL.Query().Get("workflow_id")
	hasWorkflowID := r.URL.Query().Has("workflow_id")
	if hasWorkflowID {
		if err := api.ValidateExecutionWorkflowMetadata(workflowID, ""); err != nil {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
			return
		}
	}

	var (
		rows       []state.Execution
		statusRows func(limit, offset int) ([]state.Execution, error)
		err        error
	)
	principalStore, principalStoreOK := s.store.(state.ExecutionPrincipalListStore)
	if !access.broad && !hasWorkflowID && !principalStoreOK {
		api.WriteProblem(w, api.ErrCapacity("this store cannot enforce Run ownership filters"))
		return
	}
	if hasWorkflowID {
		workflowStore, ok := s.store.(state.ExecutionWorkflowStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("this store cannot enforce workflow ownership filters"))
			return
		}
		statusRows = func(pageLimit, pageOffset int) ([]state.Execution, error) {
			var principalID *string
			if !access.broad {
				principalID = access.principal
			}
			return workflowStore.ListExecutionsByWorkflow(r.Context(), acct.ID, workflowID, principalID, status, pageLimit, pageOffset)
		}
	} else if status == "" {
		statusRows = func(pageLimit, pageOffset int) ([]state.Execution, error) {
			if !access.broad {
				return principalStore.ListExecutionsByPrincipal(r.Context(), acct.ID, access.principalID, pageLimit, pageOffset)
			}
			return s.store.ListExecutions(r.Context(), acct.ID, pageLimit, pageOffset)
		}
	} else {
		statusRows = func(pageLimit, pageOffset int) ([]state.Execution, error) {
			if !access.broad {
				return principalStore.ListExecutionsByPrincipalStatus(r.Context(), acct.ID, access.principalID, status, pageLimit, pageOffset)
			}
			return s.store.ListExecutionsByStatus(r.Context(), acct.ID, status, pageLimit, pageOffset)
		}
	}
	rows, err = statusRows(limit, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list executions"))
		return
	}
	items := make([]api.ExecutionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, executionResponse(row))
	}
	nextOffset := -1
	if len(items) == limit {
		// A full page is not sufficient to prove that another page exists.
		// Probe the first row after this page so next_offset=-1 remains an
		// exact end-of-results signal without adding a total count to the
		// customer-facing response.
		probe, probeErr := statusRows(1, offset+limit)
		if probeErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list executions"))
			return
		}
		if len(probe) != 0 {
			nextOffset = offset + len(items)
		}
	}
	writeJSON(w, http.StatusOK, api.ExecutionListResponse{
		Executions: items,
		Limit:      limit,
		Offset:     offset,
		NextOffset: nextOffset,
	})
}

func (s *server) writeExecutionCreateError(w http.ResponseWriter, acct state.Account, err error) {
	switch {
	case errors.Is(err, state.ErrExecutionArtifactGrantUnavailable):
		api.WriteProblem(w, artifactGrantUnavailableProblem())
		return
	case errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable):
		api.WriteProblem(w, api.NewProblem(
			http.StatusUnprocessableEntity,
			api.CodeExecutionPayloadInvalid,
			"Requested integration unavailable",
			"one or more requested integrations are not active, credentialed, and explicitly enabled for Runs",
		))
		return
	case errors.Is(err, state.ErrExecutionWorkflowStepExists):
		api.WriteProblem(w, api.NewProblem(
			http.StatusConflict,
			api.CodeExecutionWorkflowStepExists,
			"Workflow step already submitted",
			"this agent workflow step already has a Run receipt; read the existing workflow before retrying",
		))
		return
	case errors.Is(err, state.ErrExecutionsNotAllowed):
		api.WriteProblem(w, api.ErrExecutionsNotAllowed(acct.Plan))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such account")
		return
	}
	var quota *state.ExecutionQuotaError
	if errors.As(err, &quota) {
		api.WriteProblem(w, api.NewProblem(
			http.StatusTooManyRequests,
			api.CodePlanLimitConcur,
			"Execution concurrency limit reached",
			fmt.Sprintf("the %s plan allows %d concurrent executions; %d would be active",
				acct.Plan, quota.Limit, quota.Observed),
		).WithLimit(int64(quota.Limit), int64(quota.Observed)).WithDocs("https://gregale.dev/docs/plans#executions"))
		return
	}
	api.WriteProblem(w, api.ErrInternal("could not persist execution admission"))
}

// getExecution handles GET /v1/executions/{id}. Store lookups are account-
// scoped, so a cross-account id is indistinguishable from a missing id.
func (s *server) getExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) {
		return
	}
	if !s.requireExecutionAPI(w) {
		return
	}
	access, accessProblem := executionAccessForRequest(r)
	if accessProblem != nil {
		writeExecutionAccessError(w, accessProblem)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.notFound(w, "no such execution")
		return
	}
	row, err := s.store.ExecutionByID(r.Context(), acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load execution"))
		return
	}
	if !requireExecutionOwnership(w, s, access, row) {
		return
	}
	writeJSON(w, http.StatusOK, executionResponse(row))
}

// cancelExecution handles DELETE /v1/executions/{id}. Cancellation is
// idempotent at the state boundary: queued rows become terminal immediately;
// claimed rows carry a cancellation fence that schedd observes during
// teardown.
func (s *server) cancelExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) {
		return
	}
	if !s.requireExecutionAPI(w) {
		return
	}
	access, accessProblem := executionAccessForRequest(r)
	if accessProblem != nil {
		writeExecutionAccessError(w, accessProblem)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.notFound(w, "no such execution")
		return
	}
	row, err := s.store.ExecutionByID(r.Context(), acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load execution"))
		return
	}
	if !requireExecutionOwnership(w, s, access, row) {
		return
	}
	row, err = s.store.RequestExecutionCancellation(r.Context(), acct.ID, id, time.Now().UTC())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not cancel execution"))
		return
	}
	s.auditExecutionRequest(r, acct.ID, "execution.cancel_requested", row)
	writeJSON(w, http.StatusAccepted, executionResponse(row))
}
