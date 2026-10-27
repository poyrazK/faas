package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeDeploymentValues struct {
	Snapshot    state.RuntimeAppValuesSnapshot
	APIEnv      []fcvm.APIEnvEntry
	MainSecrets sealedEnvDelivery
	ConfigFence state.RuntimeAppConfigFence
	Inputs      state.RuntimeConfigInputs
}

func runtimeValuesHaveEphemeralSecrets(snapshot state.RuntimeAppValuesSnapshot) bool {
	for _, row := range snapshot.Secrets {
		if row.SecretClass == state.SecretClassEphemeral {
			return true
		}
	}
	return false
}

func (e *Engine) loadRuntimeDeploymentValues(ctx context.Context, app state.App, dep state.Deployment) (runtimeDeploymentValues, error) {
	return e.loadRuntimeDeploymentValuesForTask(ctx, app, dep, false)
}

func (e *Engine) loadRuntimeDeploymentValuesForTask(ctx context.Context, app state.App, dep state.Deployment, release bool) (runtimeDeploymentValues, error) {
	return buildRuntimeDeploymentValues(app, dep, e.readRuntimeDeploymentInputs(ctx, app.AccountID, dep), release)
}

// buildRuntimeDeploymentValues checks and assembles the values from their
// three reads. It reads nothing, so the owner cache can keep the reads and
// build fresh values, with their own maps and slices, for every wake.
func buildRuntimeDeploymentValues(app state.App, dep state.Deployment, reads runtimeDeploymentInputs, release bool) (runtimeDeploymentValues, error) {
	accountID := app.AccountID
	if reads.boundaryErr != nil {
		return runtimeDeploymentValues{}, reads.boundaryErr
	}
	boundary := reads.boundary
	if !reads.stamped {
		boundary = time.Unix(0, 0).UTC()
	}
	snapshot, err := reads.snapshot, reads.snapshotErr
	if err != nil {
		return runtimeDeploymentValues{}, fmt.Errorf("read owned deployment values: %w", err)
	}
	if !snapshot.MatchesRuntimeInputs(app, dep) || snapshot.AccountID != accountID || snapshot.AppID != dep.AppID || snapshot.DeploymentID != dep.ID ||
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
	result := runtimeDeploymentValues{Snapshot: snapshot, Inputs: state.RuntimeConfigInputs{Scope: snapshot.Scope, Boundary: boundary, Variables: map[string]string{}, SecretVersions: map[string]int64{}}}
	// ADR-1136: project environment variables sit below the app's own rows,
	// which the state layer already removed from Inherited. They are not
	// receipt inputs: freshness compares receipts with app_envs exactly, and a
	// configuration change stamps the scope instead.
	for _, row := range snapshot.Inherited {
		result.APIEnv = append(result.APIEnv, fcvm.APIEnvEntry{Key: row.Key, Value: row.Value})
	}
	keys := map[string]bool{}
	for _, row := range snapshot.Values {
		if row.AccountID != accountID || row.AppID != dep.AppID || row.Scope != snapshot.Scope || api.ValidateEnvKey(row.Key) != nil || keys[row.Key] {
			return runtimeDeploymentValues{}, fmt.Errorf("invalid runtime environment projection: %w", state.ErrConflict)
		}
		keys[row.Key] = true
		result.Inputs.Variables[row.Key] = row.Value
		if row.UpdatedAt.After(result.Inputs.Boundary) {
			result.Inputs.Boundary = row.UpdatedAt
		}
		result.APIEnv = append(result.APIEnv, fcvm.APIEnvEntry{Key: row.Key, Value: row.Value})
	}
	// The intent was read for normalizedDeploymentScope(dep.Scope), which the
	// check above requires snapshot.Scope to equal.
	intent := reads.intent
	if reads.intentErr != nil {
		return runtimeDeploymentValues{}, reads.intentErr
	}
	result.MainSecrets, err = sealedEnvDeliveryFromRowsWithIntent(snapshot.Secrets, accountID, dep.AppID, snapshot.Scope, refs, release, intent, true)
	if err != nil {
		return runtimeDeploymentValues{}, err
	}
	// ADR-1139: an explicit secret selection is a least-privilege grant and
	// inherits nothing; otherwise project secrets go first so vmmd lets the
	// app's own entries win. They are not delivery candidates or receipt
	// inputs: a change stamps the scope instead.
	if len(refs) == 0 {
		result.MainSecrets.Entries = withInheritedProjectSecrets(snapshot.InheritedSecrets, result.MainSecrets.Entries)
	}
	result.MainSecrets.Fence, err = state.NewRuntimeAppSecretFence(snapshot)
	if err != nil {
		return runtimeDeploymentValues{}, fmt.Errorf("capture runtime secret delivery owner: %w", err)
	}
	result.ConfigFence, err = state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		return runtimeDeploymentValues{}, fmt.Errorf("capture runtime configuration: %w", err)
	}
	return result, nil
}

// withInheritedProjectSecrets prepends the inherited project secrets the app
// does not deliver itself. Each entry names its single source key, so vmmd
// refuses an envelope that holds anything else.
func withInheritedProjectSecrets(inherited []state.ProjectEnvironmentSecret, own []fcvm.SealedEnvEntry) []fcvm.SealedEnvEntry {
	if len(inherited) == 0 {
		return own
	}
	delivered := make(map[string]bool, len(own))
	for _, entry := range own {
		delivered[entry.Key] = true
	}
	entries := make([]fcvm.SealedEnvEntry, 0, len(inherited)+len(own))
	for _, secret := range inherited {
		if delivered[secret.Key] {
			continue
		}
		entries = append(entries, fcvm.SealedEnvEntry{Key: secret.Key, Ciphertext: append([]byte(nil), secret.Ciphertext...), SourceKey: secret.Key})
	}
	return append(entries, own...)
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
	left, leftErr := state.NewRuntimeAppConfigFence(a)
	right, rightErr := state.NewRuntimeAppConfigFence(b)
	return leftErr == nil && rightErr == nil && left == right
}

// runtimeDeploymentInputs are the three independent reads behind
// loadRuntimeDeploymentValuesForTask. They run concurrently, one round trip
// on the wake path instead of three in a row; the caller checks their errors
// in the order the reads used to run, so the result is unchanged.
type runtimeDeploymentInputs struct {
	boundary    time.Time
	stamped     bool
	boundaryErr error
	snapshot    state.RuntimeAppValuesSnapshot
	snapshotErr error
	intent      state.AppEnvironmentSecretIntent
	intentErr   error
}

func (e *Engine) readRuntimeDeploymentInputs(ctx context.Context, accountID string, dep state.Deployment) runtimeDeploymentInputs {
	var in runtimeDeploymentInputs
	concurrently(
		func() {
			in.boundary, in.stamped, in.boundaryErr = state.RuntimeConfigChangedAtForScope(ctx, e.store, dep.AppID, dep.Scope)
		},
		func() {
			in.snapshot, in.snapshotErr = e.store.RuntimeAppValuesForDeployment(ctx, accountID, dep.AppID, dep.ID)
		},
		func() {
			if reader, ok := e.store.(state.AppEnvironmentSecretIntentReader); ok {
				in.intent, in.intentErr = reader.AppEnvironmentSecretIntent(ctx, accountID, dep.AppID, normalizedDeploymentScope(dep.Scope))
			}
		},
	)
	return in
}

// concurrently runs fns at once and returns when all have finished. Each fn
// writes only its own results.
func concurrently(fns ...func()) {
	if len(fns) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, fn := range fns[1:] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	fns[0]()
	wg.Wait()
}
