package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// sidecarsForDeployment resolves the persisted sidecar declarations and their
// immutable layer handles into the workload specs carried by a wake. The
// declaration order is intentional: it is the stable drive order used by
// imaged, vmmd, and guest-init. The storage rows are keyed by name because
// their SQL reader sorts by name, so they must never be used as the ordering
// source.
func (e *Engine) sidecarsForDeployment(ctx context.Context, dep state.Deployment) ([]fcvm.WorkloadSpec, error) {
	if len(dep.Sidecars) == 0 || string(dep.Sidecars) == "null" || string(dep.Sidecars) == "[]" {
		return nil, nil
	}
	layers, err := e.store.ListDeploymentSidecarLayers(ctx, dep.ID)
	if err != nil {
		return nil, fmt.Errorf("list sidecar layers: %w", err)
	}
	return sidecarSpecsFromDeployment(dep.Sidecars, layers)
}

func sidecarSpecsFromDeployment(raw json.RawMessage, layers []state.DeploymentSidecarLayer) ([]fcvm.WorkloadSpec, error) {
	var sidecars api.Sidecars
	if err := json.Unmarshal(raw, &sidecars); err != nil {
		return nil, fmt.Errorf("decode sidecars: %w", err)
	}
	if len(sidecars) == 0 {
		return nil, nil
	}
	if len(sidecars) > api.SidecarCapMax {
		return nil, fmt.Errorf("sidecar count %d exceeds cap %d", len(sidecars), api.SidecarCapMax)
	}

	byName := make(map[string]state.DeploymentSidecarLayer, len(layers))
	for _, layer := range layers {
		if layer.SidecarName == "" || layer.StorageKey == "" {
			return nil, fmt.Errorf("sidecar layer has incomplete handle (name=%q storage_key=%q)", layer.SidecarName, layer.StorageKey)
		}
		if _, exists := byName[layer.SidecarName]; exists {
			return nil, fmt.Errorf("duplicate sidecar layer %q", layer.SidecarName)
		}
		byName[layer.SidecarName] = layer
	}

	out := make([]fcvm.WorkloadSpec, 0, len(sidecars))
	seenNames := make(map[string]struct{}, len(sidecars))
	seenTypes := make(map[api.SidecarType]struct{}, len(sidecars))
	for i, sc := range sidecars {
		if err := validatePersistedSidecar(sc, seenNames, seenTypes); err != nil {
			return nil, err
		}
		layer, ok := byName[sc.Name]
		if !ok {
			return nil, fmt.Errorf("sidecar %q has no built layer", sc.Name)
		}
		essential := true
		if sc.Essential != nil {
			essential = *sc.Essential
		}
		sealedEnv, err := sealedSidecarEnv(sc)
		if err != nil {
			return nil, fmt.Errorf("sidecar %q env: %w", sc.Name, err)
		}
		out = append(out, fcvm.WorkloadSpec{
			Name:       sc.Name,
			Type:       string(sc.Type),
			Image:      sc.Image,
			StorageKey: layer.StorageKey,
			DriveID:    fmt.Sprintf("%s%d", fcvm.DriveSidecarPrefix, i),
			RamMB:      sc.RamMB,
			Port:       sc.Port,
			Essential:  essential,
			SealedEnv:  sealedEnv,
			DependsOn:  append([]api.WorkloadDependency(nil), sc.DependsOn...),
			// Cmd is retained as a legacy fallback for guest-init
			// versions that predate baked sidecar manifests. Current
			// guest-init prefers the immutable per-sidecar manifest,
			// which also carries the image Entrypoint and defaults.
			Cmd: append([]string(nil), sc.Cmd...),
		})
	}
	for name := range byName {
		if _, referenced := seenNames[name]; !referenced {
			return nil, fmt.Errorf("sidecar layer %q is not referenced by deployment", name)
		}
	}
	return out, nil
}

// sealedSidecarEnv decodes the base64 transport wrapper used by the persisted
// sidecar JSON. The returned values are still age ciphertext; only vmmd, with
// the host identity, opens them at wake. Sorting keys keeps the schedd-to-vmmd
// wire deterministic even though the JSON object is a map.
func sealedSidecarEnv(sc api.Sidecar) ([]fcvm.SealedEnvEntry, error) {
	if len(sc.Env) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(sc.Env))
	for key := range sc.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]fcvm.SealedEnvEntry, 0, len(keys))
	for _, key := range keys {
		ciphertext, err := base64.StdEncoding.DecodeString(sc.Env[key])
		if err != nil {
			return nil, fmt.Errorf("decode %q: %w", key, err)
		}
		out = append(out, fcvm.SealedEnvEntry{Key: key, Ciphertext: ciphertext})
	}
	return out, nil
}

func validatePersistedSidecar(sc api.Sidecar, seenNames map[string]struct{}, seenTypes map[api.SidecarType]struct{}) error {
	if !validPersistedSidecarName(sc.Name) {
		return fmt.Errorf("invalid sidecar name %q", sc.Name)
	}
	if sc.Type != api.SidecarTypeInit && sc.Type != api.SidecarTypeSidecar {
		return fmt.Errorf("sidecar %q has invalid type %q", sc.Name, sc.Type)
	}
	if _, exists := seenNames[sc.Name]; exists {
		return fmt.Errorf("duplicate sidecar name %q", sc.Name)
	}
	seenNames[sc.Name] = struct{}{}
	if _, exists := seenTypes[sc.Type]; exists {
		return fmt.Errorf("duplicate sidecar type %q", sc.Type)
	}
	seenTypes[sc.Type] = struct{}{}
	return nil
}

func validPersistedSidecarName(name string) bool {
	if name == "" || len(name) > 63 || strings.ContainsAny(name, "/\\") {
		return false
	}
	for i, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
		if i == 0 && ((r < 'a' || r > 'z') && (r < '0' || r > '9')) {
			return false
		}
	}
	return true
}
