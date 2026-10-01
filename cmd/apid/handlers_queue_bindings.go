package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

var queueBindingNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func queueBindingResponse(row state.QueueBinding) api.QueueBindingResponse {
	return api.QueueBindingResponseFromRow(api.QueueBindingRow{
		Environment: row.DeploymentScope, EnvironmentID: row.EnvironmentID,
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

// Push delivery invokes an HTTP handler through gatewayd. A function can
// therefore consume a push binding without pretending to be a long-lived
// worker; pull bindings retain their worker/job contract.
func validateQueueBindingTarget(mode, class string, app state.App) *api.Problem {
	if class == string(state.WorkloadClassHTTP) {
		if mode != "push" || app.Type != state.AppTypeFunction {
			return queueBindingProblem(`workload_class "http" requires push mode and a function app`)
		}
	} else if prob := validateQueueBindingClass(class); prob != nil {
		return prob
	}
	if app.WorkloadClass != "" && string(app.WorkloadClass) != class {
		return queueBindingProblem(fmt.Sprintf("workload_class %q does not match app workload class %q", class, app.WorkloadClass))
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

func queueBindingTriggerID(ctx context.Context, store state.Store, appID, bindingID string) (string, error) {
	bindingUUID, err := uuid.Parse(bindingID)
	if err != nil {
		return "", state.ErrInvalidArgument
	}
	triggers, err := store.ListTriggersForApp(ctx, appID)
	if err != nil {
		return "", err
	}
	for _, trigger := range triggers {
		if trigger.Kind != string(api.TriggerKindQueue) || !trigger.Source.Valid || trigger.Source.String != string(state.InvocationQueue) {
			continue
		}
		if trigger.QueueBindingID.Valid && trigger.QueueBindingID.Bytes == bindingUUID {
			return trigger.ID.String(), nil
		}
	}
	return "", nil
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

// getQueueBindingStatus returns the durable consumer projection together with
// binding-scoped queue counters and the scheduler's last-known poll health.
// Fleet-wide liveness and alerting remain observable through schedd metrics.
func (s *server) getQueueBindingStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	binding, err := s.store.QueueBindingByID(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Queue binding not found", "no queue binding exists with that id"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not load queue binding status"))
		return
	}
	stats, err := s.store.QueueStateForBinding(r.Context(), app.ID, binding.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load queue binding queue state"))
		return
	}

	resp := api.QueueBindingStatusResponse{
		Environment:      binding.DeploymentScope,
		EnvironmentID:    binding.EnvironmentID,
		BindingID:        binding.ID,
		Name:             binding.Name,
		QueueName:        binding.QueueName,
		Mode:             binding.Mode,
		WorkloadClass:    string(binding.WorkloadClass),
		Enabled:          binding.Enabled,
		ConsumerState:    "external",
		ConsumerLiveness: "external",
		GeneratedAt:      time.Now().UTC(),
		Depth:            stats.Depth,
		InFlight:         stats.InFlight,
		DeadLetter:       stats.DeadLetter,
	}
	if !stats.OldestPendingAt.IsZero() {
		oldest := stats.OldestPendingAt
		age := int64(resp.GeneratedAt.Sub(oldest).Seconds())
		if age < 0 {
			age = 0
		}
		resp.OldestPendingAt = &oldest
		resp.OldestPendingAgeSeconds = &age
	}

	if binding.Mode == "push" {
		resp.ConsumerState = "not_configured"
		resp.ConsumerLiveness = "not_observed"
		resp.ConsumerStateReason = "push_consumer_not_provisioned"
		triggerID, triggerErr := queueBindingTriggerID(r.Context(), s.store, app.ID, binding.ID)
		if triggerErr != nil {
			api.WriteProblem(w, api.ErrInternal("could not load queue consumer status"))
			return
		}
		if triggerID != "" {
			resp.TriggerID = triggerID
			trigger, triggerErr := s.store.TriggerByID(r.Context(), triggerID)
			if triggerErr != nil {
				api.WriteProblem(w, api.ErrInternal("could not load queue consumer status"))
				return
			}
			consumerActive := binding.Enabled && trigger.Enabled
			if consumerActive {
				resp.ConsumerState = "active"
				resp.ConsumerStateReason = "push_consumer_enabled"
			} else {
				resp.ConsumerState = "paused"
				resp.ConsumerStateReason = "push_consumer_disabled"
			}
			if healthStore, ok := s.store.(state.TriggerConsumerHealthStore); ok {
				health, healthErr := healthStore.TriggerConsumerHealth(r.Context(), triggerID)
				if healthErr != nil && !errors.Is(healthErr, state.ErrNotFound) {
					api.WriteProblem(w, api.ErrInternal("could not load queue consumer health"))
					return
				}
				resp.LastPollAt = health.LastPollAt
				resp.LastSuccessAt = health.LastSuccessAt
				resp.LastErrorAt = health.LastErrorAt
				resp.LastError = health.LastError
				resp.LagMessages = health.LagMessages
				resp.LagAgeSeconds = health.LagAgeSeconds
				if consumerActive {
					resp.ConsumerLiveness = queueBindingConsumerLiveness(time.Now().UTC(), health)
				}
			}
		}
	}
	if binding.DeploymentScope != "" {
		available, problem := s.queueBindingEnvironmentAvailable(r.Context(), acct, app, binding)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if !available {
			resp.ConsumerState, resp.ConsumerStateReason, resp.ConsumerLiveness = "paused", "environment_unavailable", "not_observed"
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

const queueBindingConsumerStaleAfter = 30 * time.Second

func queueBindingConsumerLiveness(now time.Time, health state.TriggerConsumerHealth) string {
	if health.LastPollAt == nil {
		return "not_observed"
	}
	if now.Sub(*health.LastPollAt) > queueBindingConsumerStaleAfter {
		return "stale"
	}
	if health.LastErrorAt != nil && (health.LastSuccessAt == nil || health.LastErrorAt.After(*health.LastSuccessAt)) {
		return "degraded"
	}
	return "healthy"
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
	if prob := validateQueueBindingTarget(mode, class, app); prob != nil {
		api.WriteProblem(w, prob)
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
	if mode == "push" {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok || !limits.TriggersAllowed {
			api.WriteProblem(w, api.ErrPlanTriggersNotAllowed(acct.Plan))
			return
		}
	}
	consumers, ok := s.queueBindingConsumers(w)
	if !ok {
		return
	}
	var environmentID string
	if req.Environment != "" {
		_, environment, problem := s.resolveQueueEnvironment(r.Context(), acct, app, req.Environment)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		environmentID = environment.ID
	}
	result, err := consumers.CreateQueueBindingWithConsumer(r.Context(), state.QueueBinding{
		DeploymentScope: req.Environment, EnvironmentID: environmentID,
		AccountID: acct.ID, AppID: app.ID, Name: req.Name, QueueName: req.QueueName,
		Mode: mode, WorkloadClass: state.WorkloadClass(class), Enabled: enabled,
		MaxConcurrency: concurrency, RetryPolicyJSON: policy,
	})
	if err != nil {
		writeQueueBindingMutationError(w, acct, err, "could not create queue binding and consumer")
		return
	}
	row := result.Binding
	s.notifyQueueBindingConsumer(r.Context(), result)
	s.audit.Emit(r.Context(), "queue.binding_created", &acct.ID, map[string]any{"binding_id": row.ID, "app_id": app.ID, "name": row.Name, "queue_name": row.QueueName, "mode": row.Mode, "environment": row.DeploymentScope, "environment_id": row.EnvironmentID})
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
	existing, err := s.store.QueueBindingByID(r.Context(), acct.ID, app.ID, id)
	if err != nil {
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
	finalMode := existing.Mode
	if req.Mode != nil {
		finalMode = *req.Mode
	}
	if finalMode == "push" {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok || !limits.TriggersAllowed {
			api.WriteProblem(w, api.ErrPlanTriggersNotAllowed(acct.Plan))
			return
		}
	}
	finalClass := string(existing.WorkloadClass)
	if req.WorkloadClass != nil {
		finalClass = *req.WorkloadClass
		class := state.WorkloadClass(finalClass)
		params.WorkloadClass = &class
	}
	if prob := validateQueueBindingTarget(finalMode, finalClass, app); prob != nil {
		api.WriteProblem(w, prob)
		return
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
	consumers, ok := s.queueBindingConsumers(w)
	if !ok {
		return
	}
	result, err := consumers.UpdateQueueBindingWithConsumer(r.Context(), acct.ID, app.ID, id, params)
	if err != nil {
		writeQueueBindingMutationError(w, acct, err, "could not update queue binding and consumer")
		return
	}
	row := result.Binding
	s.notifyQueueBindingConsumer(r.Context(), result)
	s.audit.Emit(r.Context(), "queue.binding_updated", &acct.ID, map[string]any{"binding_id": row.ID, "app_id": app.ID, "name": row.Name})
	writeJSON(w, http.StatusOK, queueBindingResponse(row))
}

func (s *server) deleteQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	consumers, ok := s.queueBindingConsumers(w)
	if !ok {
		return
	}
	result, err := consumers.DeleteQueueBindingWithConsumer(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		writeQueueBindingMutationError(w, acct, err, "could not delete queue binding and consumer")
		return
	}
	s.notifyQueueBindingConsumer(r.Context(), result)
	s.audit.Emit(r.Context(), "queue.binding_deleted", &acct.ID, map[string]any{"binding_id": r.PathValue("id"), "app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) queueBindingConsumers(w http.ResponseWriter) (state.QueueBindingConsumerStore, bool) {
	store, ok := s.store.(state.QueueBindingConsumerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("atomic queue consumer publication is unavailable"))
	}
	return store, ok
}

func (s *server) notifyQueueBindingConsumer(ctx context.Context, result state.QueueBindingConsumerResult) {
	if result.NotificationsCommitted {
		return
	}
	for _, change := range result.Changes {
		_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON(change.Kind, change.AppID, change.TriggerID))
	}
}

func writeQueueBindingMutationError(w http.ResponseWriter, acct state.Account, err error, detail string) {
	var quota *state.TriggerQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanTriggerQuota(acct.Plan, string(quota.Scope), quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue binding conflicts", "binding or consumer identity is already in use"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Queue binding not found", "no queue binding exists with that id"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, queueBindingProblem("binding is not supported by the current app or plan"))
	default:
		api.WriteProblem(w, api.ErrCapacity(detail))
	}
}
