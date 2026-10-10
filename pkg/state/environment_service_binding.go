package state

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// A scoped binding pins the target's physical identity. Workload is its
// logical Git name; it is never resolved through an app slug or another scope.
type EnvironmentScopedServiceBinding struct {
	Workload    string `json:"workload"`
	EnvKey      string `json:"env_key"`
	TargetAppID string `json:"target_app_id"`
}

func validateEnvironmentServiceBindings(appID string, bindings map[string]EnvironmentScopedServiceBinding) error {
	if len(bindings) > api.ServiceBindingTargetsMax {
		return ErrInvalidArgument
	}
	caller, _ := uuid.Parse(appID)
	keys := map[string]bool{}
	for name, binding := range bindings {
		id, err := uuid.Parse(binding.TargetAppID)
		if !api.ValidAppSlug(name) || !api.ValidAppSlug(binding.Workload) || api.ValidateEnvKey(binding.EnvKey) != nil ||
			err != nil || id == uuid.Nil || id == caller || keys[binding.EnvKey] {
			return ErrInvalidArgument
		}
		keys[binding.EnvKey] = true
	}
	return nil
}

func observeEnvironmentServiceBindings(out *EnvironmentGitOpsObservation, app gitOpsIntentApp, resource string) {
	if app.WorkloadIntent == nil {
		return
	}
	for name, binding := range app.WorkloadIntent.ServiceBindings {
		value, _ := json.Marshal(api.EnvironmentServiceBinding{Workload: binding.Workload, EnvKey: binding.EnvKey})
		out.State.Fields = append(out.State.Fields, environmentsync.Field{Resource: resource, Path: "service_bindings/" + name, Value: value})
		out.State.ResourceIDs[resource+"/service_bindings/"+name] = binding.TargetAppID
		if out.State.ResourceIDs["workload/"+binding.Workload] != binding.TargetAppID {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#service_bindings/"+name+": original scoped target mapping is absent or changed")
		}
		if _, exists := app.Variables[binding.EnvKey]; exists {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#service_bindings/"+name+": binding environment key overlaps an existing variable")
		}
		if _, exists := app.SecretRefs[binding.EnvKey]; exists {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#service_bindings/"+name+": binding environment key overlaps an existing secret reference")
		}
	}
}

func changeEnvironmentServiceBinding(row EnvironmentWorkloadIntent, change environmentsync.Change, value json.RawMessage, ids map[string]string) EnvironmentWorkloadIntent {
	row.ServiceBindings = maps.Clone(row.ServiceBindings)
	if row.ServiceBindings == nil {
		row.ServiceBindings = map[string]EnvironmentScopedServiceBinding{}
	}
	name := strings.TrimPrefix(change.Path, "service_bindings/")
	if change.Action == "remove" {
		delete(row.ServiceBindings, name)
	} else {
		var binding api.EnvironmentServiceBinding
		_ = json.Unmarshal(value, &binding)
		row.ServiceBindings[name] = EnvironmentScopedServiceBinding{Workload: binding.Workload, EnvKey: binding.EnvKey, TargetAppID: ids["workload/"+binding.Workload]}
	}
	return row
}

// Count generated keys across the app's environments, including retained
// unmanaged bindings and active overrides. Replacing a binding uses one slot.
func projectedEnvironmentServiceBindingCount(snapshot gitOpsIntentSnapshot, resource string, app gitOpsIntentApp, desired api.EnvironmentWorkload) int {
	count := app.BindingCount
	current := map[string]EnvironmentScopedServiceBinding{}
	if app.WorkloadIntent != nil {
		current = app.WorkloadIntent.ServiceBindings
	}
	for name := range desired.ServiceBindings {
		if _, exists := current[name]; !exists {
			count++
		}
	}
	if snapshot.Prune {
		for _, owner := range snapshot.Owners {
			if owner.Manager != snapshot.SourceID || owner.Resource != resource || !strings.HasPrefix(owner.Path, "service_bindings/") {
				continue
			}
			name := strings.TrimPrefix(owner.Path, "service_bindings/")
			_, exists := current[name]
			_, wanted := desired.ServiceBindings[name]
			overridden := false
			for _, override := range snapshot.Overrides {
				overridden = overridden || override.Resource == resource && override.Path == owner.Path && override.ExpiresAt.After(time.Now())
			}
			if exists && !wanted && !overridden {
				count--
			}
		}
	}
	return count
}

func (m *MemStore) validateEnvironmentServiceBindingTargetsLocked(row EnvironmentWorkloadIntent) error {
	if err := validateEnvironmentServiceBindings(row.AppID, row.ServiceBindings); err != nil {
		return err
	}
	for _, binding := range row.ServiceBindings {
		valid := false
		for _, memory := range m.environmentGitOps {
			if !memory.source.Detached && memory.source.AccountID == row.AccountID && memory.source.EnvironmentID == row.EnvironmentID &&
				memory.resources["workload/"+binding.Workload] == binding.TargetAppID {
				target := m.apps[binding.TargetAppID]
				valid = target.Status != AppDeleted && target.ID != "" && target.ProjectID == memory.source.ProjectID && target.AccountID == row.AccountID
			}
		}
		if !valid {
			return ErrConflict
		}
	}
	return nil
}
