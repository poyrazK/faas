package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// An optional expected revision lets a client fence an edit against the complete
// desired workload config. The store also fences concurrent read-modify-writes.
func UpsertEnvironmentWorkPolicy(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment string, expectedRevision *int64, policy workpolicy.Policy) (AppWorkPolicy, ProjectEnvironmentWorkloadSpec, error) {
	definition, err := environmentWorkPolicyDefinition(policy, 1)
	if err != nil {
		return AppWorkPolicy{}, ProjectEnvironmentWorkloadSpec{}, err
	}
	env, current, err := environmentWorkPolicyEditHead(ctx, store, app, environment)
	if err != nil {
		return AppWorkPolicy{}, current, err
	}
	if expectedRevision != nil && (*expectedRevision < 0 || *expectedRevision != current.Revision) {
		return AppWorkPolicy{}, current, ErrConflict
	}
	policies := []ProjectEnvironmentCloneWorkPolicy{}
	if current.Settings.WorkPolicies != nil {
		policies = append(policies, current.Settings.WorkPolicies.Policies...)
	}
	found := false
	for i := range policies {
		if policies[i].Name == policy.Name {
			policies[i], found = definition, true
			break
		}
	}
	if !found {
		policies = append(policies, definition)
	}
	spec, err := replaceEnvironmentWorkPolicies(ctx, store, app, env, current, policies)
	if err != nil {
		return AppWorkPolicy{}, spec, err
	}
	for _, configured := range spec.Settings.WorkPolicies.Policies {
		if configured.Name == policy.Name {
			return environmentWorkPolicyRecord(app, configured, spec.CreatedAt), spec, nil
		}
	}
	return AppWorkPolicy{}, spec, ErrConflict
}

func DeleteEnvironmentWorkPolicy(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment, name string, expectedRevision *int64) (ProjectEnvironmentWorkloadSpec, error) {
	env, current, err := environmentWorkPolicyEditHead(ctx, store, app, environment)
	if err != nil {
		return current, err
	}
	if expectedRevision != nil && (*expectedRevision < 0 || *expectedRevision != current.Revision) {
		return current, ErrConflict
	}
	if current.Settings.WorkPolicies == nil {
		return current, ErrProjectEnvironmentWorkPolicyCollectionUnavailable
	}
	policies := make([]ProjectEnvironmentCloneWorkPolicy, 0, len(current.Settings.WorkPolicies.Policies))
	found := false
	for _, policy := range current.Settings.WorkPolicies.Policies {
		if policy.Name == name {
			found = true
		} else {
			policies = append(policies, policy)
		}
	}
	if !found {
		return current, ErrNotFound
	}
	return replaceEnvironmentWorkPolicies(ctx, store, app, env, current, policies)
}
