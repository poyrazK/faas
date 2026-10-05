package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ HealthStore = (*PostgresStore)(nil)

func healthTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func (s *PostgresStore) ClaimHealthCheck(ctx context.Context, token string, now, until time.Time) (HealthClaim, error) {
	if token == "" || now.IsZero() || !until.After(now) {
		return HealthClaim{}, ErrInvalid
	}
	row, err := sqlc.New().ClaimManagedPostgresHealthCheck(ctx, s.pool, sqlc.ClaimManagedPostgresHealthCheckParams{
		Now: healthTimestamp(now), LeaseToken: token, LeaseUntil: healthTimestamp(until),
	})
	if err != nil {
		return HealthClaim{}, mapPostgresError(err)
	}
	return HealthClaim{LeaseToken: token, LeaseUntil: until, AttemptCount: row.AttemptCount, Database: Database{
		ID: row.ID, AccountID: row.AccountID, BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
		ProviderResourceID: row.ProviderResourceID, DesiredGeneration: row.DesiredGeneration, State: StateReady,
		Spec: Spec{Region: row.Region, PostgresMajor: int(row.PostgresMajor), Class: ServiceClass(row.ServiceClass),
			Availability: Availability(row.Availability), ScaleToZero: row.ScaleToZero,
			StorageLimitBytes: row.StorageLimitBytes, RestoreWindowSeconds: row.RestoreWindowSeconds},
	}}, nil
}

func (s *PostgresStore) FinishHealthCheck(ctx context.Context, claim HealthClaim, result HealthResult) error {
	if err := validateHealthResult(result); err != nil {
		return err
	}
	if claim.LeaseToken == "" {
		return ErrInvalid
	}
	count, err := sqlc.New().FinishManagedPostgresHealthCheck(ctx, s.pool, sqlc.FinishManagedPostgresHealthCheckParams{
		DatabaseID: claim.Database.ID, LeaseToken: claim.LeaseToken,
		ProviderStatus: result.ProviderStatus, ComputeState: string(result.ComputeState), CheckedAt: healthTimestamp(result.CheckedAt),
		Succeeded: result.Succeeded, ErrorCode: result.LastErrorCode, NextCheckAt: healthTimestamp(result.NextCheckAt),
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) ReadHealthSnapshots(ctx context.Context, accountID string, ids []string) (map[string]HealthSnapshot, error) {
	rows, err := sqlc.New().ReadManagedPostgresHealthSnapshots(ctx, s.pool, sqlc.ReadManagedPostgresHealthSnapshotsParams{AccountID: accountID, DatabaseIds: ids})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make(map[string]HealthSnapshot, len(rows))
	for _, row := range rows {
		out[row.DatabaseID] = HealthSnapshot{ProviderStatus: row.ProviderStatus, ComputeState: ComputeState(row.ComputeState), LastErrorCode: row.LastErrorCode}
		snapshot := out[row.DatabaseID]
		if row.CheckedAt.Valid {
			snapshot.CheckedAt = row.CheckedAt.Time
		}
		if row.LastSuccessAt.Valid {
			snapshot.LastSuccessAt = row.LastSuccessAt.Time
		}
		out[row.DatabaseID] = snapshot
	}
	return out, nil
}

func (s *PostgresStore) CountDatabaseHealth(ctx context.Context, cutoff, now time.Time) (HealthCounts, error) {
	row, err := sqlc.New().CountManagedPostgresHealth(ctx, s.pool, sqlc.CountManagedPostgresHealthParams{Cutoff: healthTimestamp(cutoff), Now: healthTimestamp(now)})
	if err != nil {
		return HealthCounts{}, mapPostgresError(err)
	}
	return HealthCounts{Healthy: row.Healthy, Degraded: row.Degraded, Unknown: row.Unknown, Stale: row.Stale}, nil
}
