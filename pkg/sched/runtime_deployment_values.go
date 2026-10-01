package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeDeploymentValues struct {
	Snapshot    state.RuntimeAppValuesSnapshot
	APIEnv      []fcvm.APIEnvEntry
	MainSecrets sealedEnvDelivery
}

func runtimeValuesHaveEphemeralSecrets(snapshot state.RuntimeAppValuesSnapshot) bool {
	for _, row := range snapshot.Secrets {
		if row.SecretClass == state.SecretClassEphemeral {
			return true
		}
	}
	return false
}

func (e *Engine) loadRuntimeDeploymentValues(ctx context.Context, accountID string, dep state.Deployment) (runtimeDeploymentValues, error) {
	snapshot, err := e.store.RuntimeAppValuesForDeployment(ctx, accountID, dep.AppID, dep.ID)
	if err != nil {
		return runtimeDeploymentValues{}, fmt.Errorf("read owned deployment values: %w", err)
	}
	if snapshot.AccountID != accountID || snapshot.AppID != dep.AppID || snapshot.DeploymentID != dep.ID ||
		snapshot.Scope != normalizedDeploymentScope(dep.Scope) || api.ValidateScope(snapshot.Scope) != nil ||
		!sameRuntimeJSON(snapshot.SecretGrants.OverrideEnvSecrets, dep.OverrideEnvSecrets) ||
		!sameRuntimeJSON(snapshot.SecretGrants.Sidecars, dep.Sidecars) || snapshot.SecretGrants.ReloadSignal != dep.SecretReloadSignal {
		return runtimeDeploymentValues{}, fmt.Errorf("deployment runtime value owner or grants changed: %w", state.ErrConflict)
	}
	var refs map[string]string
	if len(snapshot.SecretGrants.OverrideEnvSecrets) > 0 {
		if err := json.Unmarshal(snapshot.SecretGrants.OverrideEnvSecrets, &refs); err != nil {
			return runtimeDeploymentValues{}, fmt.Errorf("decode runtime secret grants: %w", err)
		}
	}
	for key, ref := range refs {
		if api.ValidateEnvKey(key) != nil || !strings.HasPrefix(ref, api.SecretRefPrefix) || !api.SecretRefNameRe.MatchString(strings.TrimPrefix(ref, api.SecretRefPrefix)) {
			return runtimeDeploymentValues{}, fmt.Errorf("invalid runtime secret grant: %w", state.ErrConflict)
		}
	}
	result := runtimeDeploymentValues{Snapshot: snapshot}
	keys := map[string]bool{}
	for _, row := range snapshot.Values {
		if row.AccountID != accountID || row.AppID != dep.AppID || row.Scope != snapshot.Scope || api.ValidateEnvKey(row.Key) != nil || keys[row.Key] {
			return runtimeDeploymentValues{}, fmt.Errorf("invalid runtime environment projection: %w", state.ErrConflict)
		}
		keys[row.Key] = true
		result.APIEnv = append(result.APIEnv, fcvm.APIEnvEntry{Key: row.Key, Value: row.Value})
	}
	result.MainSecrets, err = sealedEnvDeliveryFromRows(snapshot.Secrets, accountID, dep.AppID, snapshot.Scope, refs)
	if err != nil {
		return runtimeDeploymentValues{}, err
	}
	result.MainSecrets.Fence, err = state.NewRuntimeAppSecretFence(snapshot)
	if err != nil {
		return runtimeDeploymentValues{}, fmt.Errorf("capture runtime secret delivery owner: %w", err)
	}
	return result, nil
}

func sameRuntimeJSON(a, b []byte) bool {
	normalize := func(raw []byte) ([]byte, error) {
		if len(raw) == 0 {
			raw = []byte("null")
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	left, le := normalize(a)
	right, re := normalize(b)
	return le == nil && re == nil && bytes.Equal(left, right)
}

// sameRuntimeValuesSnapshot compares the owned configuration selected for a
// paused restore, excluding mutable observations. Plaintext is compared only
// in memory and is never included in an error or log.
func sameRuntimeValuesSnapshot(a, b state.RuntimeAppValuesSnapshot) bool {
	left, leftErr := state.NewRuntimeAppSecretFence(a)
	right, rightErr := state.NewRuntimeAppSecretFence(b)
	if leftErr != nil || rightErr != nil || left != right {
		return false
	}
	leftValues, leftErr := json.Marshal(a.Values)
	rightValues, rightErr := json.Marshal(b.Values)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftValues, rightValues)
}
