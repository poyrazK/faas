package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

var ErrProjectEnvironmentWorkPolicyCollectionUnavailable = fmt.Errorf("environment work policy collection is unavailable: %w", ErrConflict)
var ErrProjectEnvironmentWorkPolicyActivationUnavailable = fmt.Errorf("environment work policy activation proof is unavailable: %w", ErrConflict)

// Revision is a collection clock retained even when the last policy is removed.
// Recreating a name cannot reuse a prior policy revision in this environment.
type ProjectEnvironmentWorkPolicySettings struct {
	Revision int64                               `json:"revision"`
	Policies []ProjectEnvironmentCloneWorkPolicy `json:"policies"`
}

func normalizeEnvironmentWorkPolicySettings(settings ProjectEnvironmentWorkPolicySettings) (ProjectEnvironmentWorkPolicySettings, error) {
	policies, err := normalizeEnvironmentWorkPolicies(settings.Policies)
	if err != nil {
		return settings, err
	}
	if settings.Revision < 1 {
		return settings, ErrInvalidArgument
	}
	for _, policy := range policies {
		if policy.Revision > settings.Revision {
			return settings, ErrInvalidArgument
		}
	}
	settings.Policies = policies
	return settings, nil
}

type ProjectEnvironmentWorkPolicyStore interface {
	ProjectEnvironmentWorkloadSpecStore
	ProjectEnvironmentBySlug(context.Context, string, string, string) (ProjectEnvironment, error)
}

// Policies share the immutable workload-settings revision. They do not carry
// source producer IDs or operational queues into a target environment.
func normalizeEnvironmentWorkPolicies(policies []ProjectEnvironmentCloneWorkPolicy) ([]ProjectEnvironmentCloneWorkPolicy, error) {
	if len(policies) > api.MaxWorkPoliciesPerApp {
		return nil, ErrQuotaExceeded
	}
	policies, err := normalizeWorkPolicyDefinitions(policies)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	return policies, nil
}

func environmentWorkPolicyDefinition(policy workpolicy.Policy, revision int64) (ProjectEnvironmentCloneWorkPolicy, error) {
	if err := policy.Validate(); err != nil || policy.Debounce%time.Millisecond != 0 || policy.ExpiresAfter%time.Millisecond != 0 {
		return ProjectEnvironmentCloneWorkPolicy{}, ErrInvalidArgument
	}
	if policy.PendingUpdates == "" {
		policy.PendingUpdates = workpolicy.PendingAll
	}
	return ProjectEnvironmentCloneWorkPolicy{Name: policy.Name, Revision: revision, MaxRunningPerKey: policy.MaxRunningPerKey,
		MaxRunningPerFairnessKey: policy.MaxRunningPerFairnessKey, PendingUpdates: string(policy.PendingUpdates),
		DebounceMS: policy.Debounce.Milliseconds(), ExpiresAfterMS: policy.ExpiresAfter.Milliseconds()}, nil
}

func environmentWorkPolicyRecord(app App, definition ProjectEnvironmentCloneWorkPolicy, at time.Time) AppWorkPolicy {
	return AppWorkPolicy{AccountID: app.AccountID, AppID: app.ID, Revision: definition.Revision, CreatedAt: at, UpdatedAt: at,
		Policy: workpolicy.Policy{Name: definition.Name, MaxRunningPerKey: definition.MaxRunningPerKey,
			MaxRunningPerFairnessKey: definition.MaxRunningPerFairnessKey, PendingUpdates: workpolicy.PendingUpdates(definition.PendingUpdates),
			Debounce: time.Duration(definition.DebounceMS) * time.Millisecond, ExpiresAfter: time.Duration(definition.ExpiresAfterMS) * time.Millisecond}}
}

// Missing scoped material cannot read live application-wide policies. Callers
// decide explicitly whether a legacy production collection is still applicable.
func EnvironmentWorkPolicies(ctx context.Context, store ProjectEnvironmentWorkloadSpecReader, app App, environment string) ([]AppWorkPolicy, ProjectEnvironmentWorkloadSpec, error) {
	if app.ProjectID == "" || !api.ValidProjectEnvironmentSlug(environment) {
		return nil, ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	spec, err := store.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment, app.ID)
	if err != nil {
		return nil, spec, err
	}
	if err := validateEnvironmentWorkPolicySpec(spec, app, environment); err != nil {
		return nil, spec, err
	}
	if spec.Settings.WorkPolicies == nil {
		return nil, spec, ErrProjectEnvironmentWorkPolicyCollectionUnavailable
	}
	settings, err := normalizeEnvironmentWorkPolicySettings(*spec.Settings.WorkPolicies)
	if err != nil {
		return nil, spec, ErrConflict
	}
	out := make([]AppWorkPolicy, 0, len(settings.Policies))
	for _, policy := range settings.Policies {
		out = append(out, environmentWorkPolicyRecord(app, policy, spec.CreatedAt))
	}
	return out, spec, nil
}

// Replacing a complete collection preserves unrelated desired settings and
// uses the same environment protection and compare-and-swap as other edits.
func ReplaceEnvironmentWorkPolicies(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment string, expectedRevision int64, policies []ProjectEnvironmentCloneWorkPolicy) (ProjectEnvironmentWorkloadSpec, error) {
	if expectedRevision < 0 {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	env, current, err := environmentWorkPolicyEditHead(ctx, store, app, environment)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if current.Revision != expectedRevision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	return replaceEnvironmentWorkPolicies(ctx, store, app, env, current, policies)
}

func validateEnvironmentWorkPolicySpec(spec ProjectEnvironmentWorkloadSpec, app App, environment string) error {
	if app.ID == "" || app.AccountID == "" || app.ProjectID == "" || !api.ValidProjectEnvironmentSlug(environment) ||
		spec.ID == "" || spec.EnvironmentID == "" || spec.Revision < 1 || spec.AccountID != app.AccountID || spec.ProjectID != app.ProjectID ||
		spec.AppID != app.ID || spec.EnvironmentSlug != environment {
		return ErrConflict
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return ErrConflict
	}
	return nil
}

func environmentWorkPolicyEditHead(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment string) (ProjectEnvironment, ProjectEnvironmentWorkloadSpec, error) {
	if app.ProjectID == "" || environment == "production" || !api.ValidProjectEnvironmentSlug(environment) {
		return ProjectEnvironment{}, ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, environment)
	if err != nil {
		return env, ProjectEnvironmentWorkloadSpec{}, err
	}
	if env.ID == "" || env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || env.Slug != environment {
		return env, ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	current, err := store.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment, app.ID)
	if errors.Is(err, ErrNotFound) {
		current = ProjectEnvironmentWorkloadSpec{}
		current.Settings, err = MaterializeEnvironmentWorkloadSettings(ctx, store, app, environment)
	} else if err == nil {
		err = validateEnvironmentWorkPolicySpec(current, app, environment)
		if current.EnvironmentID != env.ID {
			err = ErrConflict
		}
	}
	return env, current, err
}

// Revisions supplied by callers are not authority. Preserve unchanged policy
// revisions and assign every new/changed definition the next collection clock.
func replaceEnvironmentWorkPolicies(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, env ProjectEnvironment, current ProjectEnvironmentWorkloadSpec, policies []ProjectEnvironmentCloneWorkPolicy) (ProjectEnvironmentWorkloadSpec, error) {
	policies = append([]ProjectEnvironmentCloneWorkPolicy{}, policies...)
	for i := range policies {
		policies[i].Revision = 1
	}
	policies, err := normalizeEnvironmentWorkPolicies(policies)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	clock, changed := int64(0), current.Settings.WorkPolicies == nil
	previous := map[string]ProjectEnvironmentCloneWorkPolicy{}
	if current.Settings.WorkPolicies != nil {
		clock = current.Settings.WorkPolicies.Revision
		changed = len(policies) != len(current.Settings.WorkPolicies.Policies)
		for _, policy := range current.Settings.WorkPolicies.Policies {
			previous[policy.Name] = policy
		}
	}
	for i, policy := range policies {
		old, exists := previous[policy.Name]
		old.Revision = 1
		if exists && old == policy {
			policies[i].Revision = previous[policy.Name].Revision
		} else {
			policies[i].Revision = 0
			changed = true
		}
	}
	if changed {
		if clock == math.MaxInt64 {
			return ProjectEnvironmentWorkloadSpec{}, ErrConflict
		}
		clock++
	}
	for i := range policies {
		if policies[i].Revision == 0 {
			policies[i].Revision = clock
		}
	}
	current.Settings.WorkPolicies = &ProjectEnvironmentWorkPolicySettings{Revision: clock, Policies: policies}
	// Even a no-op checks protection and CAS inside the store's transaction.
	return store.PutUnprotectedProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, env.ID, app.ID, current.Revision, current.Settings)
}

// A deployment reads its own immutable collection, independent of later
// desired edits. An absent collection cannot fall back to current production.
func WorkPolicyForDeployment(ctx context.Context, store DeploymentWorkloadSpecReader, app App, deploymentID, name string) (AppWorkPolicy, error) {
	spec, err := store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, app.AccountID, app.ProjectID, deploymentID)
	if err != nil {
		return AppWorkPolicy{}, err
	}
	if err := validateEnvironmentWorkPolicySpec(spec, app, spec.EnvironmentSlug); err != nil {
		return AppWorkPolicy{}, err
	}
	if spec.Settings.WorkPolicies == nil {
		return AppWorkPolicy{}, ErrProjectEnvironmentWorkPolicyCollectionUnavailable
	}
	settings, err := normalizeEnvironmentWorkPolicySettings(*spec.Settings.WorkPolicies)
	if err != nil {
		return AppWorkPolicy{}, ErrConflict
	}
	for _, policy := range settings.Policies {
		if policy.Name == name {
			return environmentWorkPolicyRecord(app, policy, spec.CreatedAt), nil
		}
	}
	return AppWorkPolicy{}, ErrNotFound
}
