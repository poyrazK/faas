package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrEnvBatchRevisionConflict = errors.New("configuration changed since review")
	ErrEnvBatchRequestConflict  = errors.New("request id was used for different configuration changes")
)

type EnvBatchQuotaError struct{ Observed int }

func (e *EnvBatchQuotaError) Error() string { return "configuration batch exceeds the variable quota" }

type EnvBatchChange struct {
	AccountID string
	AppID     string
	Scope     string
	Actor     string
	Guard     bool
	Limits    api.Limits
	Request   api.EnvBatchRequest
}

// EnvBatchStore commits values, one revision, runtime invalidation and the
// replay receipt together. It is optional so older/fake stores fail closed.
type EnvBatchStore interface {
	ApplyEnvBatch(context.Context, EnvBatchChange) (api.EnvBatchResponse, error)
}

type envBatchRecord struct {
	Hash      [32]byte
	Receipt   api.EnvBatchResponse
	CreatedAt time.Time
}

func validateEnvBatch(in *EnvBatchChange) ([32]byte, error) {
	if in.AccountID == "" || in.AppID == "" || api.ValidateScope(in.Scope) != nil || len(in.Actor) > 256 || in.Limits.EnvVarsMax < 1 {
		return [32]byte{}, ErrInvalidArgument
	}
	if in.Request.Validate(in.Limits) != nil {
		return [32]byte{}, ErrInvalidArgument
	}
	data, err := json.Marshal(in.Request)
	return sha256.Sum256(data), err
}

func envBatchValues(current map[string]string, request api.EnvBatchRequest) map[string]string {
	out := maps.Clone(current)
	for key, value := range request.Values {
		out[key] = value
	}
	for _, key := range request.Delete {
		delete(out, key)
	}
	return out
}

func envBatchRevisionMatches(latest ConfigRevision, exists bool, variables map[string]string, secrets map[string]ConfigSecretFingerprint, expected int64) bool {
	if !exists {
		// A first legacy write may have committed values while its separate
		// history recorder is still running. Revision zero is safe only for
		// an empty scope; otherwise that write could be silently overwritten.
		return expected == 0 && len(variables) == 0 && len(secrets) == 0
	}
	return latest.Revision == expected && configStateEqual(latest, variables, secrets)
}

func envBatchReceipt(in EnvBatchChange, revision int64, guard *api.ConfigGuardSummary) api.EnvBatchResponse {
	return api.EnvBatchResponse{RequestID: in.Request.RequestID, AppID: in.AppID, Scope: in.Scope,
		UpdatedKeys: append([]string{}, slices.Sorted(maps.Keys(in.Request.Values))...), DeletedKeys: append([]string{}, in.Request.Delete...),
		ConfigChange: api.ConfigChangeReceipt{Revision: revision, Guard: guard}}
}

func (m *MemStore) ApplyEnvBatch(ctx context.Context, in EnvBatchChange) (api.EnvBatchResponse, error) {
	hash, err := validateEnvBatch(&in)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.EnvBatchResponse{}, err
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.AccountID != in.AccountID || app.Status == AppDeleted {
		return api.EnvBatchResponse{}, ErrNotFound
	}
	key := in.AppID + "/" + in.Scope + "/" + in.Request.RequestID
	for k, receipt := range m.envBatchReceipts {
		if time.Since(receipt.CreatedAt) > 24*time.Hour {
			delete(m.envBatchReceipts, k)
		}
	}
	if previous, ok := m.envBatchReceipts[key]; ok {
		if previous.Hash != hash {
			return api.EnvBatchResponse{}, ErrEnvBatchRequestConflict
		}
		return cloneEnvBatchReceipt(previous.Receipt), nil
	}
	variables, secrets := m.configStateLocked(in.AccountID, in.AppID, in.Scope)
	latest, exists := m.latestConfigRevisionLocked(in.AppID, in.Scope)
	if !exists && *in.Request.ExpectedRevision == 0 && (len(variables) != 0 || len(secrets) != 0) {
		// Older manifest/internal writers did not always create history.
		// Record their current state, then require a fresh review of that
		// revision. This attempt changes no values and consumes no receipt.
		m.appendConfigRevisionLocked(ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: 1,
			Source: ConfigSourceBaseline, Actor: in.Actor, Variables: variables, Secrets: secrets, CreatedAt: time.Now().UTC()})
		return api.EnvBatchResponse{}, ErrEnvBatchRevisionConflict
	}
	if !envBatchRevisionMatches(latest, exists, variables, secrets, *in.Request.ExpectedRevision) {
		return api.EnvBatchResponse{}, ErrEnvBatchRevisionConflict
	}
	next := envBatchValues(variables, in.Request)
	count := len(next)
	for _, row := range m.envs {
		if row.AccountID == in.AccountID && row.AppID == in.AppID && row.Scope != in.Scope {
			count++
		}
	}
	for key := range m.appEnvironmentSecretRefs {
		if key.AppID == in.AppID {
			count++
		}
	}
	for key, intent := range m.appEnvironmentWorkloadIntents {
		if key.AppID == in.AppID && intent.AccountID == in.AccountID {
			count += len(intent.ServiceBindings)
		}
	}
	if count > in.Limits.EnvVarsMax {
		return api.EnvBatchResponse{}, &EnvBatchQuotaError{count}
	}
	paths := []string{}
	for key := range in.Request.Values {
		paths = append(paths, "variables/"+key, "secret_refs/"+key)
	}
	for _, key := range in.Request.Delete {
		paths = append(paths, "variables/"+key)
	}
	memory, err := m.gitOpsGuardScopedWriteLocked(in.AccountID, in.AppID, in.Scope, paths)
	if err != nil {
		return api.EnvBatchResponse{}, err
	}
	now := time.Now().UTC()
	if !exists {
		latest = m.appendConfigRevisionLocked(ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: 1, Source: ConfigSourceBaseline, Actor: in.Actor, Variables: variables, Secrets: secrets, CreatedAt: now})
	}
	guard, watching := m.watchingConfigGuardLocked(in.AppID, in.Scope)
	if !in.Guard && watching {
		m.closeConfigGuardLocked(guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededByUnguarded), now)
		watching = false
	}
	if in.Guard && !watching {
		if m.configGuards == nil {
			m.configGuards = map[string]ConfigGuard{}
		}
		if m.configGuardSecrets == nil {
			m.configGuardSecrets = map[string]map[string]ConfigGuardSecret{}
		}
		guard = ConfigGuard{ID: uuid.NewString(), AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, BaseRevision: latest.Revision, HeadRevision: latest.Revision, Status: ConfigGuardWatching, Restorable: true, OpenedAt: now, LastChangeAt: now}
	}
	for key, value := range in.Request.Values {
		k := envKey{AppID: in.AppID, Scope: in.Scope, Key: key}
		row := m.envs[k]
		if row.CreatedAt.IsZero() {
			row = AppEnv{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Key: key, CreatedAt: now}
		}
		row.Value, row.UpdatedAt = value, now
		m.envs[k] = row
	}
	for _, key := range in.Request.Delete {
		delete(m.envs, envKey{AppID: in.AppID, Scope: in.Scope, Key: key})
	}
	touchGitOpsMemoryIntent(memory)
	revision := ConfigRevision{AccountID: in.AccountID, AppID: in.AppID, Scope: in.Scope, Revision: latest.Revision + 1, Source: ConfigSourceEnvBatch, Actor: in.Actor, Variables: next, Secrets: secrets, CreatedAt: now}
	var summary *api.ConfigGuardSummary
	if in.Guard {
		revision.GuardID = guard.ID
		guard.HeadRevision, guard.LastChangeAt, guard.WindowEndsAt = revision.Revision, now, nil
		m.configGuards[guard.ID] = guard
		summary = &api.ConfigGuardSummary{ID: guard.ID, Status: guard.Status, BaseRevision: guard.BaseRevision, WindowMinutes: api.ConfigGuardWindowMinutes}
	}
	m.appendConfigRevisionLocked(revision)
	m.pruneConfigRevisionsLocked(in.AppID, in.Scope, revision.Revision-api.ConfigRevisionsRetained)
	m.invalidateEnvBatchSnapshotsLocked(in.AppID, in.Scope, now)
	receipt := envBatchReceipt(in, revision.Revision, summary)
	if m.envBatchReceipts == nil {
		m.envBatchReceipts = map[string]envBatchRecord{}
	}
	m.envBatchReceipts[key] = envBatchRecord{Hash: hash, Receipt: cloneEnvBatchReceipt(receipt), CreatedAt: now}
	return receipt, nil
}

func cloneEnvBatchReceipt(in api.EnvBatchResponse) api.EnvBatchResponse {
	in.UpdatedKeys = append([]string{}, in.UpdatedKeys...)
	in.DeletedKeys = append([]string{}, in.DeletedKeys...)
	if in.ConfigChange.Guard != nil {
		g := *in.ConfigChange.Guard
		in.ConfigChange.Guard = &g
	}
	return in
}

func (m *MemStore) invalidateEnvBatchSnapshotsLocked(appID, scope string, now time.Time) {
	if scope != DefaultEnvScope {
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope, now)
		return
	}
	if m.runtimeConfigChangedAt == nil {
		m.runtimeConfigChangedAt = map[string]time.Time{}
	}
	m.runtimeConfigChangedAt[appID] = now
	for id, snapshot := range m.snapshots {
		if m.deployments[snapshot.DeploymentID].AppID == appID && !snapshot.Stale {
			m.markSnapshotStaleLocked(id)
		}
	}
}
