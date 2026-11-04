package state

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrConfigRestoreChanged = errors.New("configuration changed before restoration")

type ConfigVariableRestore struct {
	Key, Value string
	Delete     bool
}

// ConfigRestore commits the complete change, its revision and (for automatic
// recovery) the guard's final decision together. Expected state prevents a
// concurrent editor or another worker from being overwritten.
type ConfigRestore struct {
	Finish            ConfigChangeFinish
	ExpectedVariables map[string]string
	ExpectedSecrets   map[string]ConfigSecretFingerprint
	Variables         []ConfigVariableRestore
	Secrets           []ConfigGuardSecret
	Guard             *ConfigGuard
	Decision          api.ConfigGuardDecision
}

type ConfigRestoreStore interface {
	ApplyConfigRestore(context.Context, ConfigRestore) (ConfigRevision, error)
}

func (r ConfigRestore) valid() bool {
	if !validConfigChangeFinish(r.Finish) || !configSourceRestores(r.Finish.Source) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range r.Variables {
		if api.ValidateEnvKey(v.Key) != nil || seen[v.Key] {
			return false
		}
		seen[v.Key] = true
	}
	seen = map[string]bool{}
	for _, s := range r.Secrets {
		if api.ValidateEnvKey(s.Key) != nil || seen[s.Key] || s.Existed && (len(s.Ciphertext) == 0 || !validSecretClass(defaultSecretClass(s.SecretClass))) {
			return false
		}
		seen[s.Key] = true
	}
	return true
}

func (r ConfigRestore) unchanged(variables map[string]string, secrets map[string]ConfigSecretFingerprint, guard ConfigGuard) bool {
	if !maps.Equal(r.ExpectedVariables, variables) || !maps.Equal(r.ExpectedSecrets, secrets) {
		return false
	}
	return r.Guard == nil || guard.ID == r.Guard.ID && guard.AccountID == r.Finish.AccountID && guard.AppID == r.Finish.AppID && guard.Scope == r.Finish.Scope && guard.Status == ConfigGuardWatching && guard.Restorable && guard.HeadRevision == r.Guard.HeadRevision
}

func (r ConfigRestore) decision(revision int64) json.RawMessage {
	d := r.Decision
	d.RestoredRevision = revision
	b, _ := json.Marshal(d)
	return b
}

func (m *MemStore) ApplyConfigRestore(ctx context.Context, r ConfigRestore) (revision ConfigRevision, err error) {
	if !r.valid() {
		return revision, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.checkOrgAccessLocked(ctx); err != nil {
		return revision, err
	}
	f := r.Finish
	app, ok := m.apps[f.AppID]
	if !ok || app.AccountID != f.AccountID || app.Status == AppDeleted {
		return revision, ErrNotFound
	}
	variables, secrets := m.configStateLocked(f.AccountID, f.AppID, f.Scope)
	var guard ConfigGuard
	if r.Guard != nil {
		guard = m.configGuards[r.Guard.ID]
	}
	if !r.unchanged(variables, secrets, guard) {
		return revision, ErrConfigRestoreChanged
	}
	undo := m.stageConfigRestoreLocked()
	defer func() {
		if err != nil {
			undo()
		}
	}()
	for _, v := range r.Variables {
		if v.Delete {
			err = m.deleteAppEnvInScopeLocked(ctx, f.AccountID, f.AppID, f.Scope, v.Key)
		} else {
			err = m.upsertAppEnvInScopeLocked(ctx, f.AccountID, f.AppID, f.Scope, v.Key, v.Value)
		}
		if err != nil {
			return revision, err
		}
	}
	for _, s := range r.Secrets {
		if s.Existed {
			err = m.upsertAppSecretWithClassInScopeLocked(ctx, f.AccountID, f.AppID, f.Scope, s.Key, s.Kid, s.ValueHash, s.SecretClass, s.Ciphertext)
		} else {
			_, err = m.deleteAppSecretInScopeWithRevocationLocked(ctx, f.AccountID, f.AppID, f.Scope, s.Key)
		}
		if err != nil {
			return revision, err
		}
	}
	revision, err = m.finishConfigChangeLocked(f)
	if err != nil {
		return revision, err
	}
	if r.Guard != nil {
		m.closeConfigGuardLocked(r.Guard.ID, ConfigGuardRestored, r.decision(revision.Revision), f.Now)
	}
	if f.Scope == DefaultEnvScope || len(r.Secrets) > 0 {
		m.markAppRuntimeConfigChangedAndSnapshotsStaleLocked(f.AppID, f.Now)
	} else {
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(f.AppID, f.Scope, f.Now)
	}
	return revision, nil
}

// Existing secret mutators also maintain delivery, revocation and OAuth
// journals. Stage those maps so an error in any later key restores all of
// their changes while readers remain excluded by the store mutex.
func (m *MemStore) stageConfigRestoreLocked() func() {
	envs, secrets := m.envs, m.secrets
	observations, revocations := m.secretRuntimeReloadObservations, m.secretRevocations
	monitors, retirements := m.mcpCatalogMonitors, m.mcpOAuthRetirements
	m.envs, m.secrets = maps.Clone(envs), maps.Clone(secrets)
	m.secretRuntimeReloadObservations, m.secretRevocations = maps.Clone(observations), maps.Clone(revocations)
	m.mcpCatalogMonitors, m.mcpOAuthRetirements = maps.Clone(monitors), maps.Clone(retirements)
	type intent struct {
		source EnvironmentGitSource
		next   time.Time
	}
	intents := map[*environmentGitOpsMemory]intent{}
	for _, memory := range m.environmentGitOps {
		intents[memory] = intent{memory.source, memory.next}
	}
	return func() {
		m.envs, m.secrets = envs, secrets
		m.secretRuntimeReloadObservations, m.secretRevocations = observations, revocations
		m.mcpCatalogMonitors, m.mcpOAuthRetirements = monitors, retirements
		for memory, before := range intents {
			memory.source, memory.next = before.source, before.next
		}
	}
}
