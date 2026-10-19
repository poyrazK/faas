package managedpostgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ConnectionCapacityStore = (*PostgresStore)(nil)

func (s *PostgresStore) ClaimConnectionCapacity(ctx context.Context, d Database, token string, now, until time.Time) error {
	if token == "" || now.IsZero() || !until.After(now) || until.Sub(now) > api.DataAPIConnectionCapacityLease {
		return ErrInvalid
	}
	count, err := sqlc.New().ClaimManagedPostgresConnectionCapacity(ctx, s.pool, sqlc.ClaimManagedPostgresConnectionCapacityParams{
		DatabaseID: d.ID, AccountID: d.AccountID, BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint,
		ProviderResourceID: d.ProviderResourceID, DataResourceID: d.DataResourceID, Generation: d.DesiredGeneration,
		Now: healthTimestamp(now), LeaseToken: token, LeaseUntil: healthTimestamp(until),
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func validateConnectionCapacityResult(d Database, result ConnectionCapacityResult, now time.Time) error {
	if result.CheckedAt.IsZero() || now.Before(result.CheckedAt) || result.ExpiresAt.Before(result.CheckedAt) ||
		result.ExpiresAt.Sub(result.CheckedAt) > api.DataAPIConnectionBudgetMaxAge {
		return ErrInvalid
	}
	if result.State == "unknown" && result.Evidence == nil && result.ExpiresAt.Equal(result.CheckedAt) {
		return nil
	}
	c := result.Evidence
	if result.State != "verified" || !c.valid(d.ID, d.BackendFingerprint, d.DesiredGeneration, now, now) ||
		!c.CheckedAt.Equal(result.CheckedAt) || !c.ExpiresAt.Equal(result.ExpiresAt) {
		return ErrInvalid
	}
	return nil
}

func (s *PostgresStore) FinishConnectionCapacity(ctx context.Context, d Database, token string, result ConnectionCapacityResult, now time.Time) error {
	if token == "" || validateConnectionCapacityResult(d, result, now) != nil {
		return ErrInvalid
	}
	var evidence []byte
	if result.Evidence != nil {
		var err error
		evidence, err = json.Marshal(result.Evidence)
		if err != nil {
			return ErrInvalid
		}
	}
	count, err := sqlc.New().FinishManagedPostgresConnectionCapacity(ctx, s.pool, sqlc.FinishManagedPostgresConnectionCapacityParams{
		DatabaseID: d.ID, AccountID: d.AccountID, LeaseToken: token, State: result.State, Evidence: evidence,
		CheckedAt: healthTimestamp(result.CheckedAt), ExpiresAt: healthTimestamp(result.ExpiresAt), Now: healthTimestamp(now),
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
