package state

import (
	"context"
	"encoding/json"
	"errors"
)

// First-class bindings retain their catalog UUID and scope. Private clone
// queue receipts use their own admission path and cannot become bound work.
func invocationBoundQueueShape(inv Invocation) bool {
	return inv.Source == InvocationQueue && inv.QueueBindingID != "" && inv.EnvironmentID == "" && inv.WorkPolicyName == "" && len(inv.WorkKeyDigest) == 0 && inv.CronID == nil && inv.OnSuccessDestinationID == "" && inv.OnFailureDestinationID == ""
}

func validateBoundQueueEnvironment(ctx context.Context, store invocationAppReader, inv Invocation, scope string) (bool, error) {
	if !invocationBoundQueueShape(inv) {
		return false, nil
	}
	history, ok := store.(interface {
		QueueBindingHistoryByID(context.Context, string, string, string) (QueueBinding, error)
	})
	if !ok {
		return false, ErrInvocationEnvironmentWorkIsolation
	}
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return false, err
	}
	binding, err := history.QueueBindingHistoryByID(ctx, app.AccountID, app.ID, inv.QueueBindingID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, ErrInvalidArgument
		}
		return false, err
	}
	if binding.AppID != app.ID || binding.AccountID != app.AccountID || inv.AccountID != "" && inv.AccountID != app.AccountID {
		return false, ErrInvalidArgument
	}
	if binding.RetiredAt != nil {
		return false, ErrQueueBindingRetired
	}
	if binding.DeploymentScope == "" {
		return !invocationStageScope(scope), nil
	}
	if !invocationScopesMatch(app.ProjectID != "", binding.DeploymentScope, scope) {
		return false, ErrInvalidArgument
	}
	environments, ok := store.(invocationEnvironmentStore)
	if !ok {
		return false, ErrInvocationEnvironmentWorkIsolation
	}
	environment, err := environments.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
	if err != nil || environment.ID != binding.EnvironmentID {
		return false, ErrQueueBindingEnvironmentUnavailable
	}
	return true, nil
}

func (m *MemStore) validateBoundQueueClaimLocked(inv Invocation) (bool, error) {
	if !invocationBoundQueueShape(inv) {
		return false, nil
	}
	for _, binding := range m.queueBindings {
		if canonicalMemUUID(binding.ID) == canonicalMemUUID(inv.QueueBindingID) &&
			binding.AppID == inv.AppID && binding.AccountID == inv.AccountID &&
			!m.queueBindingEnvironmentAvailableLocked(binding) {
			return true, ErrQueueBindingEnvironmentUnavailable
		}
	}
	copied := inv
	if err := m.captureInvocationQueueBindingLocked(&copied); err != nil {
		return true, err
	}
	var headers map[string]string
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return true, ErrInvalidArgument
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return true, err
	}
	if revision != "" {
		dep, ok := m.deployments[revision]
		if !ok || dep.AppID != inv.AppID || !invocationScopesMatch(m.apps[inv.AppID].ProjectID != "", inv.DeploymentScope, dep.Scope) {
			return true, ErrInvocationEnvironmentWorkIsolation
		}
	}
	if release != "" {
		set, ok := m.projectReleaseSets[release]
		app := m.apps[inv.AppID]
		if !ok || set.ProjectID != app.ProjectID || set.AccountID != inv.AccountID || !invocationScopesMatch(true, inv.DeploymentScope, set.EnvironmentSlug) {
			return true, ErrInvocationEnvironmentWorkIsolation
		}
	}
	return true, nil
}
