package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrProjectEnvironmentQueueCollectionUnavailable = fmt.Errorf("environment queue collection is unavailable: %w", ErrConflict)
	ErrProjectEnvironmentQueueActivationUnavailable = fmt.Errorf("environment queue activation proof is unavailable: %w", ErrConflict)
	environmentQueueNameRE                          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

// Queue settings contain logical names and desired delivery configuration.
// Consumer IDs, leases, counters and messages belong to runtime activation.
type ProjectEnvironmentQueueDefinition struct {
	Name            string          `json:"name"`
	QueueName       string          `json:"queue_name"`
	Mode            string          `json:"mode"`
	WorkloadClass   WorkloadClass   `json:"workload_class"`
	Enabled         bool            `json:"enabled"`
	MaxConcurrency  int             `json:"max_concurrency"`
	RetryPolicyJSON json.RawMessage `json:"retry_policy"`
}

type ProjectEnvironmentQueueSettings struct {
	Revision int64                               `json:"revision"`
	Bindings []ProjectEnvironmentQueueDefinition `json:"bindings"`
}

func normalizeEnvironmentQueues(bindings []ProjectEnvironmentQueueDefinition) ([]ProjectEnvironmentQueueDefinition, error) {
	bindings = append([]ProjectEnvironmentQueueDefinition{}, bindings...)
	names, queues := map[string]bool{}, map[string]bool{}
	for i := range bindings {
		binding := &bindings[i]
		if !environmentQueueNameRE.MatchString(binding.Name) || !environmentQueueNameRE.MatchString(binding.QueueName) || names[binding.Name] || queues[binding.QueueName] ||
			(binding.Mode != "pull" && binding.Mode != "push") || binding.MaxConcurrency < 1 || binding.MaxConcurrency > 10000 ||
			(binding.WorkloadClass != WorkloadClassWorker && binding.WorkloadClass != WorkloadClassJob && !(binding.WorkloadClass == WorkloadClassHTTP && binding.Mode == "push")) {
			return nil, ErrInvalidArgument
		}
		names[binding.Name], queues[binding.QueueName] = true, true
		raw := binding.RetryPolicyJSON
		if len(raw) == 0 {
			raw = []byte(`{}`)
		}
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			return nil, ErrInvalidArgument
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var policy api.RetryPolicyDTO
		if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || policy.MaxAttempts < 0 || policy.MaxAttempts > 25 ||
			policy.BaseSeconds < 0 || policy.BaseSeconds > 3600 || policy.MaxSeconds < 0 || policy.MaxSeconds > 86400 || policy.JitterSeconds < 0 || policy.JitterSeconds > 1 ||
			(policy.MaxSeconds > 0 && policy.BaseSeconds > policy.MaxSeconds) {
			return nil, ErrInvalidArgument
		}
		binding.RetryPolicyJSON, _ = json.Marshal(policy)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Name < bindings[j].Name })
	return bindings, nil
}

func normalizeWorkloadQueueSettings(settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSettings, error) {
	if settings.QueueBindings == nil {
		return settings, nil
	}
	book := *settings.QueueBindings
	var err error
	book.Bindings, err = normalizeEnvironmentQueues(book.Bindings)
	if err != nil || book.Revision < 1 {
		return settings, ErrInvalidArgument
	}
	for _, binding := range book.Bindings {
		if (binding.WorkloadClass == WorkloadClassHTTP && settings.Type != AppTypeFunction) || (settings.WorkloadClass != "" && settings.WorkloadClass != binding.WorkloadClass) {
			return settings, ErrInvalidArgument
		}
	}
	settings.QueueBindings = &book
	return settings, nil
}

func EnvironmentQueueBindings(ctx context.Context, store ProjectEnvironmentWorkloadSpecReader, app App, environment string) ([]ProjectEnvironmentQueueDefinition, ProjectEnvironmentWorkloadSpec, error) {
	spec, err := store.ProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, environment, app.ID)
	if err != nil {
		return nil, spec, err
	}
	if err := validateEnvironmentWorkPolicySpec(spec, app, environment); err != nil {
		return nil, spec, err
	}
	if spec.Settings.QueueBindings == nil {
		return nil, spec, ErrProjectEnvironmentQueueCollectionUnavailable
	}
	settings, err := normalizeWorkloadQueueSettings(spec.Settings)
	if err != nil {
		return nil, spec, ErrConflict
	}
	return settings.QueueBindings.Bindings, spec, nil
}

// Queue edits share the workload CAS and environment protection transaction.
// Production's legacy tables are never selected or modified by these helpers.
func ReplaceEnvironmentQueueBindings(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment string, expectedRevision int64, bindings []ProjectEnvironmentQueueDefinition) (ProjectEnvironmentWorkloadSpec, error) {
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
	return replaceEnvironmentQueueBindings(ctx, store, app, env, current, bindings)
}

func replaceEnvironmentQueueBindings(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, env ProjectEnvironment, current ProjectEnvironmentWorkloadSpec, bindings []ProjectEnvironmentQueueDefinition) (ProjectEnvironmentWorkloadSpec, error) {
	bindings, err := normalizeEnvironmentQueues(bindings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	clock := int64(0)
	changed := true
	if old := current.Settings.QueueBindings; old != nil {
		clock = old.Revision
		before, _ := json.Marshal(old.Bindings)
		after, _ := json.Marshal(bindings)
		changed = !bytes.Equal(before, after)
	}
	if changed {
		if clock == math.MaxInt64 {
			return ProjectEnvironmentWorkloadSpec{}, ErrConflict
		}
		clock++
	}
	current.Settings.QueueBindings = &ProjectEnvironmentQueueSettings{Revision: clock, Bindings: bindings}
	return store.PutUnprotectedProjectEnvironmentWorkloadSpec(ctx, app.AccountID, app.ProjectID, env.ID, app.ID, current.Revision, current.Settings)
}

func UpsertEnvironmentQueueBinding(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment string, expectedRevision *int64, binding ProjectEnvironmentQueueDefinition) (ProjectEnvironmentWorkloadSpec, error) {
	env, current, err := environmentWorkPolicyEditHead(ctx, store, app, environment)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	bindings := []ProjectEnvironmentQueueDefinition{}
	if current.Settings.QueueBindings != nil {
		bindings = append(bindings, current.Settings.QueueBindings.Bindings...)
	}
	found := false
	for i := range bindings {
		if bindings[i].Name == binding.Name {
			bindings[i] = binding
			found = true
		}
	}
	if !found {
		bindings = append(bindings, binding)
	}
	return replaceEnvironmentQueueBindings(ctx, store, app, env, current, bindings)
}

func DeleteEnvironmentQueueBinding(ctx context.Context, store ProjectEnvironmentWorkPolicyStore, app App, environment, name string, expectedRevision *int64) (ProjectEnvironmentWorkloadSpec, error) {
	env, current, err := environmentWorkPolicyEditHead(ctx, store, app, environment)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	if current.Settings.QueueBindings == nil {
		return ProjectEnvironmentWorkloadSpec{}, ErrProjectEnvironmentQueueCollectionUnavailable
	}
	bindings := []ProjectEnvironmentQueueDefinition{}
	for _, binding := range current.Settings.QueueBindings.Bindings {
		if binding.Name != name {
			bindings = append(bindings, binding)
		}
	}
	if len(bindings) == len(current.Settings.QueueBindings.Bindings) {
		return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	return replaceEnvironmentQueueBindings(ctx, store, app, env, current, bindings)
}

func QueueBindingsForDeployment(ctx context.Context, store DeploymentWorkloadSpecReader, app App, deploymentID string) ([]ProjectEnvironmentQueueDefinition, error) {
	spec, err := store.ProjectEnvironmentWorkloadSpecForDeployment(ctx, app.AccountID, app.ProjectID, deploymentID)
	if err != nil {
		return nil, err
	}
	if err := validateEnvironmentWorkPolicySpec(spec, app, spec.EnvironmentSlug); err != nil {
		return nil, err
	}
	if spec.Settings.QueueBindings == nil {
		return nil, ErrProjectEnvironmentQueueCollectionUnavailable
	}
	settings, err := normalizeWorkloadQueueSettings(spec.Settings)
	if err != nil {
		return nil, ErrConflict
	}
	return settings.QueueBindings.Bindings, nil
}
