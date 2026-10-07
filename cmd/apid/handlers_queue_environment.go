package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A selection pins the binding observed before admission. The store checks its
// immutable catalog identity again under the admission lock.
type queueSendSelection struct {
	Name    string
	Scope   string
	Binding *state.QueueBinding
	CanPush bool
}

func (s *server) resolveQueueEnvironment(ctx context.Context, acct state.Account, app state.App, requested string) (string, *state.ProjectEnvironment, *api.Problem) {
	scope := requested
	if scope == "" {
		scope = state.DefaultInvocationDeploymentScope(app)
	} else if !api.ValidProjectEnvironmentSlug(scope) || app.ProjectID == "" || app.PreviewOfSlug != "" {
		return "", nil, queueBindingProblem("environment must name a registered environment of this app's project")
	}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return scope, nil, nil
	}
	environment, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, scope)
	if errors.Is(err, state.ErrNotFound) {
		if requested == "" {
			// Legacy projects may not yet have a catalog environment. They
			// can still use shared bindings, never a named binding.
			return scope, nil, nil
		}
		return "", nil, queueBindingProblem("environment is not registered in this app's project")
	}
	if err != nil {
		return "", nil, api.ErrCapacity("look up queue environment")
	}
	return scope, &environment, nil
}

func (s *server) queueBindingEnvironmentAvailable(ctx context.Context, acct state.Account, app state.App, binding state.QueueBinding) (bool, *api.Problem) {
	if binding.DeploymentScope == "" {
		return true, nil
	}
	if app.ProjectID == "" {
		return false, nil
	}
	environment, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, binding.DeploymentScope)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, api.ErrCapacity("look up queue environment")
	}
	return environment.ID == binding.EnvironmentID, nil
}

func (s *server) resolveQueueSendSelection(ctx context.Context, acct state.Account, app state.App, requested, requestedEnvironment string) (queueSendSelection, *api.Problem) {
	if requested != "" {
		if problem := validateQueueBindingName("queue_name", requested); problem != nil {
			return queueSendSelection{}, problem
		}
	}
	scope, environment, problem := s.resolveQueueEnvironment(ctx, acct, app, requestedEnvironment)
	if problem != nil {
		return queueSendSelection{}, problem
	}
	selection := queueSendSelection{Scope: scope}
	history, ok := s.store.(state.QueueBindingHistoryStore)
	if !ok {
		return selection, api.ErrCapacity("queue binding history unavailable")
	}
	bindings, err := history.ListQueueBindingHistoryForApp(ctx, acct.ID, app.ID)
	if err != nil {
		return selection, api.ErrCapacity("look up queue bindings")
	}

	effective, hasNamedBindings := applicableQueueBindings(bindings, acct, app, scope, environment, requestedEnvironment == "")
	selection, selected, problem := selectApplicableQueueBinding(selection, effective, requested)
	if selected || problem != nil {
		return selection, problem
	}
	if requestedEnvironment != "" {
		return selection, queueBindingProblem("the selected environment requires an enabled environment-owned queue binding")
	}
	return s.resolveLegacyQueueSendSelection(ctx, app, selection, requested, effective, hasNamedBindings)
}

// Retired/disabled exact bindings also shadow the shared namespace. Omitting
// them would let a producer escape a deliberate hold by choosing a legacy queue.
func applicableQueueBindings(bindings []state.QueueBinding, acct state.Account, app state.App, scope string, environment *state.ProjectEnvironment, allowShared bool) (map[string]state.QueueBinding, bool) {
	effective := map[string]state.QueueBinding{}
	hasNamedBindings := false
	for _, binding := range bindings {
		if binding.AppID != app.ID || binding.AccountID != acct.ID {
			continue
		}
		if binding.DeploymentScope != "" {
			hasNamedBindings = true
			if environment == nil || binding.EnvironmentID != environment.ID || binding.DeploymentScope != scope {
				continue
			}
		} else if !allowShared {
			continue
		}
		previous, found := effective[binding.QueueName]
		if !found || previous.DeploymentScope == "" && binding.DeploymentScope != "" {
			effective[binding.QueueName] = binding
		}
	}
	return effective, hasNamedBindings
}

func selectApplicableQueueBinding(selection queueSendSelection, bindings map[string]state.QueueBinding, requested string) (queueSendSelection, bool, *api.Problem) {
	if requested != "" {
		if binding, found := bindings[requested]; found {
			selected, problem := selectQueueBinding(selection, binding)
			return selected, true, problem
		}
		return selection, false, nil
	}
	active := 0
	for _, binding := range bindings {
		if binding.Enabled && binding.RetiredAt == nil {
			selection.Name, selection.Binding, selection.CanPush = binding.QueueName, &binding, binding.Mode == "push"
			active++
		}
	}
	if active > 1 {
		return selection, true, queueBindingProblem("queue_name is required when the selected environment has multiple enabled queue bindings")
	}
	return selection, active == 1, nil
}

// Only genuinely unbound legacy consumers participate in the compatibility
// fallback. A private consumer in another namespace cannot select this work.
func (s *server) unboundQueueConsumerNames(ctx context.Context, appID string) ([]string, *api.Problem) {
	triggers, err := s.store.ListTriggersForApp(ctx, appID)
	if err != nil {
		return nil, api.ErrCapacity("look up queue consumers")
	}
	var names []string
	for _, trigger := range triggers {
		if trigger.Kind == string(api.TriggerKindQueue) && trigger.Enabled && trigger.Source.Valid &&
			trigger.Source.String == string(state.InvocationQueue) && trigger.QueueBindingScope == "" && !trigger.QueueBindingID.Valid {
			names = append(names, trigger.Slug)
		}
	}
	return names, nil
}

func (s *server) resolveLegacyQueueSendSelection(ctx context.Context, app state.App, selection queueSendSelection, requested string, effective map[string]state.QueueBinding, hasNamedBindings bool) (queueSendSelection, *api.Problem) {
	names, problem := s.unboundQueueConsumerNames(ctx, app.ID)
	if problem != nil {
		return selection, problem
	}
	if requested != "" {
		for _, name := range names {
			if name == requested {
				selection.Name, selection.CanPush = name, true
				return selection, nil
			}
		}
	} else if len(names) == 1 {
		if binding, found := effective[names[0]]; found {
			return selectQueueBinding(selection, binding)
		}
		selection.Name, selection.CanPush = names[0], true
		return selection, nil
	} else if len(names) > 1 {
		return selection, queueBindingProblem("queue_name is required when the selected environment has multiple enabled queue consumers")
	}
	if len(effective) > 0 || len(names) > 0 || hasNamedBindings {
		return selection, queueBindingProblem(fmt.Sprintf("queue_name %q is not an enabled queue for the selected environment", requested))
	}
	selection.Name = requested
	return selection, nil
}

func selectQueueBinding(selection queueSendSelection, binding state.QueueBinding) (queueSendSelection, *api.Problem) {
	if binding.RetiredAt != nil {
		return selection, api.NewProblem(http.StatusConflict, "queue_binding_retired", "Queue binding retired", "this queue is held for explicit recovery")
	}
	if !binding.Enabled {
		return selection, queueBindingProblem("the selected queue binding is disabled")
	}
	selection.Name, selection.Binding, selection.CanPush = binding.QueueName, &binding, binding.Mode == "push"
	return selection, nil
}
