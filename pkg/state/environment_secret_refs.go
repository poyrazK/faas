package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// References are environment intent. The resolver keeps ciphertext and source
// delivery versions in the existing secret store; changing a name never copies
// or deletes a secret value.
type AppEnvironmentSecretReferenceReader interface {
	AppEnvironmentSecretReferences(context.Context, string, string, string) (map[string]string, error)
}

type AppEnvironmentSecretReferenceStore interface {
	AppEnvironmentSecretReferenceReader
	PutAppEnvironmentSecretReference(context.Context, string, string, string, string, string) error
	DeleteAppEnvironmentSecretReference(context.Context, string, string, string, string) error
}

var (
	_ AppEnvironmentSecretReferenceStore = (*MemStore)(nil)
	_ AppEnvironmentSecretReferenceStore = (*PgStore)(nil)
)

type environmentSecretRefKey struct{ AppID, EnvironmentID, Key string }
type environmentSecretRef struct {
	Ref       string
	UpdatedAt time.Time
}

func (m *MemStore) deleteEnvironmentSecretRefsLocked(appID, environmentID string) {
	for key := range m.appEnvironmentSecretRefs {
		if appID != "" && key.AppID == appID || environmentID != "" && key.EnvironmentID == environmentID {
			delete(m.appEnvironmentSecretRefs, key)
		}
	}
}

func ValidSecretReference(ref string) bool {
	return strings.HasPrefix(ref, api.SecretRefPrefix) && api.ValidateEnvKey(strings.TrimPrefix(ref, api.SecretRefPrefix)) == nil
}

func (m *MemStore) environmentSecretRefsLocked(appID, scope string) map[string]string {
	out := map[string]string{}
	app := m.apps[appID]
	env, err := m.projectEnvironmentBySlugLocked(app.ProjectID, scope)
	if err != nil {
		return out
	}
	for key, row := range m.appEnvironmentSecretRefs {
		if key.AppID == appID && key.EnvironmentID == env.ID {
			out[key.Key] = row.Ref
		}
	}
	return out
}

func (m *MemStore) AppEnvironmentSecretReferences(_ context.Context, accountID, appID, scope string) (map[string]string, error) {
	if api.ValidateScope(scope) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return nil, ErrNotFound
	}
	return m.environmentSecretRefsLocked(appID, scope), nil
}

func (m *MemStore) PutAppEnvironmentSecretReference(_ context.Context, accountID, appID, scope, key, ref string) error {
	if api.ValidateScope(scope) != nil || api.ValidateEnvKey(key) != nil || !ValidSecretReference(ref) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(app.ProjectID, scope)
	if err != nil {
		return err
	}
	source, err := m.gitOpsGuardScopedWriteLocked(accountID, appID, scope, []string{"secret_refs/" + key})
	if err != nil {
		return err
	}
	if _, exists := m.envs[envKey{AppID: appID, Scope: scope, Key: key}]; exists {
		return ErrConflict
	}
	limits, ok := api.LimitsFor(m.accounts[accountID].Plan)
	if !ok {
		return ErrInvalidArgument
	}
	count := 0
	for k := range m.envs {
		if k.AppID == appID {
			count++
		}
	}
	for k := range m.appEnvironmentSecretRefs {
		if k.AppID == appID {
			count++
		}
	}
	_, exists := m.appEnvironmentSecretRefs[environmentSecretRefKey{appID, env.ID, key}]
	if !exists {
		count++
	}
	if !exists && count > limits.EnvVarsMax {
		return ErrQuotaExceeded
	}
	if m.appEnvironmentSecretRefs == nil {
		m.appEnvironmentSecretRefs = map[environmentSecretRefKey]environmentSecretRef{}
	}
	m.appEnvironmentSecretRefs[environmentSecretRefKey{appID, env.ID, key}] = environmentSecretRef{ref, time.Now().UTC()}
	m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope, time.Now().UTC())
	touchGitOpsMemoryIntent(source)
	return nil
}

func (m *MemStore) DeleteAppEnvironmentSecretReference(_ context.Context, accountID, appID, scope, key string) error {
	if api.ValidateScope(scope) != nil || api.ValidateEnvKey(key) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(app.ProjectID, scope)
	if err != nil {
		return err
	}
	source, err := m.gitOpsGuardScopedWriteLocked(accountID, appID, scope, []string{"secret_refs/" + key})
	if err != nil {
		return err
	}
	refKey := environmentSecretRefKey{appID, env.ID, key}
	if _, exists := m.appEnvironmentSecretRefs[refKey]; !exists {
		return nil
	}
	delete(m.appEnvironmentSecretRefs, refKey)
	m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope, time.Now().UTC())
	touchGitOpsMemoryIntent(source)
	return nil
}

func (s *PgStore) AppEnvironmentSecretReferences(ctx context.Context, accountID, appID, scope string) (map[string]string, error) {
	if api.ValidateScope(scope) != nil {
		return nil, ErrInvalidArgument
	}
	raw, err := sqlc.New().GetAppEnvironmentSecretReferences(ctx, s.pool, sqlc.GetAppEnvironmentSecretReferencesParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return nil, mapErr(err)
	}
	refs := map[string]string{}
	if json.Unmarshal(raw, &refs) != nil {
		return nil, ErrInvalidArgument
	}
	return refs, nil
}

func (s *PgStore) PutAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key, ref string) error {
	return s.writeAppEnvironmentSecretReference(ctx, accountID, appID, scope, key, ref, false)
}
func (s *PgStore) DeleteAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key string) error {
	return s.writeAppEnvironmentSecretReference(ctx, accountID, appID, scope, key, "", true)
}
func (s *PgStore) writeAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key, ref string, remove bool) error {
	if api.ValidateScope(scope) != nil || api.ValidateEnvKey(key) != nil || !remove && !ValidSecretReference(ref) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitSourceForSecretMutation(ctx, tx, sqlc.LockEnvironmentGitSourceForSecretMutationParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope}); err != nil {
		return mapErr(err)
	}
	env, err := q.LockAppEnvironmentSecretReferenceScope(ctx, tx, sqlc.LockAppEnvironmentSecretReferenceScopeParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return mapErr(err)
	}
	if !remove {
		account, err := q.QueueConsumerLockAccount(ctx, tx, mustPgUUID(accountID))
		if err != nil {
			return mapErr(err)
		}
		limits, ok := api.LimitsFor(api.Plan(account.Plan))
		if !ok {
			return ErrInvalidArgument
		}
		count, err := q.EnvironmentSecretReferenceQuota(ctx, tx, sqlc.EnvironmentSecretReferenceQuotaParams{AppID: mustPgUUID(appID), EnvironmentID: env.ID, Scope: scope, Key: key})
		if err != nil {
			return mapErr(err)
		}
		if count.VariableExists {
			return ErrConflict
		}
		if count.Total+1 > int64(limits.EnvVarsMax) && !count.RefExists {
			return ErrQuotaExceeded
		}
	}
	if remove {
		err = q.DeleteEnvironmentGitOpsSecretReference(ctx, tx, sqlc.DeleteEnvironmentGitOpsSecretReferenceParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EnvironmentID: env.ID, Key: key})
	} else {
		err = q.PutEnvironmentGitOpsSecretReference(ctx, tx, sqlc.PutEnvironmentGitOpsSecretReferenceParams{AccountID: mustPgUUID(accountID), ProjectID: env.ProjectID, AppID: mustPgUUID(appID), EnvironmentID: env.ID, Scope: scope, Key: key, SecretName: strings.TrimPrefix(ref, api.SecretRefPrefix)})
	}
	if err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
