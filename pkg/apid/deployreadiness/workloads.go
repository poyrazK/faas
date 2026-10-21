package deployreadiness

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func readBaseline(ctx context.Context, r *report, store Reader, account, project string, req api.ProjectDeploymentCheckRequest) state.ProjectReleaseSet {
	if project == "" {
		if req.Atomic {
			r.add("baseline_required", "blocked", "", "Atomic deployment requires an existing project release.", "Deploy the project and enable revision retention before requesting an atomic release.")
		}
		return state.ProjectReleaseSet{}
	}
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		r.add("baseline_unavailable", "unknown", "", "Release graph inventory is unavailable.", "Use a server with release graph support.")
		return state.ProjectReleaseSet{}
	}
	baseline, err := reader.ActiveProjectReleaseSet(ctx, account, project, req.Environment)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			r.add("baseline_unavailable", "unknown", "", "The active release graph could not be read.", "Retry when release state is available.")
		} else if req.Atomic {
			r.add("baseline_required", "blocked", "", "No active baseline release exists in this environment.", "Enable revision retention and deploy a complete baseline release.")
		}
		return state.ProjectReleaseSet{}
	}
	r.identities["baseline"] = baseline
	return baseline
}

func checkWorkload(ctx context.Context, r *report, store Reader, account state.Account, req api.ProjectDeploymentCheckRequest, item api.ProjectDeploymentCheckWorkload, app *state.App, baseline state.ProjectReleaseSet) {
	w := item.Workload
	available := map[string]bool{}
	for _, key := range item.ProvidedKeys {
		available[key] = true
	}
	var config api.ProjectReleaseConfigRequest
	if len(req.ReleaseConfig) > 0 && json.Unmarshal(req.ReleaseConfig, &config) == nil {
		for key := range config.Env[w.Name] {
			available[key] = true
		}
		for key := range config.Secrets[w.Name] {
			available[key] = true
		}
	}
	if app != nil {
		r.identities["app:"+w.Name] = struct {
			ID, Name, Root   string
			Manifest         state.AppManifest
			RAM, Concurrency int
		}{app.ID, app.WorkloadName, app.RootDir, app.Manifest, app.RAMMB, app.MaxConcurrency}
		raw, _ := json.Marshal(app.Manifest)
		var manifest api.AppManifest
		_ = json.Unmarshal(raw, &manifest)
		if err := manifest.ValidateLifecyclePlan(account.Plan); err != nil {
			r.add("workload_plan_incompatible", "blocked", w.Name, "Existing workload settings are incompatible with the selected plan.", "Review workload lifecycle and resource settings against the account plan.")
		}
		if !loadRuntimeKeys(ctx, r, store, account.ID, *app, baseline, req.Environment, available) {
			return
		}
	}
	// An explicit variable map replaces the previous variable set, including
	// an empty map. Secret keys retain their separate delivery path.
	if variables, present := config.Env[w.Name]; present {
		for key := range available {
			if app != nil && !isSecretKey(r, w.Name, key) {
				delete(available, key)
			}
		}
		for key := range variables {
			available[key] = true
		}
		for _, key := range item.ProvidedKeys {
			available[key] = true
		}
		for key := range config.Secrets[w.Name] {
			available[key] = true
		}
	}
	if versions, present := config.Secrets[w.Name]; present {
		previous, _ := r.identities["secret_keys:"+w.Name].(map[string]bool)
		for key := range previous {
			delete(available, key)
		}
		for key := range versions {
			available[key] = true
		}
		for _, key := range item.ProvidedKeys {
			available[key] = true
		}
	}
	for _, dependency := range w.DependsOn {
		for _, target := range req.Workloads {
			if target.Workload.Name == dependency {
				available[api.ServiceBindingEnvKey(dependency)] = true
				break
			}
		}
	}
	missing := map[string]bool{}
	for _, key := range w.EnvKeys {
		if !available[key] {
			missing[key] = true
		}
	}
	for _, key := range sortedKeys(missing) {
		r.add("runtime_key_missing", "blocked", w.Name, "Required runtime key "+key+" is not available in the selected environment.", "Configure "+key+" for this environment or include it in the proposed configuration or secrets file.")
	}
	if len(missing) == 0 {
		r.add("runtime_keys_available", "passed", w.Name, "Declared runtime keys are available in the selected environment or proposed input.", "")
	}
}

func isSecretKey(r *report, workload, key string) bool {
	keys, _ := r.identities["secret_keys:"+workload].(map[string]bool)
	return keys[key]
}

func loadRuntimeKeys(ctx context.Context, r *report, store Reader, account string, app state.App, baseline state.ProjectReleaseSet, environment string, keys map[string]bool) bool {
	for _, member := range baseline.Members {
		if member.AppID != app.ID {
			continue
		}
		reader, ok := store.(state.RuntimeAppValuesStore)
		if !ok {
			r.add("runtime_inputs_unavailable", "unknown", app.WorkloadName, "Effective release configuration is unavailable.", "Use a server with deployment-aware configuration readers.")
			return false
		}
		snapshot, err := reader.RuntimeAppValuesForDeployment(ctx, account, app.ID, member.DeploymentID)
		if err != nil {
			r.add("runtime_inputs_unavailable", "unknown", app.WorkloadName, "Effective release configuration could not be read.", "Retry when configuration and secret metadata are available.")
			return false
		}
		fence, err := state.NewRuntimeAppConfigFence(snapshot)
		if err != nil {
			r.add("runtime_inputs_unavailable", "unknown", app.WorkloadName, "Effective release configuration identity could not be checked.", "Retry when configuration metadata is available.")
			return false
		}
		r.identities["runtime:"+app.WorkloadName] = fence.Fingerprint
		for _, value := range snapshot.Values {
			keys[value.Key] = true
		}
		secretKeys := map[string]bool{}
		for _, secret := range snapshot.Secrets {
			keys[secret.Key] = true
			secretKeys[secret.Key] = true
		}
		r.identities["secret_keys:"+app.WorkloadName] = secretKeys
		return true
	}
	variables, envErr := store.ListAppEnvInScope(ctx, account, app.ID, environment)
	secrets, secretErr := store.ListAppSecretsInScope(ctx, account, app.ID, environment)
	if envErr != nil || secretErr != nil {
		r.add("runtime_inputs_unavailable", "unknown", app.WorkloadName, "Environment configuration or secret metadata could not be read.", "Retry when configuration metadata is available.")
		return false
	}
	r.identities["variables:"+app.WorkloadName] = variables
	secretKeys := map[string]bool{}
	for _, variable := range variables {
		keys[variable.Key] = true
	}
	logicalSecrets := map[string]any{}
	for _, secret := range secrets {
		keys[secret.Key] = true
		secretKeys[secret.Key] = true
		logicalSecrets[secret.Key] = struct {
			Hash, Class, Postgres, Object, Credential string
			Version, Generation                       int64
		}{secret.ValueHash, secret.SecretClass, secret.ManagedPostgresBindingID, secret.ManagedObjectStorageCredentialID, secret.ManagedCredentialRef, secret.SecretVersion, secret.ManagedCredentialGeneration}
	}
	r.identities["secrets:"+app.WorkloadName] = logicalSecrets
	r.identities["secret_keys:"+app.WorkloadName] = secretKeys
	return true
}
