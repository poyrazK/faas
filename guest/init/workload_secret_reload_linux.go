//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func prepareWorkloadRuntimeSecrets(imageRoot string, manifest api.AppManifest, initial map[string]string, projectionPath, revisionPath string) (*runtimeSecretsState, error) {
	credential, err := processCredential(imageRoot, manifest.EffectiveUser())
	if err != nil {
		return nil, fmt.Errorf("resolve secret projection owner: %w", err)
	}
	return newProjectedRuntimeSecretsState(initial, runtimeSecretProjection{
		secretsPath: projectionPath, revisionPath: revisionPath, uid: int(credential.Uid),
	})
}

// Prepare sidecar projections before workers can fetch updates, even when
// dependencies delay the sidecar's first process start. Restarts never publish.
func newSidecarWorkloadRuntimeAt(root string, spec workloadSpec, apiEnv map[string]string, log *slog.Logger, proxy *sidecarEventsProxy, workloadEnv map[string]string) (*workloadRuntime, error) {
	manifest, found, err := sidecarManifestForRuntimeAt(root, spec.Name)
	if err != nil {
		return nil, fmt.Errorf("workload %q: load runtime manifest: %w", spec.Name, err)
	}
	runtime := &workloadRuntime{spec: spec, state: newWorkloadDependencyState()}
	if found && manifest.SecretReloadSignal != "" && len(spec.GrantedEnvNames) > 0 && spec.Type == "sidecar" {
		initial, err := sidecarInitialRuntimeSecrets(root, spec)
		if err != nil {
			return nil, err
		}
		directRoot, err := fullRootfsSidecarRootAt(root, spec.Name)
		if err != nil {
			return nil, fmt.Errorf("workload %q: resolve secret projection root: %w", spec.Name, err)
		}
		projectionPath, revisionPath := sidecarSecretReloadProjectionPaths(spec.Name, directRoot)
		imageRoot := directRoot
		if directRoot == "" {
			imageRoot = root
			projectionPath = filepath.Join(root, strings.TrimPrefix(projectionPath, "/"))
			revisionPath = filepath.Join(root, strings.TrimPrefix(revisionPath, "/"))
		}
		runtime.spec.runtimeSecrets, err = prepareWorkloadRuntimeSecrets(imageRoot, manifest, initial, projectionPath, revisionPath)
		if err != nil {
			return nil, fmt.Errorf("workload %q: %w", spec.Name, err)
		}
		runtime.secretManifest = &manifest
	}
	runtime.sup = newSupervisorForAt(root, runtime.spec, apiEnv, log, proxy, workloadEnv)
	if found {
		runtime.sup.stopSignal = parseStopSignal(manifest.StopSignal)
		runtime.sup.stopGrace = stopGraceForManifest(manifest.StopGracePeriod)
	}
	return runtime, nil
}

func sidecarInitialRuntimeSecrets(root string, spec workloadSpec) (map[string]string, error) {
	env, err := loadSidecarEnvAt(root, spec.Name)
	if err != nil && !isNotExist(err) {
		return nil, fmt.Errorf("workload %q: load secret grants: %w", spec.Name, err)
	}
	initial := make(map[string]string, len(spec.GrantedEnvNames))
	for _, key := range spec.GrantedEnvNames {
		value, ok := env[key]
		if !ok {
			return nil, fmt.Errorf("workload %q: granted secret %q is missing from wake-time env", spec.Name, key)
		}
		initial[key] = value
	}
	return initial, nil
}

func applySidecarRuntimeEnv(base []string, overrides map[string]string, spec workloadSpec) []string {
	env, _ := applySidecarRuntimeEnvSnapshot(base, overrides, spec)
	return env
}

func applySidecarRuntimeEnvSnapshot(base []string, overrides map[string]string, spec workloadSpec) ([]string, *runtimeSecretSnapshot) {
	if spec.runtimeSecrets == nil {
		return applySidecarEnvOverrides(base, overrides), nil
	}
	// Remove every granted key from all older layers before applying the live
	// projection. Missing keys represent revocations, not a fallback to boot.
	granted := make(map[string]struct{}, len(spec.GrantedEnvNames))
	snapshot := spec.runtimeSecrets.startupSnapshot()
	current := snapshot.Secrets
	values := cloneRuntimeSecrets(overrides)
	for _, key := range spec.GrantedEnvNames {
		granted[key] = struct{}{}
		delete(values, key)
		if value, ok := current[key]; ok {
			values[key] = value
		}
	}
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, secret := granted[key]; !secret {
			filtered = append(filtered, entry)
		}
	}
	return applySidecarEnvOverrides(filtered, values), &snapshot
}
