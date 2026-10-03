package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeSecretsStore interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
	ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
}

type runtimeSidecarSecretReloadSignalStore interface {
	DeploymentSidecarSecretReloadSignal(context.Context, string, string) (string, error)
}

type runtimeSecretSelection struct {
	Rows     []state.AppSecret
	Entries  []fcvm.SealedEnvEntry
	Revision string
}

func selectRuntimeSecretRowsForWorkload(ctx context.Context, store runtimeSecretsStore, deploymentID, appID, accountID, workloadName string) (runtimeSecretSelection, error) {
	if ctx == nil || store == nil || deploymentID == "" || appID == "" || accountID == "" {
		return runtimeSecretSelection{}, errors.New("runtime secret selection dependencies are not configured")
	}
	if !state.ValidSecretRuntimeWorkloadName(workloadName) {
		return runtimeSecretSelection{}, errors.New("invalid runtime secret workload name")
	}
	deployment, err := store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return runtimeSecretSelection{}, fmt.Errorf("load deployment: %w", err)
	}
	if deployment.ID != deploymentID || deployment.AppID != appID {
		return runtimeSecretSelection{}, errors.New("live instance and deployment identity mismatch")
	}
	allowedKeys, err := runtimeSecretWorkloadAllowlist(deployment, workloadName)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	scope := deployment.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if api.ValidateScope(scope) != nil {
		return runtimeSecretSelection{}, errors.New("deployment has invalid secret scope")
	}
	rows, err := store.ListAppSecretsInScope(ctx, accountID, appID, scope)
	if err != nil {
		return runtimeSecretSelection{}, fmt.Errorf("list app secrets: %w", err)
	}
	requested := make(map[string]string, len(allowedKeys))
	for key := range allowedKeys {
		requested[key] = "secret:" + key
	}
	// Missing keys retain the existing reload/revocation semantics below.
	// Only request existing keys here; policy still rejects explicit DDL grants.
	existingRequested := make(map[string]string)
	for _, row := range rows {
		if ref, ok := requested[row.Key]; ok {
			existingRequested[row.Key] = ref
		}
	}
	eligible, err := state.SelectAppSecretsForDelivery(rows, existingRequested, false)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	rows = eligible
	selected := make([]state.AppSecret, 0, len(rows))
	entries := make([]fcvm.SealedEnvEntry, 0, len(rows))
	foundKeys := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.AccountID != accountID || row.AppID != appID || row.Scope != scope {
			return runtimeSecretSelection{}, errors.New("secret store returned a mismatched identity")
		}
		if api.ValidateEnvKey(row.Key) != nil {
			return runtimeSecretSelection{}, errors.New("secret store returned an invalid key")
		}
		if allowedKeys != nil {
			if _, ok := allowedKeys[row.Key]; !ok {
				continue
			}
		}
		selected = append(selected, row)
		entries = append(entries, fcvm.SealedEnvEntry{Key: row.Key, Ciphertext: row.Ciphertext})
		foundKeys[row.Key] = struct{}{}
	}
	// A missing grant must remain an error for restart-only workloads, since
	// they cannot receive a live projection update. For a workload that opted
	// into reload, an absent granted key is a revocation: return the current
	// projection so guest-init removes the key and signals that workload.
	if allowedKeys != nil {
		var missing []string
		for key := range allowedKeys {
			if _, ok := foundKeys[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			enabled, signalErr := runtimeSecretReloadEnabled(ctx, store, deployment, workloadName)
			if signalErr != nil {
				return runtimeSecretSelection{}, fmt.Errorf("load runtime secret reload opt-in: %w", signalErr)
			}
			if !enabled {
				return runtimeSecretSelection{}, fmt.Errorf("deployment references missing secrets: %s", strings.Join(missing, ", "))
			}
		}
	}
	revision := runtimeSecretRevision(scope, selected)
	return runtimeSecretSelection{Rows: selected, Entries: entries, Revision: revision}, nil
}

func runtimeSecretReloadEnabled(ctx context.Context, store runtimeSecretsStore, deployment state.Deployment, workloadName string) (bool, error) {
	if workloadName == "" {
		return deployment.SecretReloadSignal != "", nil
	}
	signalStore, ok := store.(runtimeSidecarSecretReloadSignalStore)
	if !ok {
		return false, nil
	}
	signal, err := signalStore.DeploymentSidecarSecretReloadSignal(ctx, deployment.ID, workloadName)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return signal != "", nil
}

func runtimeSecretWorkloadAllowlist(deployment state.Deployment, workloadName string) (map[string]struct{}, error) {
	if workloadName == "" {
		if _, err := runtimeDeploymentHasSidecars(deployment.Sidecars); err != nil {
			return nil, fmt.Errorf("decode deployment sidecars: %w", err)
		}
		allowed, err := runtimeSecretAllowlist(deployment.OverrideEnvSecrets)
		if err != nil {
			return nil, fmt.Errorf("decode deployment secret allowlist: %w", err)
		}
		return allowed, nil
	}
	var sidecars []api.Sidecar
	if err := json.Unmarshal(deployment.Sidecars, &sidecars); err != nil {
		return nil, fmt.Errorf("decode deployment sidecars: %w", err)
	}
	for _, sidecar := range sidecars {
		if sidecar.Name != workloadName {
			continue
		}
		if sidecar.Type != api.SidecarTypeSidecar {
			return nil, fmt.Errorf("workload %q is not a long-running sidecar", workloadName)
		}
		if len(sidecar.EnvSecrets) == 0 {
			return nil, fmt.Errorf("workload %q has no explicit secret grants", workloadName)
		}
		allowed := make(map[string]struct{}, len(sidecar.EnvSecrets))
		for envKey, ref := range sidecar.EnvSecrets {
			if api.ValidateEnvKey(envKey) != nil || ref != api.SecretRefPrefix+envKey {
				return nil, fmt.Errorf("workload %q has an invalid secret grant", workloadName)
			}
			allowed[envKey] = struct{}{}
		}
		return allowed, nil
	}
	return nil, fmt.Errorf("workload %q is not declared by the deployment", workloadName)
}

func runtimeDeploymentHasSidecars(raw json.RawMessage) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false, nil
	}
	var sidecars []json.RawMessage
	if err := json.Unmarshal(raw, &sidecars); err != nil {
		return false, err
	}
	return len(sidecars) > 0, nil
}

// A nil allowlist preserves the existing deployment contract: legacy deploys
// receive all app secrets in their selected scope. A non-empty allowlist is
// enforced as a positive grant, exactly as the boot delivery path does.
func runtimeSecretAllowlist(raw json.RawMessage) (map[string]struct{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var refs map[string]string
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(refs))
	for envKey, ref := range refs {
		if api.ValidateEnvKey(envKey) != nil || !strings.HasPrefix(ref, api.SecretRefPrefix) {
			return nil, errors.New("invalid env_secrets entry")
		}
		name := strings.TrimPrefix(ref, api.SecretRefPrefix)
		if !api.SecretRefNameRe.MatchString(name) {
			return nil, errors.New("invalid env_secrets reference")
		}
		allowed[envKey] = struct{}{}
	}
	return allowed, nil
}

func runtimeSecretRevision(scope string, rows []state.AppSecret) string {
	rows = append([]state.AppSecret(nil), rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	h := sha256.New()
	_, _ = io.WriteString(h, scope+"\x00")
	for _, row := range rows {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00", row.Key, row.DeliveryVersion, len(row.Ciphertext))
		// DeliveryVersion is scoped to the lifetime of an app_secrets row.
		// Including the sealed envelope also fences delete-and-recreate, where
		// the new row can legitimately start at the same delivery version.
		_, _ = h.Write(row.Ciphertext)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
