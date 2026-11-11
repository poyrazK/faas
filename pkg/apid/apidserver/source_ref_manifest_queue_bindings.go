package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceRefQueueBindingChange records one queue binding a repository deploy
// created or changed, so a deploy that is not accepted puts it back.
type sourceRefQueueBindingChange struct {
	id       string
	created  bool
	previous state.QueueBinding
}

// applySourceRefQueueBindings makes the app's production queue bindings match
// the manifest's queue_bindings block by name, as `gregale deploy` does: an
// omitted mode or workload class is pull/worker. A binding the block no longer
// names is disabled, not deleted, so a deploy that is not accepted can restore
// it; delete it with `gregale queue bindings delete`. An omitted block leaves
// the bindings alone (ADR-20261018101500 amendment 2026-10-31).
func (s *server) applySourceRefQueueBindings(ctx context.Context, acct state.Account, app state.App, bindings []gregalemanifest.QueueBinding, deploymentScope string, staged *sourceRefManifestStaged) *api.Problem {
	if bindings == nil || !state.ProductionDeploymentScope(deploymentScope) {
		return nil
	}
	if !s.capabilityEnabledFor("queue-bindings", acct, app) {
		return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest",
			"queue_bindings are not available to this account")
	}
	consumers, ok := s.store.(state.QueueBindingConsumerStore)
	if !ok {
		return api.ErrCapacity("atomic queue consumer publication is unavailable")
	}
	// The lifecycle step may have just made the app a worker; bindings are
	// checked against the stored mode.
	fresh, err := s.store.AppByID(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app")
	}
	existing, err := s.store.ListQueueBindingsForApp(ctx, acct.ID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not list queue bindings")
	}
	current := make(map[string]state.QueueBinding, len(existing))
	for _, row := range existing {
		if row.DeploymentScope == "" {
			current[row.Name] = row
		}
	}
	for _, row := range current {
		if row.Enabled && !slices.ContainsFunc(bindings, func(b gregalemanifest.QueueBinding) bool { return b.Name == row.Name }) {
			disabled := false
			if problem := s.updateSourceRefQueueBinding(ctx, consumers, acct, row, state.UpdateQueueBindingParams{Enabled: &disabled}, staged); problem != nil {
				return problem
			}
		}
	}
	for _, binding := range bindings {
		want, problem := sourceRefQueueBindingSpec(acct, fresh, binding)
		if problem != nil {
			return problem
		}
		row, exists := current[binding.Name]
		if !exists {
			result, err := consumers.CreateQueueBindingWithConsumer(ctx, want)
			if err != nil {
				return sourceRefQueueBindingProblem(binding.Name, err)
			}
			staged.queueBindingChanges = append(staged.queueBindingChanges, sourceRefQueueBindingChange{id: result.Binding.ID, created: true})
			s.notifyQueueBindingConsumer(ctx, result)
			continue
		}
		if row.QueueName == want.QueueName && row.Mode == want.Mode && row.WorkloadClass == want.WorkloadClass &&
			row.Enabled == want.Enabled && row.MaxConcurrency == want.MaxConcurrency &&
			(binding.RetryPolicy == nil || string(row.RetryPolicyJSON) == string(want.RetryPolicyJSON)) {
			continue
		}
		params := state.UpdateQueueBindingParams{QueueName: &want.QueueName, Mode: &want.Mode, WorkloadClass: &want.WorkloadClass,
			Enabled: &want.Enabled, MaxConcurrency: &want.MaxConcurrency}
		if binding.RetryPolicy != nil {
			policy := []byte(want.RetryPolicyJSON)
			params.RetryPolicyJSON = &policy
		}
		if problem := s.updateSourceRefQueueBinding(ctx, consumers, acct, row, params, staged); problem != nil {
			return problem
		}
	}
	return nil
}

func (s *server) updateSourceRefQueueBinding(ctx context.Context, consumers state.QueueBindingConsumerStore, acct state.Account,
	row state.QueueBinding, params state.UpdateQueueBindingParams, staged *sourceRefManifestStaged) *api.Problem {
	result, err := consumers.UpdateQueueBindingWithConsumer(ctx, acct.ID, row.AppID, row.ID, params)
	if err != nil {
		return sourceRefQueueBindingProblem(row.Name, err)
	}
	staged.queueBindingChanges = append(staged.queueBindingChanges, sourceRefQueueBindingChange{id: row.ID, previous: row})
	s.notifyQueueBindingConsumer(ctx, result)
	return nil
}

// sourceRefQueueBindingSpec applies the queue binding API's defaults and
// checks to one manifest entry.
func sourceRefQueueBindingSpec(acct state.Account, app state.App, binding gregalemanifest.QueueBinding) (state.QueueBinding, *api.Problem) {
	field := fmt.Sprintf("queue_bindings %q: ", binding.Name)
	mode := binding.Mode
	if mode == "" {
		mode = "pull"
	}
	class := binding.WorkloadClass
	if class == "" {
		class = string(state.WorkloadClassWorker)
	}
	concurrency := binding.MaxConcurrency
	if concurrency == 0 {
		concurrency = 1
	}
	enabled := binding.Enabled == nil || *binding.Enabled
	var retry *api.RetryPolicyDTO
	if binding.RetryPolicy != nil {
		retry = &api.RetryPolicyDTO{MaxAttempts: binding.RetryPolicy.MaxAttempts, BaseSeconds: binding.RetryPolicy.BaseSeconds,
			MaxSeconds: binding.RetryPolicy.MaxSeconds, JitterSeconds: binding.RetryPolicy.JitterSeconds}
	}
	policy, problem := marshalQueueBindingRetryPolicy(retry)
	for _, check := range []*api.Problem{
		validateQueueBindingName("name", binding.Name), validateQueueBindingName("queue_name", binding.QueueName),
		validateQueueBindingMode(mode), validateQueueBindingTarget(mode, class, app), validateQueueBindingConcurrency(concurrency), problem,
	} {
		if check != nil {
			return state.QueueBinding{}, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", field+check.Detail)
		}
	}
	if mode == "push" {
		if limits, ok := api.LimitsFor(acct.Plan); !ok || !limits.TriggersAllowed {
			return state.QueueBinding{}, api.ErrPlanTriggersNotAllowed(acct.Plan)
		}
	}
	return state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: binding.Name, QueueName: binding.QueueName,
		Mode: mode, WorkloadClass: state.WorkloadClass(class), Enabled: enabled, MaxConcurrency: concurrency, RetryPolicyJSON: policy}, nil
}

func sourceRefQueueBindingProblem(name string, err error) *api.Problem {
	detail := fmt.Sprintf("queue binding %q could not be applied", name)
	switch {
	case errors.Is(err, state.ErrInvalidArgument), errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", detail+": "+err.Error())
	default:
		return api.ErrCapacity(detail)
	}
}

// rollbackSourceRefQueueBindings undoes the deploy's binding changes, newest
// first: a created binding is deleted, a changed one gets its old settings.
func (s *server) rollbackSourceRefQueueBindings(ctx context.Context, staged sourceRefManifestStaged) error {
	if len(staged.queueBindingChanges) == 0 {
		return nil
	}
	consumers, ok := s.store.(state.QueueBindingConsumerStore)
	if !ok {
		return errors.New("queue bindings unavailable during rollback")
	}
	var errs []error
	for i := len(staged.queueBindingChanges) - 1; i >= 0; i-- {
		change := staged.queueBindingChanges[i]
		var result state.QueueBindingConsumerResult
		var err error
		if change.created {
			result, err = consumers.DeleteQueueBindingWithConsumer(ctx, staged.accountID, staged.appID, change.id)
		} else {
			old := change.previous
			policy := []byte(old.RetryPolicyJSON)
			result, err = consumers.UpdateQueueBindingWithConsumer(ctx, staged.accountID, staged.appID, change.id, state.UpdateQueueBindingParams{
				QueueName: &old.QueueName, Mode: &old.Mode, WorkloadClass: &old.WorkloadClass, Enabled: &old.Enabled,
				MaxConcurrency: &old.MaxConcurrency, RetryPolicyJSON: &policy})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("rollback queue binding %s: %w", change.id, err))
			continue
		}
		s.notifyQueueBindingConsumer(ctx, result)
	}
	return errors.Join(errs...)
}
