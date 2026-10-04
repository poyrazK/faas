package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEnvironmentSecretReferenceSourceNotFound = errors.New("environment secret reference source is unavailable")

type EnvironmentSecretReferenceQuotaError struct{ Observed int }

func (e *EnvironmentSecretReferenceQuotaError) Error() string { return ErrQuotaExceeded.Error() }
func (e *EnvironmentSecretReferenceQuotaError) Unwrap() error { return ErrQuotaExceeded }

type EnvironmentSecretReferenceSuppressionQuotaError struct{}

func (e *EnvironmentSecretReferenceSuppressionQuotaError) Error() string {
	return "secret reference suppression limit reached"
}
func (e *EnvironmentSecretReferenceSuppressionQuotaError) Unwrap() error { return ErrQuotaExceeded }

// Controls retain the catalog identity observed before a customer mutation.
// A replacement with the same slug cannot receive that mutation.
type AppEnvironmentSecretReferenceTarget struct {
	AccountID, AppID, EnvironmentID, Scope string
}

type AppEnvironmentSecretReferenceSnapshot struct {
	Target         AppEnvironmentSecretReferenceTarget
	References     map[string]string
	SuppressedKeys []string
	Count          int
}

type AppEnvironmentSecretReferenceControlStore interface {
	ReadAppEnvironmentSecretReferences(context.Context, string, string, string) (AppEnvironmentSecretReferenceSnapshot, error)
	SetAppEnvironmentSecretReference(context.Context, AppEnvironmentSecretReferenceTarget, string, string) error
	RemoveAppEnvironmentSecretReference(context.Context, AppEnvironmentSecretReferenceTarget, string) error
}

var (
	_ AppEnvironmentSecretReferenceControlStore = (*MemStore)(nil)
	_ AppEnvironmentSecretReferenceControlStore = (*PgStore)(nil)
)

func (m *MemStore) ReadAppEnvironmentSecretReferences(_ context.Context, accountID, appID, scope string) (AppEnvironmentSecretReferenceSnapshot, error) {
	if api.ValidateScope(scope) != nil {
		return AppEnvironmentSecretReferenceSnapshot{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, exists := m.apps[appID]
	if !exists || app.AccountID != accountID || app.Status == AppDeleted {
		return AppEnvironmentSecretReferenceSnapshot{}, ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(app.ProjectID, scope)
	if err != nil {
		return AppEnvironmentSecretReferenceSnapshot{}, err
	}
	count := 0
	for key := range m.envs {
		if key.AppID == appID {
			count++
		}
	}
	for key := range m.appEnvironmentSecretRefs {
		if key.AppID == appID {
			count++
		}
	}
	return AppEnvironmentSecretReferenceSnapshot{Target: AppEnvironmentSecretReferenceTarget{accountID, appID, env.ID, scope},
		References: m.environmentSecretRefsLocked(appID, scope), SuppressedKeys: m.environmentSecretSuppressionsLocked(appID, scope), Count: count}, nil
}

func (s *PgStore) ReadAppEnvironmentSecretReferences(ctx context.Context, accountID, appID, scope string) (AppEnvironmentSecretReferenceSnapshot, error) {
	if api.ValidateScope(scope) != nil {
		return AppEnvironmentSecretReferenceSnapshot{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadAppEnvironmentSecretReferenceSnapshot(ctx, s.pool, sqlc.ReadAppEnvironmentSecretReferenceSnapshotParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope,
	})
	if err != nil {
		return AppEnvironmentSecretReferenceSnapshot{}, mapErr(err)
	}
	refs := map[string]string{}
	if err := json.Unmarshal(row.Refs, &refs); err != nil {
		return AppEnvironmentSecretReferenceSnapshot{}, ErrInvalidArgument
	}
	return AppEnvironmentSecretReferenceSnapshot{Target: AppEnvironmentSecretReferenceTarget{accountID, appID, pgUUIDString(row.EnvironmentID), scope},
		References: refs, SuppressedKeys: row.SuppressedKeys, Count: int(row.Count)}, nil
}

func (m *MemStore) SetAppEnvironmentSecretReference(ctx context.Context, target AppEnvironmentSecretReferenceTarget, key, ref string) error {
	if target.EnvironmentID == "" {
		return ErrInvalidArgument
	}
	return m.putAppEnvironmentSecretReference(ctx, target.AccountID, target.AppID, target.Scope, target.EnvironmentID, key, ref)
}

func (s *PgStore) SetAppEnvironmentSecretReference(ctx context.Context, target AppEnvironmentSecretReferenceTarget, key, ref string) error {
	if target.EnvironmentID == "" {
		return ErrInvalidArgument
	}
	return s.writeAppEnvironmentSecretReference(ctx, target.AccountID, target.AppID, target.Scope, target.EnvironmentID, key, ref, false)
}

func (m *MemStore) RemoveAppEnvironmentSecretReference(ctx context.Context, target AppEnvironmentSecretReferenceTarget, key string) error {
	if target.EnvironmentID == "" {
		return ErrInvalidArgument
	}
	return m.deleteAppEnvironmentSecretReference(ctx, target.AccountID, target.AppID, target.Scope, target.EnvironmentID, key)
}

func (s *PgStore) RemoveAppEnvironmentSecretReference(ctx context.Context, target AppEnvironmentSecretReferenceTarget, key string) error {
	if target.EnvironmentID == "" {
		return ErrInvalidArgument
	}
	return s.writeAppEnvironmentSecretReference(ctx, target.AccountID, target.AppID, target.Scope, target.EnvironmentID, key, "", true)
}
