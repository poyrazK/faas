package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var queueBindingNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func queueBindingResponse(row state.QueueBinding) api.QueueBindingResponse {
	return api.QueueBindingResponseFromRow(api.QueueBindingRow{
		ID: row.ID, AppID: row.AppID, AccountID: row.AccountID,
		Name: row.Name, QueueName: row.QueueName, Mode: row.Mode,
		WorkloadClass: string(row.WorkloadClass), Enabled: row.Enabled,
		MaxConcurrency: row.MaxConcurrency, RetryPolicyJSON: row.RetryPolicyJSON,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
}

func queueBindingProblem(detail string) *api.Problem {
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid queue binding", detail)
}

func validateQueueBindingName(field, value string) *api.Problem {
	if !queueBindingNameRE.MatchString(value) {
		return queueBindingProblem(fmt.Sprintf("%s must match [a-z][a-z0-9-]{0,62}", field))
	}
	return nil
}

func validateQueueBindingMode(mode string) *api.Problem {
	if mode != "pull" && mode != "push" {
		return queueBindingProblem(`mode must be "pull" or "push"`)
	}
	return nil
}

func validateQueueBindingClass(class string) *api.Problem {
	if class != string(state.WorkloadClassWorker) && class != string(state.WorkloadClassJob) {
		return queueBindingProblem(`workload_class must be "worker" or "job"`)
	}
	return nil
}

func validateQueueBindingConcurrency(value int) *api.Problem {
	if value < 1 || value > 10000 {
		return queueBindingProblem("max_concurrency must be between 1 and 10000")
	}
	return nil
}

func marshalQueueBindingRetryPolicy(policy *api.RetryPolicyDTO) ([]byte, *api.Problem) {
	if policy == nil {
		return []byte(`{}`), nil
	}
	if policy.MaxAttempts < 0 || policy.MaxAttempts > 25 {
		return nil, queueBindingProblem("retry_policy.max_attempts must be between 0 and 25")
	}
	if policy.BaseSeconds < 0 || policy.BaseSeconds > 3600 || policy.MaxSeconds < 0 || policy.MaxSeconds > 86400 || policy.JitterSeconds < 0 || policy.JitterSeconds > 1 {
		return nil, queueBindingProblem("retry_policy seconds must be non-negative and jitter_seconds must be between 0 and 1")
	}
	if policy.MaxSeconds > 0 && policy.BaseSeconds > policy.MaxSeconds {
		return nil, queueBindingProblem("retry_policy.base_seconds must not exceed max_seconds")
	}
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, queueBindingProblem("retry_policy is not encodable")
	}
	return b, nil
}

func (s *server) listQueueBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := s.store.ListQueueBindingsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list queue bindings"))
		return
	}
	out := make([]api.QueueBindingResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, queueBindingResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateQueueBindingRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, queueBindingProblem(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		api.WriteProblem(w, queueBindingProblem("name is required"))
		return
	}
	if prob := validateQueueBindingName("name", req.Name); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateQueueBindingName("queue_name", req.QueueName); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = "pull"
	}
	if prob := validateQueueBindingMode(mode); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	class := req.WorkloadClass
	if class == "" {
		class = string(state.WorkloadClassWorker)
	}
	if prob := validateQueueBindingClass(class); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if app.WorkloadClass != "" && string(app.WorkloadClass) != class {
		api.WriteProblem(w, queueBindingProblem(fmt.Sprintf("workload_class %q does not match app workload class %q", class, app.WorkloadClass)))
		return
	}
	concurrency := req.MaxConcurrency
	if concurrency == 0 {
		concurrency = 1
	}
	if prob := validateQueueBindingConcurrency(concurrency); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	policy, prob := marshalQueueBindingRetryPolicy(req.RetryPolicy)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := s.store.CreateQueueBinding(r.Context(), state.QueueBinding{
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, QueueName: req.QueueName,
		Mode: mode, WorkloadClass: state.WorkloadClass(class), Enabled: enabled,
		MaxConcurrency: concurrency, RetryPolicyJSON: policy,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue binding already exists", "name and queue_name must be unique within an app"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not create queue binding"))
		return
	}
	s.audit.Emit(r.Context(), "queue.binding_created", &acct.ID, map[string]any{"binding_id": row.ID, "app_id": app.ID, "name": row.Name, "queue_name": row.QueueName, "mode": row.Mode})
	writeJSON(w, http.StatusCreated, queueBindingResponse(row))
}

func (s *server) getQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	row, err := s.store.QueueBindingByID(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "queue binding not found")
		return
	}
	writeJSON(w, http.StatusOK, queueBindingResponse(row))
}

func (s *server) updateQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateQueueBindingRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, queueBindingProblem(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := s.store.QueueBindingByID(r.Context(), acct.ID, app.ID, id); err != nil {
		s.notFound(w, "queue binding not found")
		return
	}
	params := state.UpdateQueueBindingParams{QueueName: req.QueueName, Enabled: req.Enabled, MaxConcurrency: req.MaxConcurrency}
	if req.QueueName != nil {
		if prob := validateQueueBindingName("queue_name", *req.QueueName); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	if req.Mode != nil {
		if prob := validateQueueBindingMode(*req.Mode); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.Mode = req.Mode
	}
	if req.WorkloadClass != nil {
		if prob := validateQueueBindingClass(*req.WorkloadClass); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		class := state.WorkloadClass(*req.WorkloadClass)
		if app.WorkloadClass != "" && app.WorkloadClass != class {
			api.WriteProblem(w, queueBindingProblem(fmt.Sprintf("workload_class %q does not match app workload class %q", class, app.WorkloadClass)))
			return
		}
		params.WorkloadClass = &class
	}
	if req.MaxConcurrency != nil {
		if prob := validateQueueBindingConcurrency(*req.MaxConcurrency); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	if req.RetryPolicy != nil {
		policy, prob := marshalQueueBindingRetryPolicy(req.RetryPolicy)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.RetryPolicyJSON = &policy
	}
	row, err := s.store.UpdateQueueBinding(r.Context(), acct.ID, app.ID, id, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "queue binding not found")
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue binding already exists", "queue_name must be unique within an app"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update queue binding"))
		return
	}
	s.audit.Emit(r.Context(), "queue.binding_updated", &acct.ID, map[string]any{"binding_id": row.ID, "app_id": app.ID, "name": row.Name})
	writeJSON(w, http.StatusOK, queueBindingResponse(row))
}

func (s *server) deleteQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := s.store.DeleteQueueBinding(r.Context(), acct.ID, app.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "queue binding not found")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete queue binding"))
		return
	}
	s.audit.Emit(r.Context(), "queue.binding_deleted", &acct.ID, map[string]any{"binding_id": r.PathValue("id"), "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}
