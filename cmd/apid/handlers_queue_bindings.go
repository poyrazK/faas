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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
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

// queueBindingTriggerConfig is the private trigger projection used by the
// existing schedd push-consumer loop. Keeping the binding id in the config
// lets updates/deletes find only the trigger owned by this binding while the
// public queue-binding API remains the source of truth.
func queueBindingTriggerConfig(binding state.QueueBinding) []byte {
	config := map[string]any{"mode": "queue", "queue_binding_id": binding.ID}
	if len(binding.RetryPolicyJSON) > 0 {
		var policy api.RetryPolicyDTO
		if json.Unmarshal(binding.RetryPolicyJSON, &policy) == nil && (policy.MaxAttempts > 0 || policy.BaseSeconds > 0 || policy.MaxSeconds > 0 || policy.JitterSeconds > 0) {
			config["retry_policy"] = policy
		}
	}
	b, _ := json.Marshal(config)
	return b
}

func queueBindingTriggerID(ctx context.Context, store state.Store, appID, bindingID string) (string, error) {
	triggers, err := store.ListTriggersForApp(ctx, appID)
	if err != nil {
		return "", err
	}
	for _, trigger := range triggers {
		if trigger.Kind != string(api.TriggerKindQueue) || !trigger.Source.Valid || trigger.Source.String != string(state.InvocationQueue) {
			continue
		}
		var marker struct {
			BindingID string `json:"queue_binding_id"`
		}
		if json.Unmarshal(trigger.Config, &marker) == nil && marker.BindingID == bindingID {
			return trigger.ID.String(), nil
		}
	}
	return "", nil
}

func queueBindingTriggerAttempts(binding state.QueueBinding, limits api.Limits) int32 {
	_, _, attempts, _ := triggerDeliveryDefaults(limits)
	var policy api.RetryPolicyDTO
	if json.Unmarshal(binding.RetryPolicyJSON, &policy) == nil && policy.MaxAttempts > 0 {
		attempts = int32(policy.MaxAttempts)
	}
	if limits.TriggerMaxAttemptsMax > 0 && attempts > int32(limits.TriggerMaxAttemptsMax) {
		attempts = int32(limits.TriggerMaxAttemptsMax)
	}
	return attempts
}

func queueBindingTriggerBatch(binding state.QueueBinding, limits api.Limits) int32 {
	batch := binding.MaxConcurrency
	if batch < 1 {
		batch = 1
	}
	if batch > 5000 {
		batch = 5000
	}
	if limits.TriggerBatchSizeMax > 0 && batch > limits.TriggerBatchSizeMax {
		batch = limits.TriggerBatchSizeMax
	}
	return int32(batch)
}

// syncQueueBindingConsumer projects a push binding onto the scheduler's
// existing queue trigger/poller path. The projection is deliberately private:
// callers manage it through queue-bindings, while schedd continues to consume
// the durable trigger rows it already understands.
func (s *server) syncQueueBindingConsumer(ctx context.Context, app state.App, acct state.Account, binding state.QueueBinding) error {
	triggerID, err := queueBindingTriggerID(ctx, s.store, app.ID, binding.ID)
	if err != nil {
		return fmt.Errorf("find queue consumer: %w", err)
	}
	if binding.Mode != "push" {
		if triggerID != "" {
			if err := s.store.DeleteTrigger(ctx, triggerID, app.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				return fmt.Errorf("remove queue consumer: %w", err)
			}
			_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", app.ID, triggerID))
		}
		return nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.TriggersAllowed {
		return api.ErrPlanTriggersNotAllowed(acct.Plan)
	}
	config := queueBindingTriggerConfig(binding)
	enabled := binding.Enabled
	batchSize := queueBindingTriggerBatch(binding, limits)
	batchWindow := int32(1000)
	maxAttempts := queueBindingTriggerAttempts(binding, limits)
	if triggerID != "" {
		trigger, lookupErr := s.store.TriggerByID(ctx, triggerID)
		if lookupErr != nil {
			return fmt.Errorf("read queue consumer: %w", lookupErr)
		}
		if trigger.Slug != binding.QueueName {
			if err := s.store.DeleteTrigger(ctx, triggerID, app.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				return fmt.Errorf("replace queue consumer: %w", err)
			}
			_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", app.ID, triggerID))
			triggerID = ""
		}
	}
	if triggerID != "" {
		updated, err := s.store.UpdateTrigger(ctx, triggerID, &enabled, config, &batchSize, &batchWindow, &maxAttempts, nil, nil, nil, nil)
		if err == nil {
			_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("updated", app.ID, uuidFromPgtype(updated.ID).String()))
		}
		return err
	}
	created, err := s.store.CreateTriggerIfUnderQuota(ctx, app.ID, string(api.TriggerKindQueue), binding.QueueName,
		enabled, config, string(state.InvocationQueue), batchSize, 1000, maxAttempts,
		int32(limits.TriggerPayloadMaxBytes), api.BrokerPoisonStrategyCommit, limits)
	if err == nil {
		_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("created", app.ID, uuidFromPgtype(created.ID).String()))
	}
	return err
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
// binding-scoped queue counters. It deliberately reports configuration state,
// not process liveness: broker/worker liveness remains observable through the
// schedd metrics and alerts documented in FaasDurableQueue.
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
	stats, err := s.store.QueueStateForQueue(r.Context(), app.ID, binding.QueueName)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load queue binding queue state"))
		return
	}

	resp := api.QueueBindingStatusResponse{
		BindingID:     binding.ID,
		Name:          binding.Name,
		QueueName:     binding.QueueName,
		Mode:          binding.Mode,
		WorkloadClass: string(binding.WorkloadClass),
		Enabled:       binding.Enabled,
		ConsumerState: "external",
		GeneratedAt:   time.Now().UTC(),
		Depth:         stats.Depth,
		InFlight:      stats.InFlight,
		DeadLetter:    stats.DeadLetter,
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
			if binding.Enabled && trigger.Enabled {
				resp.ConsumerState = "active"
				resp.ConsumerStateReason = "push_consumer_enabled"
			} else {
				resp.ConsumerState = "paused"
				resp.ConsumerStateReason = "push_consumer_disabled"
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
	if mode == "push" {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok || !limits.TriggersAllowed {
			api.WriteProblem(w, api.ErrPlanTriggersNotAllowed(acct.Plan))
			return
		}
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
	if err := s.syncQueueBindingConsumer(r.Context(), app, acct, row); err != nil {
		_ = s.store.DeleteQueueBinding(r.Context(), acct.ID, app.ID, row.ID)
		api.WriteProblem(w, api.ErrCapacity("could not provision queue consumer"))
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
	if err := s.syncQueueBindingConsumer(r.Context(), app, acct, row); err != nil {
		// The binding row is durable even if a scheduler projection is
		// temporarily unavailable; the next update/reconcile repairs it.
		s.log.Warn("queue binding consumer projection failed", "binding_id", row.ID, "err", err)
	}
	s.audit.Emit(r.Context(), "queue.binding_updated", &acct.ID, map[string]any{"binding_id": row.ID, "app_id": app.ID, "name": row.Name})
	writeJSON(w, http.StatusOK, queueBindingResponse(row))
}

func (s *server) deleteQueueBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	row, err := s.store.QueueBindingByID(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		s.notFound(w, "queue binding not found")
		return
	}
	triggerID, findErr := queueBindingTriggerID(r.Context(), s.store, app.ID, row.ID)
	if findErr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not find queue consumer"))
		return
	}
	if triggerID != "" {
		if err := s.store.DeleteTrigger(r.Context(), triggerID, app.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not remove queue consumer"))
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", app.ID, triggerID))
	}
	if err := s.store.DeleteQueueBinding(r.Context(), acct.ID, app.ID, id); err != nil {
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
