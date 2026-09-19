package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	queueWorkloadProfileName           = "default"
	queueWorkloadProfileQueueName      = "default"
	queueWorkloadProfileTargetDepth    = 10.0
	queueWorkloadProfileMaxConcurrency = 1
)

// queueWorkloadProfileScalingPolicy expands the simple profile while
// preserving unrelated advanced scaling settings. The profile owns the
// queue-depth target, scale-to-zero floor, and safe cooldown defaults.
func queueWorkloadProfileScalingPolicy(app state.App, targetDepth float64) *api.ScalingPolicy {
	policy := &api.ScalingPolicy{
		MinInstances:      0,
		ScaleOutCooldownS: 5,
		ScaleInCooldownS:  60,
		Target:            &api.ScalingTarget{Metric: "queue_depth", Value: targetDepth},
	}
	if app.ScalingPolicy == nil {
		return policy
	}
	current := app.ScalingPolicy
	policy.MaxInstances = current.MaxInstances
	policy.ConcurrencyOverflow = current.ConcurrencyOverflow
	policy.MaxQueueWaitMS = current.MaxQueueWaitMS
	policy.WakeMaxQueueDepth = current.WakeMaxQueueDepth
	policy.WakeMaxQueueWaitSeconds = current.WakeMaxQueueWaitSeconds
	if current.ScaleOutCooldownS > 0 {
		policy.ScaleOutCooldownS = current.ScaleOutCooldownS
	}
	if current.ScaleInCooldownS > 0 {
		policy.ScaleInCooldownS = current.ScaleInCooldownS
	}
	return policy
}

func queueWorkloadProfilePolicyEqual(app state.App, desired *api.ScalingPolicy) bool {
	if desired == nil || app.ScalingPolicy == nil || app.MinInstances != desired.MinInstances {
		return false
	}
	current := app.ScalingPolicy
	if current.MaxInstances != desired.MaxInstances ||
		current.ScaleOutCooldownS != desired.ScaleOutCooldownS ||
		current.ScaleInCooldownS != desired.ScaleInCooldownS ||
		current.ConcurrencyOverflow != desired.ConcurrencyOverflow ||
		current.MaxQueueWaitMS != desired.MaxQueueWaitMS ||
		current.WakeMaxQueueDepth != desired.WakeMaxQueueDepth ||
		current.WakeMaxQueueWaitSeconds != desired.WakeMaxQueueWaitSeconds {
		return false
	}
	if current.Target == nil || desired.Target == nil {
		return current.Target == nil && desired.Target == nil
	}
	return current.Target.Metric == desired.Target.Metric && current.Target.Value == desired.Target.Value
}

// configureQueueWorkload is the server-side counterpart to `gregale queue
// setup`. It reconciles the binding, its push-consumer projection, and the
// app scaling policy under one idempotent PUT. If a later step fails, the
// binding change is rolled back best-effort so callers do not observe a
// half-created profile.
func (s *server) configureQueueWorkload(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.QueueWorkloadProfileRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.TriggersAllowed {
		api.WriteProblem(w, api.ErrPlanTriggersNotAllowed(acct.Plan))
		return
	}

	queueName := req.QueueName
	if queueName == "" {
		queueName = queueWorkloadProfileQueueName
	}
	if prob := validateQueueBindingName("queue_name", queueName); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	class := req.WorkloadClass
	if class == "" {
		class = string(app.WorkloadClass)
	}
	if prob := validateQueueBindingClass(class); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if app.WorkloadClass != "" && string(app.WorkloadClass) != class {
		api.WriteProblem(w, queueBindingProblem(fmt.Sprintf("workload_class %q does not match app workload class %q", class, app.WorkloadClass)))
		return
	}
	if app.WorkloadClass != state.WorkloadClassWorker && app.WorkloadClass != state.WorkloadClassJob {
		api.WriteProblem(w, api.ErrScalingTargetIncompatibleWithWorkloadClass("queue_depth"))
		return
	}
	maxConcurrency := req.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = queueWorkloadProfileMaxConcurrency
	}
	if prob := validateQueueBindingConcurrency(maxConcurrency); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	targetDepth := req.TargetDepth
	if targetDepth == 0 {
		targetDepth = queueWorkloadProfileTargetDepth
	}
	if targetDepth <= 0 {
		api.WriteProblem(w, queueBindingProblem("target_depth must be greater than zero"))
		return
	}

	desiredPolicy := queueWorkloadProfileScalingPolicy(app, targetDepth)
	policyReq := &api.UpdateAppRequest{ScalingPolicy: desiredPolicy}
	if prob := validateUpdateApp(policyReq, acct, limits, app); prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	bindings, err := s.store.ListQueueBindingsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list queue bindings"))
		return
	}
	var existing *state.QueueBinding
	for i := range bindings {
		if bindings[i].Name != queueWorkloadProfileName {
			continue
		}
		if existing != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue workload profile conflict", "multiple default queue bindings exist; reconcile them with the queue bindings API"))
			return
		}
		existing = &bindings[i]
	}
	if existing != nil && existing.QueueName != queueName && !req.Force {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue workload profile conflict", fmt.Sprintf("default binding already points at queue %q; set force=true to replace it", existing.QueueName)))
		return
	}

	var retryPolicyJSON []byte
	if req.RetryPolicy != nil {
		var retryProblem *api.Problem
		retryPolicyJSON, retryProblem = marshalQueueBindingRetryPolicy(req.RetryPolicy)
		if retryProblem != nil {
			api.WriteProblem(w, retryProblem)
			return
		}
	}
	enabled := true
	mode := "push"
	workloadClass := state.WorkloadClass(class)
	var binding state.QueueBinding
	created := existing == nil
	if created {
		if retryPolicyJSON == nil {
			retryPolicyJSON = []byte(`{}`)
		}
		binding, err = s.store.CreateQueueBinding(r.Context(), state.QueueBinding{
			AccountID: acct.ID, AppID: app.ID, Name: queueWorkloadProfileName,
			QueueName: queueName, Mode: mode, WorkloadClass: workloadClass,
			Enabled: enabled, MaxConcurrency: maxConcurrency, RetryPolicyJSON: retryPolicyJSON,
		})
	} else {
		params := state.UpdateQueueBindingParams{
			QueueName: &queueName, Mode: &mode, WorkloadClass: &workloadClass,
			Enabled: &enabled, MaxConcurrency: &maxConcurrency,
		}
		if req.RetryPolicy != nil {
			params.RetryPolicyJSON = &retryPolicyJSON
		}
		binding, err = s.store.UpdateQueueBinding(r.Context(), acct.ID, app.ID, existing.ID, params)
	}
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Queue workload profile conflict", "name and queue_name must be unique within an app"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not configure queue workload binding"))
		return
	}
	if err := s.syncQueueBindingConsumer(r.Context(), app, acct, binding); err != nil {
		s.rollbackQueueWorkloadBinding(r.Context(), acct, app, binding, existing, created)
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not provision queue consumer"))
		}
		return
	}

	updatedApp := app
	if !queueWorkloadProfilePolicyEqual(app, desiredPolicy) {
		minInstances := desiredPolicy.MinInstances
		updatedApp, err = s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{
			MinInstances: &minInstances, SetMinInstances: true,
			ScalingPolicy: policyPtrFromReq(policyReq), SetScalingPolicy: true,
		})
		if err != nil {
			s.rollbackQueueWorkloadBinding(r.Context(), acct, app, binding, existing, created)
			api.WriteProblem(w, api.ErrCapacity("could not apply queue-depth scaling policy"))
			return
		}
		_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":"queue_workload_configured","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	}

	s.audit.Emit(r.Context(), "queue.workload_configured", &acct.ID, map[string]any{
		"app_id": app.ID, "binding_id": binding.ID, "queue_name": binding.QueueName,
		"created": created, "workload_class": binding.WorkloadClass,
	})
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], api.QueueWorkloadProfileResponse{
		App: s.appResponseWithContext(r.Context(), updatedApp, acct.Plan), Binding: queueBindingResponse(binding),
		ScalingPolicy: desiredPolicy, Created: created,
	})
}

func (s *server) rollbackQueueWorkloadBinding(ctx context.Context, acct state.Account, app state.App, current state.QueueBinding, previous *state.QueueBinding, created bool) {
	if created {
		if triggerID, err := queueBindingTriggerID(ctx, s.store, app.ID, current.ID); err == nil && triggerID != "" {
			_ = s.store.DeleteTrigger(ctx, triggerID, app.ID)
		}
		_ = s.store.DeleteQueueBinding(ctx, acct.ID, app.ID, current.ID)
		return
	}
	if previous == nil {
		return
	}
	queueName, mode, workloadClass := previous.QueueName, previous.Mode, previous.WorkloadClass
	enabled, maxConcurrency := previous.Enabled, previous.MaxConcurrency
	policy := append([]byte(nil), previous.RetryPolicyJSON...)
	restored, err := s.store.UpdateQueueBinding(ctx, acct.ID, app.ID, previous.ID, state.UpdateQueueBindingParams{
		QueueName: &queueName, Mode: &mode, WorkloadClass: &workloadClass,
		Enabled: &enabled, MaxConcurrency: &maxConcurrency, RetryPolicyJSON: &policy,
	})
	if err == nil {
		_ = s.syncQueueBindingConsumer(ctx, app, acct, restored)
	}
}
