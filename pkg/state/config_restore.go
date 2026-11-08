package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrConfigRestoreChanged = errors.New("configuration changed before restoration")
var ErrConfigRollbackRecorded = errors.New("reviewed configuration rollback was already recorded")

type ConfigVariableRestore struct {
	Key, Value string
	Delete     bool
}

// ConfigRestore commits the complete change, its revision and (for automatic
// recovery) the guard's final decision together. Expected state prevents a
// concurrent editor or another worker from being overwritten.
type ConfigRestore struct {
	Finish                ConfigChangeFinish
	ExpectedVariables     map[string]string
	ExpectedSecrets       map[string]ConfigSecretFingerprint
	Variables             []ConfigVariableRestore
	Secrets               []ConfigGuardSecret
	Guard                 *ConfigGuard
	Decision              api.ConfigGuardDecision
	ExpectedRevision      *int64
	ExpectedAppID         string
	ExpectedEnvironmentID string
	ReviewedGuard         bool
	RequestID             string
	ReceiptProof          string
}

type ConfigRollbackReceiptReader interface {
	ConfigRollbackReceipt(context.Context, string, string, string, string, string) (api.ConfigRollbackReceipt, error)
}

func (r ConfigRestore) rollbackRequestHash() [32]byte {
	data, _ := json.Marshal(struct {
		RequestID, AppID, Scope, EnvironmentID, Proof string
		Target, Expected                              int64
	}{r.RequestID, r.ExpectedAppID, r.Finish.Scope, r.ExpectedEnvironmentID, r.ReceiptProof, r.Finish.RestoredRevision, *r.ExpectedRevision})
	return sha256.Sum256(data)
}

func (r ConfigRestore) rollbackReceipt(revision int64, changed bool) api.ConfigRollbackReceipt {
	return api.ConfigRollbackReceipt{RequestID: r.RequestID, AppID: r.Finish.AppID, Scope: r.Finish.Scope, EnvironmentID: r.ExpectedEnvironmentID, RestoredRevision: r.Finish.RestoredRevision, ConfigRevision: revision, Changed: changed}
}

type ConfigRestoreStore interface {
	ApplyConfigRestore(context.Context, ConfigRestore) (ConfigRevision, error)
}

// ReviewedConfigRollbackStore explicitly opts in to the transactional reviewed
// revision, identity, guard and receipt contract.
type ReviewedConfigRollbackStore interface {
	ApplyReviewedConfigRollback(context.Context, ConfigRestore) (ConfigRevision, error)
}

func (m *MemStore) ApplyReviewedConfigRollback(ctx context.Context, r ConfigRestore) (ConfigRevision, error) {
	if r.ExpectedRevision == nil || r.Finish.Source != ConfigSourceRollback {
		return ConfigRevision{}, ErrInvalidArgument
	}
	return m.ApplyConfigRestore(ctx, r)
}

func (r ConfigRestore) valid() bool {
	if !validConfigChangeFinish(r.Finish) || !configSourceRestores(r.Finish.Source) {
		return false
	}
	if r.ExpectedRevision != nil {
		if *r.ExpectedRevision < 0 || r.Finish.Source != ConfigSourceRollback || !sameStandardUUID(r.ExpectedAppID, r.Finish.AppID) || (r.Finish.Scope == DefaultEnvScope) != (r.ExpectedEnvironmentID == "") {
			return false
		}
	} else if r.ExpectedAppID != "" || r.ExpectedEnvironmentID != "" {
		return false
	}
	if r.RequestID != "" || r.ReceiptProof != "" {
		id, err := uuid.Parse(r.RequestID)
		if r.ExpectedRevision == nil || err != nil || id == uuid.Nil || id.String() != r.RequestID || !api.ValidEnvBatchReceiptProof(r.ReceiptProof) {
			return false
		}
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
	if r.ExpectedRevision != nil && r.ExpectedEnvironmentID != "" {
		env, err := m.workloadSpecEnvironmentLocked(f.AccountID, app.ProjectID, f.Scope, app.ID)
		if err != nil {
			return revision, err
		}
		if !sameStandardUUID(env.ID, r.ExpectedEnvironmentID) || env.Protected {
			return revision, ErrConfigRestoreChanged
		}
	}
	if r.RequestID != "" {
		key := f.AppID + "/" + f.Scope + "/" + r.RequestID
		if previous, ok := m.envBatchReceipts[key]; ok {
			if previous.Rollback == nil || previous.Hash != r.rollbackRequestHash() {
				return revision, ErrEnvBatchRequestConflict
			}
			return revision, ErrConfigRollbackRecorded
		}
	}
	variables, secrets := m.configStateLocked(f.AccountID, f.AppID, f.Scope)
	if r.ExpectedRevision != nil {
		latest, exists := m.latestConfigRevisionLocked(f.AppID, f.Scope)
		if !envBatchRevisionMatches(latest, exists, variables, secrets, *r.ExpectedRevision) {
			return revision, ErrConfigRestoreChanged
		}
	}
	var guard ConfigGuard
	if r.Guard != nil {
		guard = m.configGuards[r.Guard.ID]
	}
	if !r.unchanged(variables, secrets, guard) {
		return revision, ErrConfigRestoreChanged
	}
	if r.ExpectedRevision != nil && len(r.Variables) == 0 && len(r.Secrets) == 0 {
		latest, _ := m.latestConfigRevisionLocked(f.AppID, f.Scope)
		m.recordReviewedConfigRollback(r, latest.Revision, false)
		return latest, nil
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
	var restoreGuard *api.ConfigGuardSummary
	if r.ExpectedRevision != nil {
		latest, _ := m.latestConfigRevisionLocked(f.AppID, f.Scope)
		guard, watching := m.watchingConfigGuardLocked(f.AppID, f.Scope)
		if !r.ReviewedGuard && watching {
			m.closeConfigGuardLocked(guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededByUnguarded), f.Now)
		} else if r.ReviewedGuard {
			if !watching {
				if m.configGuards == nil {
					m.configGuards = map[string]ConfigGuard{}
				}
				guard = ConfigGuard{ID: uuid.NewString(), AccountID: f.AccountID, AppID: f.AppID, Scope: f.Scope, BaseRevision: latest.Revision, HeadRevision: latest.Revision, Status: ConfigGuardWatching, Restorable: true, OpenedAt: f.Now, LastChangeAt: f.Now}
				m.configGuards[guard.ID] = guard
			}
			f.GuardID = guard.ID
			restoreGuard = &api.ConfigGuardSummary{ID: guard.ID, Status: guard.Status, BaseRevision: guard.BaseRevision, WindowMinutes: api.ConfigGuardWindowMinutes}
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
	revision.RestoreGuard = restoreGuard
	m.recordReviewedConfigRollback(r, revision.Revision, true)
	return revision, nil
}

func (m *MemStore) recordReviewedConfigRollback(r ConfigRestore, revision int64, changed bool) {
	if r.RequestID == "" {
		return
	}
	if m.envBatchReceipts == nil {
		m.envBatchReceipts = map[string]envBatchRecord{}
	}
	for key, record := range m.envBatchReceipts {
		if time.Since(record.CreatedAt) > 24*time.Hour {
			delete(m.envBatchReceipts, key)
		}
	}
	receipt := r.rollbackReceipt(revision, changed)
	m.envBatchReceipts[r.Finish.AppID+"/"+r.Finish.Scope+"/"+r.RequestID] = envBatchRecord{Hash: r.rollbackRequestHash(), ProofHash: envBatchProofHash(r.ReceiptProof), CreatedAt: time.Now().UTC(), Rollback: &receipt}
}

func (m *MemStore) ConfigRollbackReceipt(ctx context.Context, accountID, appID, scope, requestID, proof string) (api.ConfigRollbackReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.ConfigRollbackReceipt{}, err
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.ConfigRollbackReceipt{}, ErrNotFound
	}
	environmentID := ""
	if scope != DefaultEnvScope {
		env, err := m.workloadSpecEnvironmentLocked(accountID, app.ProjectID, scope, appID)
		if err != nil {
			return api.ConfigRollbackReceipt{}, err
		}
		environmentID = env.ID
	}
	record, ok := m.envBatchReceipts[appID+"/"+scope+"/"+requestID]
	if !ok || record.Rollback == nil || !envBatchProofMatches(record.ProofHash, proof) || !envBatchEnvironmentMatches(record.Rollback.EnvironmentID, environmentID) || record.Rollback.AppID != appID || record.Rollback.Scope != scope || record.Rollback.RequestID != requestID {
		return api.ConfigRollbackReceipt{}, ErrNotFound
	}
	return *record.Rollback, nil
}

// Existing secret mutators also maintain delivery, revocation and OAuth
// journals. Stage those maps so an error in any later key restores all of
// their changes while readers remain excluded by the store mutex.
func (m *MemStore) stageConfigRestoreLocked() func() {
	envs, secrets := m.envs, m.secrets
	guards, guardSecrets := m.configGuards, m.configGuardSecrets
	observations, revocations := m.secretRuntimeReloadObservations, m.secretRevocations
	monitors, retirements := m.mcpCatalogMonitors, m.mcpOAuthRetirements
	m.envs, m.secrets = maps.Clone(envs), maps.Clone(secrets)
	m.configGuards, m.configGuardSecrets = maps.Clone(guards), maps.Clone(guardSecrets)
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
		m.configGuards, m.configGuardSecrets = guards, guardSecrets
		m.secretRuntimeReloadObservations, m.secretRevocations = observations, revocations
		m.mcpCatalogMonitors, m.mcpOAuthRetirements = monitors, retirements
		for memory, before := range intents {
			memory.source, memory.next = before.source, before.next
		}
	}
}
