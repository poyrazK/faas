package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PlatformTenantTrial is a platform tenant's one free trial (ADR-1322): its
// usage in [StartsAt, EndsAt) is priced at zero and consumes no included
// units, as a consumer trial does (ADR-1314).
type PlatformTenantTrial struct {
	AccountID string
	TenantID  string
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Active reports whether the trial prices minute at zero.
func (t PlatformTenantTrial) Active(minute time.Time) bool {
	return !minute.Before(t.StartsAt) && minute.Before(t.EndsAt)
}

// PlatformTenantTrialStore persists tenant trials (ADR-1322).
type PlatformTenantTrialStore interface {
	// PutPlatformTenantTrial creates or replaces a tenant's trial.
	PutPlatformTenantTrial(ctx context.Context, t PlatformTenantTrial) (PlatformTenantTrial, error)
	// GetPlatformTenantTrial is ErrNotFound when the tenant has no trial.
	GetPlatformTenantTrial(ctx context.Context, accountID, tenantID string) (PlatformTenantTrial, error)
	// DeletePlatformTenantTrial removes a trial that has not started.
	DeletePlatformTenantTrial(ctx context.Context, accountID, tenantID string) error
}

var (
	_ PlatformTenantTrialStore = (*MemStore)(nil)
	_ PlatformTenantTrialStore = (*PgStore)(nil)
)

func validatePlatformTenantTrial(t PlatformTenantTrial) error {
	for name, id := range map[string]string{"account_id": t.AccountID, "tenant_id": t.TenantID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: %s must be a UUID", ErrInvalidArgument, name)
		}
	}
	if !t.StartsAt.Equal(t.StartsAt.UTC().Truncate(time.Minute)) || !t.EndsAt.Equal(t.EndsAt.UTC().Truncate(time.Minute)) ||
		!t.EndsAt.After(t.StartsAt) || t.EndsAt.Sub(t.StartsAt) > MaxAPIConsumerTrialDays*24*time.Hour {
		return fmt.Errorf("%w: a trial runs whole UTC minutes, at most %d days", ErrInvalidArgument, MaxAPIConsumerTrialDays)
	}
	return nil
}

// --- MemStore ---

func (m *MemStore) PutPlatformTenantTrial(_ context.Context, t PlatformTenantTrial) (PlatformTenantTrial, error) {
	if err := validatePlatformTenantTrial(t); err != nil {
		return PlatformTenantTrial{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.platformTenants[t.TenantID]; !ok || tenant.AccountID != t.AccountID {
		return PlatformTenantTrial{}, ErrNotFound
	}
	if m.platformTenantTrials == nil {
		m.platformTenantTrials = map[string]PlatformTenantTrial{}
	}
	now := time.Now().UTC()
	t.StartsAt, t.EndsAt, t.CreatedAt, t.UpdatedAt = t.StartsAt.UTC(), t.EndsAt.UTC(), now, now
	if prior, ok := m.platformTenantTrials[t.TenantID]; ok {
		t.CreatedAt = prior.CreatedAt
	}
	m.platformTenantTrials[t.TenantID] = t
	return t, nil
}

func (m *MemStore) GetPlatformTenantTrial(_ context.Context, accountID, tenantID string) (PlatformTenantTrial, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.platformTenantTrials[tenantID]
	if !ok || t.AccountID != accountID {
		return PlatformTenantTrial{}, ErrNotFound
	}
	return t, nil
}

func (m *MemStore) DeletePlatformTenantTrial(_ context.Context, accountID, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.platformTenantTrials[tenantID]; !ok || t.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.platformTenantTrials, tenantID)
	return nil
}

// --- PgStore ---

const platformTenantTrialCols = `account_id::text, platform_tenant_id::text, starts_at, ends_at, created_at, updated_at`

func scanPlatformTenantTrial(row pgx.Row) (PlatformTenantTrial, error) {
	var t PlatformTenantTrial
	err := row.Scan(&t.AccountID, &t.TenantID, &t.StartsAt, &t.EndsAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformTenantTrial{}, ErrNotFound
	}
	if err != nil {
		return PlatformTenantTrial{}, err
	}
	t.StartsAt, t.EndsAt, t.CreatedAt, t.UpdatedAt = t.StartsAt.UTC(), t.EndsAt.UTC(), t.CreatedAt.UTC(), t.UpdatedAt.UTC()
	return t, nil
}

func (s *PgStore) PutPlatformTenantTrial(ctx context.Context, t PlatformTenantTrial) (PlatformTenantTrial, error) {
	if err := validatePlatformTenantTrial(t); err != nil {
		return PlatformTenantTrial{}, err
	}
	out, err := scanPlatformTenantTrial(s.pool.QueryRow(ctx, `insert into platform_tenant_trials (account_id, platform_tenant_id, starts_at, ends_at)
		select p.account_id, p.id, $3, $4 from platform_tenants p where p.account_id = $1::uuid and p.id = $2::uuid
		on conflict (platform_tenant_id) do update set starts_at = excluded.starts_at, ends_at = excluded.ends_at, updated_at = now()
		returning `+platformTenantTrialCols, t.AccountID, t.TenantID, t.StartsAt.UTC(), t.EndsAt.UTC()))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return PlatformTenantTrial{}, fmt.Errorf("put platform tenant trial: %w", err)
	}
	return out, err
}

func (s *PgStore) GetPlatformTenantTrial(ctx context.Context, accountID, tenantID string) (PlatformTenantTrial, error) {
	return scanPlatformTenantTrial(s.pool.QueryRow(ctx, `select `+platformTenantTrialCols+` from platform_tenant_trials
		where account_id = $1::uuid and platform_tenant_id = $2::uuid`, accountID, tenantID))
}

func (s *PgStore) DeletePlatformTenantTrial(ctx context.Context, accountID, tenantID string) error {
	tag, err := s.pool.Exec(ctx, `delete from platform_tenant_trials
		where account_id = $1::uuid and platform_tenant_id = $2::uuid`, accountID, tenantID)
	if err != nil {
		return fmt.Errorf("delete platform tenant trial: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
