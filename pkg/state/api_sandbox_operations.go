package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	APISandboxPreparingDatabase = "preparing_database"
	APISandboxRunningMigrations = "running_migrations"
	APISandboxStartingRelease   = "starting_release"
	APISandboxVerifyingAPI      = "verifying_api"
	APISandboxLive              = "live"
	APISandboxFailed            = "failed"
)

type APISandboxOperation struct {
	AppID         string
	SmokePath     string
	Phase         string
	StartedAt     time.Time
	NextAttemptAt *time.Time
	LeaseToken    string
	LeaseUntil    *time.Time
	DeploymentID  string
	Failure       *api.APISandboxFailure
	Verification  *api.APISandboxVerification
	Scenario      *api.APISandboxScenario
}

type APISandboxOperationStore interface {
	GetAPISandboxOperation(ctx context.Context, appID string) (APISandboxOperation, error)
	ClaimAPISandboxOperation(ctx context.Context, now time.Time, lease time.Duration) (APISandboxOperation, error)
	SaveAPISandboxOperation(ctx context.Context, operation APISandboxOperation, now time.Time) error
}

func apiSandboxOperationFromRow(row sqlc.ApiSandboxProvisioning, err error) (APISandboxOperation, error) {
	if err != nil {
		return APISandboxOperation{}, mapErr(err)
	}
	op := APISandboxOperation{
		AppID: pgUUIDString(row.AppID), SmokePath: row.SmokePath, Phase: row.Phase,
		StartedAt: row.StartedAt.Time.UTC(), NextAttemptAt: optionalPgTime(row.NextAttemptAt),
		LeaseToken: pgUUIDString(row.LeaseToken), LeaseUntil: optionalPgTime(row.LeaseUntil),
		DeploymentID: pgUUIDString(row.DeploymentID),
	}
	if row.Scenario != nil {
		if err := json.Unmarshal(row.Scenario, &op.Scenario); err != nil {
			return APISandboxOperation{}, fmt.Errorf("state: decode sandbox scenario snapshot: %w", err)
		}
	}
	if row.Failure != nil {
		if err := json.Unmarshal(row.Failure, &op.Failure); err != nil {
			return APISandboxOperation{}, fmt.Errorf("state: decode sandbox failure: %w", err)
		}
	}
	if row.Verification != nil {
		if err := json.Unmarshal(row.Verification, &op.Verification); err != nil {
			return APISandboxOperation{}, fmt.Errorf("state: decode sandbox verification: %w", err)
		}
	}
	return op, nil
}

func (s *PgStore) GetAPISandboxOperation(ctx context.Context, appID string) (APISandboxOperation, error) {
	return apiSandboxOperationFromRow(sqlc.New().GetAPISandboxOperation(ctx, s.pool, mustPgUUID(appID)))
}

func (s *PgStore) ClaimAPISandboxOperation(ctx context.Context, now time.Time, lease time.Duration) (APISandboxOperation, error) {
	return apiSandboxOperationFromRow(sqlc.New().ClaimAPISandboxOperation(ctx, s.pool, sqlc.ClaimAPISandboxOperationParams{
		LeaseToken: mustPgUUID(uuid.NewString()), LeaseUntil: pgTimestamptzUTC(now.Add(lease)), ClaimedAt: pgTimestamptzUTC(now),
	}))
}

func (s *PgStore) SaveAPISandboxOperation(ctx context.Context, op APISandboxOperation, now time.Time) error {
	params := sqlc.SaveAPISandboxOperationParams{
		AppID: mustPgUUID(op.AppID), Phase: op.Phase, LeaseToken: mustPgUUID(op.LeaseToken),
		DeploymentID: mustPgUUID(op.DeploymentID), SavedAt: pgTimestamptzUTC(now),
	}
	if op.NextAttemptAt != nil {
		params.NextAttemptAt = pgtype.Timestamptz{Time: op.NextAttemptAt.UTC(), Valid: true}
	}
	var err error
	if op.Failure != nil {
		params.Failure, err = json.Marshal(op.Failure)
		if err != nil {
			return fmt.Errorf("state: encode sandbox failure: %w", err)
		}
	}
	if op.Verification != nil {
		params.Verification, err = json.Marshal(op.Verification)
		if err != nil {
			return fmt.Errorf("state: encode sandbox verification: %w", err)
		}
	}
	rows, err := sqlc.New().SaveAPISandboxOperation(ctx, s.pool, params)
	if err == nil && rows == 0 {
		return ErrConflict
	}
	return mapErr(err)
}

func (m *MemStore) GetAPISandboxOperation(_ context.Context, appID string) (APISandboxOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.apiSandboxOperations[appID]
	if !ok {
		return APISandboxOperation{}, ErrNotFound
	}
	return cloneAPISandboxOperation(op), nil
}

func (m *MemStore) ClaimAPISandboxOperation(_ context.Context, now time.Time, lease time.Duration) (APISandboxOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until := now.Add(lease)
	var candidate APISandboxOperation
	for _, op := range m.apiSandboxOperations {
		if op.NextAttemptAt == nil || op.NextAttemptAt.After(now) || (op.LeaseUntil != nil && op.LeaseUntil.After(now)) ||
			!APISandboxAcceptsRequests(m.apps[op.AppID], until) {
			continue
		}
		if candidate.AppID == "" || op.NextAttemptAt.Before(*candidate.NextAttemptAt) ||
			(op.NextAttemptAt.Equal(*candidate.NextAttemptAt) && op.AppID < candidate.AppID) {
			candidate = op
		}
	}
	if candidate.AppID == "" {
		return APISandboxOperation{}, ErrNotFound
	}
	candidate.LeaseToken, candidate.LeaseUntil = uuid.NewString(), &until
	m.apiSandboxOperations[candidate.AppID] = candidate
	return cloneAPISandboxOperation(candidate), nil
}

func (m *MemStore) SaveAPISandboxOperation(_ context.Context, op APISandboxOperation, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxOperations[op.AppID]
	if current.LeaseToken != op.LeaseToken || current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
		return ErrConflict
	}
	current.Phase, current.NextAttemptAt, current.DeploymentID = op.Phase, op.NextAttemptAt, op.DeploymentID
	current.Failure, current.Verification = op.Failure, op.Verification
	current.LeaseToken, current.LeaseUntil = "", nil
	m.apiSandboxOperations[op.AppID] = cloneAPISandboxOperation(current)
	return nil
}

func cloneAPISandboxOperation(op APISandboxOperation) APISandboxOperation {
	op.Scenario = cloneAPISandboxScenario(op.Scenario)
	if op.NextAttemptAt != nil {
		t := *op.NextAttemptAt
		op.NextAttemptAt = &t
	}
	if op.LeaseUntil != nil {
		t := *op.LeaseUntil
		op.LeaseUntil = &t
	}
	if op.Failure != nil {
		failure := *op.Failure
		op.Failure = &failure
	}
	if op.Verification != nil {
		verification := *op.Verification
		op.Verification = &verification
	}
	return op
}
