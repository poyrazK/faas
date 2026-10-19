package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// APIConsumerTrial is a consumer's one free trial (ADR-1314): its usage in
// [StartsAt, EndsAt) is priced at zero and consumes no included units.
// Trials never move into the past, so finalized statements stay exact.
type APIConsumerTrial struct {
	AccountID  string
	AppID      string
	ConsumerID string
	StartsAt   time.Time
	EndsAt     time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MaxAPIConsumerTrialDays bounds a trial's length.
const MaxAPIConsumerTrialDays = api.ConsumerTrialMaxDays

// Active reports whether the trial prices minute at zero.
func (t APIConsumerTrial) Active(minute time.Time) bool {
	return !minute.Before(t.StartsAt) && minute.Before(t.EndsAt)
}

// APIConsumerTrialStore persists consumer trials (ADR-1314).
type APIConsumerTrialStore interface {
	// PutAPIConsumerTrial creates or replaces a consumer's trial.
	PutAPIConsumerTrial(ctx context.Context, t APIConsumerTrial) (APIConsumerTrial, error)
	// GetAPIConsumerTrial is ErrNotFound when the consumer has no trial.
	GetAPIConsumerTrial(ctx context.Context, accountID, appID, consumerID string) (APIConsumerTrial, error)
	// DeleteAPIConsumerTrial removes a trial that has not started.
	DeleteAPIConsumerTrial(ctx context.Context, accountID, appID, consumerID string) error
}

var (
	_ APIConsumerTrialStore = (*MemStore)(nil)
	_ APIConsumerTrialStore = (*PgStore)(nil)
)

func validateAPIConsumerTrial(t APIConsumerTrial) error {
	for name, id := range map[string]string{"account_id": t.AccountID, "app_id": t.AppID, "consumer_id": t.ConsumerID} {
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

func apiConsumerTrialKey(accountID, appID, consumerID string) string {
	return accountID + "\x00" + appID + "\x00" + consumerID
}

// --- MemStore ---

func (m *MemStore) PutAPIConsumerTrial(_ context.Context, t APIConsumerTrial) (APIConsumerTrial, error) {
	if err := validateAPIConsumerTrial(t); err != nil {
		return APIConsumerTrial{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.apiConsumers[t.ConsumerID]; !ok || c.AccountID != t.AccountID || c.AppID != t.AppID {
		return APIConsumerTrial{}, ErrNotFound
	}
	if m.apiConsumerTrials == nil {
		m.apiConsumerTrials = map[string]APIConsumerTrial{}
	}
	key := apiConsumerTrialKey(t.AccountID, t.AppID, t.ConsumerID)
	now := time.Now().UTC()
	t.StartsAt, t.EndsAt, t.CreatedAt, t.UpdatedAt = t.StartsAt.UTC(), t.EndsAt.UTC(), now, now
	if prior, ok := m.apiConsumerTrials[key]; ok {
		t.CreatedAt = prior.CreatedAt
	}
	m.apiConsumerTrials[key] = t
	return t, nil
}

func (m *MemStore) GetAPIConsumerTrial(_ context.Context, accountID, appID, consumerID string) (APIConsumerTrial, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.apiConsumerTrials[apiConsumerTrialKey(accountID, appID, consumerID)]
	if !ok {
		return APIConsumerTrial{}, ErrNotFound
	}
	return t, nil
}

func (m *MemStore) DeleteAPIConsumerTrial(_ context.Context, accountID, appID, consumerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := apiConsumerTrialKey(accountID, appID, consumerID)
	if _, ok := m.apiConsumerTrials[key]; !ok {
		return ErrNotFound
	}
	delete(m.apiConsumerTrials, key)
	return nil
}

// --- PgStore ---

const apiConsumerTrialCols = `account_id::text, app_id::text, consumer_id::text, starts_at, ends_at, created_at, updated_at`

func scanAPIConsumerTrial(row pgx.Row) (APIConsumerTrial, error) {
	var t APIConsumerTrial
	err := row.Scan(&t.AccountID, &t.AppID, &t.ConsumerID, &t.StartsAt, &t.EndsAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIConsumerTrial{}, ErrNotFound
	}
	if err != nil {
		return APIConsumerTrial{}, err
	}
	t.StartsAt, t.EndsAt, t.CreatedAt, t.UpdatedAt = t.StartsAt.UTC(), t.EndsAt.UTC(), t.CreatedAt.UTC(), t.UpdatedAt.UTC()
	return t, nil
}

func (s *PgStore) PutAPIConsumerTrial(ctx context.Context, t APIConsumerTrial) (APIConsumerTrial, error) {
	if err := validateAPIConsumerTrial(t); err != nil {
		return APIConsumerTrial{}, err
	}
	out, err := scanAPIConsumerTrial(s.pool.QueryRow(ctx, `insert into api_consumer_trials (account_id, app_id, consumer_id, starts_at, ends_at)
		select c.account_id, c.app_id, c.id, $4, $5 from api_consumers c
		 where c.account_id = $1::uuid and c.app_id = $2::uuid and c.id = $3::uuid
		on conflict (consumer_id) do update set starts_at = excluded.starts_at, ends_at = excluded.ends_at, updated_at = now()
		returning `+apiConsumerTrialCols, t.AccountID, t.AppID, t.ConsumerID, t.StartsAt.UTC(), t.EndsAt.UTC()))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return APIConsumerTrial{}, fmt.Errorf("put consumer trial: %w", err)
	}
	return out, err
}

func (s *PgStore) GetAPIConsumerTrial(ctx context.Context, accountID, appID, consumerID string) (APIConsumerTrial, error) {
	return scanAPIConsumerTrial(s.pool.QueryRow(ctx, `select `+apiConsumerTrialCols+` from api_consumer_trials
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid`, accountID, appID, consumerID))
}

func (s *PgStore) DeleteAPIConsumerTrial(ctx context.Context, accountID, appID, consumerID string) error {
	tag, err := s.pool.Exec(ctx, `delete from api_consumer_trials
		where account_id = $1::uuid and app_id = $2::uuid and consumer_id = $3::uuid`, accountID, appID, consumerID)
	if err != nil {
		return fmt.Errorf("delete consumer trial: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
