package state

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
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

// Read positive and negative intent together; independent reads could combine
// the two sides of a replacement across transactions.
type AppEnvironmentSecretIntentReader interface {
	AppEnvironmentSecretIntent(context.Context, string, string, string) (AppEnvironmentSecretIntent, error)
}

type AppEnvironmentSecretIntent struct {
	References     map[string]string `json:"references"`
	SuppressedKeys []string          `json:"suppressed_keys"`
}

func (intent AppEnvironmentSecretIntent) EffectiveReferences(baseline map[string]string) map[string]string {
	refs := maps.Clone(baseline)
	if refs == nil {
		refs = map[string]string{}
	}
	for _, key := range intent.SuppressedKeys {
		delete(refs, key)
	}
	maps.Copy(refs, intent.References)
	return refs
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
	changed := map[string]bool{}
	for key := range m.appEnvironmentSecretRefs {
		if appID != "" && key.AppID == appID || environmentID != "" && key.EnvironmentID == environmentID {
			delete(m.appEnvironmentSecretRefs, key)
			if environmentID != "" {
				changed[key.AppID] = true
			}
		}
	}
	for key := range m.appEnvironmentSecretSuppressions {
		if appID != "" && key.AppID == appID || environmentID != "" && key.EnvironmentID == environmentID {
			delete(m.appEnvironmentSecretSuppressions, key)
			if environmentID != "" {
				changed[key.AppID] = true
			}
		}
	}
	for id := range changed {
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(id, m.projectEnvironments[environmentID].Slug, time.Now().UTC())
	}
}

func ValidSecretReference(ref string) bool {
	return strings.HasPrefix(ref, api.SecretRefPrefix) && api.ValidateEnvKey(strings.TrimPrefix(ref, api.SecretRefPrefix)) == nil
}

// DeploymentSecretReferences keeps the legacy empty stage-all contract but
// fails closed on corrupt persisted intent before any scoped overlay.
func DeploymentSecretReferences(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var refs map[string]string
	if json.Unmarshal(raw, &refs) != nil {
		return nil, ErrInvalidArgument
	}
	for key, ref := range refs {
		if api.ValidateEnvKey(key) != nil || !ValidSecretReference(ref) {
			return nil, ErrInvalidArgument
		}
	}
	return refs, nil
}

func (m *MemStore) environmentSecretSuppressionsLocked(appID, scope string) []string {
	keys := []string{}
	env, err := m.projectEnvironmentBySlugLocked(m.apps[appID].ProjectID, scope)
	if err != nil {
		return keys
	}
	for key := range m.appEnvironmentSecretSuppressions {
		if key.AppID == appID && key.EnvironmentID == env.ID {
			keys = append(keys, key.Key)
		}
	}
	sort.Strings(keys)
	return keys
}

func (m *MemStore) AppEnvironmentSecretIntent(_ context.Context, accountID, appID, scope string) (AppEnvironmentSecretIntent, error) {
	if api.ValidateScope(scope) != nil {
		return AppEnvironmentSecretIntent{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return AppEnvironmentSecretIntent{}, ErrNotFound
	}
	return AppEnvironmentSecretIntent{References: m.environmentSecretRefsLocked(appID, scope), SuppressedKeys: m.environmentSecretSuppressionsLocked(appID, scope)}, nil
}

func (s *PgStore) AppEnvironmentSecretIntent(ctx context.Context, accountID, appID, scope string) (AppEnvironmentSecretIntent, error) {
	if api.ValidateScope(scope) != nil {
		return AppEnvironmentSecretIntent{}, ErrInvalidArgument
	}
	raw, err := sqlc.New().GetAppEnvironmentSecretIntent(ctx, s.pool, sqlc.GetAppEnvironmentSecretIntentParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return AppEnvironmentSecretIntent{}, mapErr(err)
	}
	var intent AppEnvironmentSecretIntent
	if json.Unmarshal(raw, &intent) != nil {
		return intent, ErrInvalidArgument
	}
	return intent, nil
}

func (m *MemStore) suppressEnvironmentSecretReferenceLocked(key environmentSecretRefKey, now time.Time) {
	delete(m.appEnvironmentSecretRefs, key)
	if m.appEnvironmentSecretSuppressions == nil {
		m.appEnvironmentSecretSuppressions = map[environmentSecretRefKey]time.Time{}
	}
	m.appEnvironmentSecretSuppressions[key] = now
}

func (m *MemStore) environmentSecretSuppressionCountLocked(appID string) int {
	count := 0
	for key := range m.appEnvironmentSecretSuppressions {
		if key.AppID == appID {
			count++
		}
	}
	return count
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

func (m *MemStore) PutAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key, ref string) error {
	return m.putAppEnvironmentSecretReference(ctx, accountID, appID, scope, "", key, ref)
}

func (m *MemStore) putAppEnvironmentSecretReference(_ context.Context, accountID, appID, scope, expectedEnvironmentID, key, ref string) error {
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
	if expectedEnvironmentID != "" && expectedEnvironmentID != env.ID {
		return ErrConflict
	}
	source, err := m.gitOpsGuardScopedWriteLocked(accountID, appID, scope, []string{"secret_refs/" + key})
	if err != nil {
		return err
	}
	if expectedEnvironmentID != "" {
		if _, exists := m.secrets[secretKey{AppID: appID, Scope: scope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix)}]; !exists {
			return ErrEnvironmentSecretReferenceSourceNotFound
		}
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
		return &EnvironmentSecretReferenceQuotaError{Observed: count}
	}
	if m.appEnvironmentSecretRefs == nil {
		m.appEnvironmentSecretRefs = map[environmentSecretRefKey]environmentSecretRef{}
	}
	m.appEnvironmentSecretRefs[environmentSecretRefKey{appID, env.ID, key}] = environmentSecretRef{ref, time.Now().UTC()}
	delete(m.appEnvironmentSecretSuppressions, environmentSecretRefKey{appID, env.ID, key})
	m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope, time.Now().UTC())
	touchGitOpsMemoryIntent(source)
	return nil
}

func (m *MemStore) DeleteAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key string) error {
	return m.deleteAppEnvironmentSecretReference(ctx, accountID, appID, scope, "", key)
}

func (m *MemStore) deleteAppEnvironmentSecretReference(_ context.Context, accountID, appID, scope, expectedEnvironmentID, key string) error {
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
	if expectedEnvironmentID != "" && expectedEnvironmentID != env.ID {
		return ErrConflict
	}
	source, err := m.gitOpsGuardScopedWriteLocked(accountID, appID, scope, []string{"secret_refs/" + key})
	if err != nil {
		return err
	}
	refKey := environmentSecretRefKey{appID, env.ID, key}
	if _, exists := m.appEnvironmentSecretSuppressions[refKey]; exists {
		return nil
	}
	if m.environmentSecretSuppressionCountLocked(appID) >= api.EnvironmentSecretReferenceSuppressionsMaxPerApp {
		return &EnvironmentSecretReferenceSuppressionQuotaError{}
	}
	m.suppressEnvironmentSecretReferenceLocked(refKey, time.Now().UTC())
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
	return s.writeAppEnvironmentSecretReference(ctx, accountID, appID, scope, "", key, ref, false)
}
func (s *PgStore) DeleteAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, key string) error {
	return s.writeAppEnvironmentSecretReference(ctx, accountID, appID, scope, "", key, "", true)
}
func (s *PgStore) writeAppEnvironmentSecretReference(ctx context.Context, accountID, appID, scope, expectedEnvironmentID, key, ref string, remove bool) error {
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
	if expectedEnvironmentID != "" && pgUUIDString(env.ID) != expectedEnvironmentID {
		return ErrConflict
	}
	{
		owned, err := q.EnvironmentSecretReferenceWriteOwned(ctx, tx, sqlc.EnvironmentSecretReferenceWriteOwnedParams{
			AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EnvironmentID: env.ID, Key: key,
		})
		if err != nil {
			return mapErr(err)
		}
		if owned {
			return ErrEnvironmentGitManaged
		}
	}
	if !remove {
		if expectedEnvironmentID != "" {
			present, err := q.EnvironmentSecretReferenceSourcePresent(ctx, tx, sqlc.EnvironmentSecretReferenceSourcePresentParams{
				AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope, Key: strings.TrimPrefix(ref, api.SecretRefPrefix),
			})
			if err != nil {
				return mapErr(err)
			}
			if !present {
				return ErrEnvironmentSecretReferenceSourceNotFound
			}
		}
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
			return &EnvironmentSecretReferenceQuotaError{Observed: int(count.Total + 1)}
		}
	}
	if remove {
		err = q.DeleteEnvironmentGitOpsSecretReference(ctx, tx, sqlc.DeleteEnvironmentGitOpsSecretReferenceParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EnvironmentID: env.ID, Key: key})
		if err == nil {
			err = q.PutEnvironmentSecretReferenceSuppression(ctx, tx, sqlc.PutEnvironmentSecretReferenceSuppressionParams{AccountID: mustPgUUID(accountID), ProjectID: env.ProjectID, AppID: mustPgUUID(appID), EnvironmentID: env.ID, Scope: scope, Key: key})
		}
	} else {
		err = q.DeleteEnvironmentSecretReferenceSuppression(ctx, tx, sqlc.DeleteEnvironmentSecretReferenceSuppressionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EnvironmentID: env.ID, Key: key})
		if err == nil {
			err = q.PutEnvironmentGitOpsSecretReference(ctx, tx, sqlc.PutEnvironmentGitOpsSecretReferenceParams{AccountID: mustPgUUID(accountID), ProjectID: env.ProjectID, AppID: mustPgUUID(appID), EnvironmentID: env.ID, Scope: scope, Key: key, SecretName: strings.TrimPrefix(ref, api.SecretRefPrefix)})
		}
	}
	if err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
