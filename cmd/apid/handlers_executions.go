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

// executionResponse projects the durable, payload-free state row into the
// caller-facing DTO. The usage envelope is exposed only after terminal state;
// source and input never enter this projection.
func executionResponse(row state.Execution) api.ExecutionResponse {
	resp := api.ExecutionResponse{
		ID:              row.ID,
		Status:          row.Status,
		Runtime:         row.Runtime,
		Limits:          row.Limits,
		OutputTruncated: row.OutputTruncated,
		CreatedAt:       row.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
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
	if !s.requireExecutionAPI(w) {
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
	resolved, problem := request.Resolve(acct.Plan)
	if problem != nil {
		api.WriteProblem(w, problem)
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
		AccountID:     acct.ID,
		Request:       resolved,
		SourceBytes:   resolved.SourceBytes(),
		InputBytes:    len(resolved.Input),
		AdmittedAt:    admittedAt,
		DeadlineAt:    admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload: sealed,
		PayloadKID:    recipient.String(),
	}
	row, err := s.store.CreateExecution(r.Context(), params)
	if err != nil {
		s.writeExecutionCreateError(w, acct, err)
		return
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

	var (
		rows       []state.Execution
		statusRows func(limit, offset int) ([]state.Execution, error)
		err        error
	)
	if status == "" {
		statusRows = func(pageLimit, pageOffset int) ([]state.Execution, error) {
			return s.store.ListExecutions(r.Context(), acct.ID, pageLimit, pageOffset)
		}
	} else {
		statusRows = func(pageLimit, pageOffset int) ([]state.Execution, error) {
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
	if !s.requireExecutionAPI(w) {
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
	writeJSON(w, http.StatusOK, executionResponse(row))
}

// cancelExecution handles DELETE /v1/executions/{id}. Cancellation is
// idempotent at the state boundary: queued rows become terminal immediately;
// claimed rows carry a cancellation fence that schedd observes during
// teardown.
func (s *server) cancelExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.notFound(w, "no such execution")
		return
	}
	row, err := s.store.RequestExecutionCancellation(r.Context(), acct.ID, id, time.Now().UTC())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not cancel execution"))
		return
	}
	writeJSON(w, http.StatusAccepted, executionResponse(row))
}
