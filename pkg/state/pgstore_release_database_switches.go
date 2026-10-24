package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DatabaseSwitchStore = (*PgStore)(nil)

func databaseSwitchFromRow(r sqlc.ReleaseDatabaseSwitch) DatabaseSwitch {
	sw := DatabaseSwitch{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Scope: r.Scope,
		FromDeploymentID: dbSwitchUUID(r.FromDeploymentID), TargetDeploymentID: pgUUIDString(r.TargetDeploymentID),
		UndoneDeploymentIDs: pgUUIDStrings(r.UndoneDeploymentIds), SourceDatabaseID: pgUUIDString(r.SourceDatabaseID),
		TargetDatabaseID: dbSwitchUUID(r.TargetDatabaseID), TargetName: r.TargetName, RestorePoint: r.RestorePoint.Time.UTC(),
		CutoverID: dbSwitchUUID(r.CutoverID), Phase: r.Phase, FailureCode: r.FailureCode, FailureDetail: r.FailureDetail,
		RequestedBy: r.RequestedBy, CancelRequestedAt: dbSwitchTime(r.CancelRequestedAt), FencedAt: dbSwitchTime(r.FencedAt),
		DrainRequestedAt: dbSwitchTime(r.DrainRequestedAt), ReleaseSwitchedAt: dbSwitchTime(r.ReleaseSwitchedAt),
		ActivatedAt: dbSwitchTime(r.ActivatedAt), FinishedAt: dbSwitchTime(r.FinishedAt), PhaseStartedAt: r.PhaseStartedAt.Time.UTC(),
		Drain:        DatabaseSwitchDrain{Retired: int(r.DrainRetired), Parked: int(r.DrainParked), Pending: int(r.DrainPending), Blocked: int(r.DrainBlocked)},
		AttemptCount: int(r.AttemptCount), LastErrorCode: r.LastErrorCode, LeaseUntil: dbSwitchTime(r.LeaseUntil),
		RetryAt: r.RetryAt.Time.UTC(), CreatedAt: r.CreatedAt.Time.UTC(), UpdatedAt: r.UpdatedAt.Time.UTC(),
	}
	if r.LeaseToken.Valid {
		sw.LeaseToken = r.LeaseToken.String
	}
	return sw
}

func dbSwitchUUID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return pgUUIDString(id)
}

func dbSwitchNullUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func dbSwitchTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time.UTC()
	return &at
}

func dbSwitchNullTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgTimestamptzUTC(*t)
}

func (s *PgStore) CreateDatabaseSwitch(ctx context.Context, sw DatabaseSwitch) (DatabaseSwitch, error) {
	if !validNewDatabaseSwitch(sw) {
		return DatabaseSwitch{}, ErrInvalidArgument
	}
	if sw.ID == "" {
		sw.ID = uuid.NewString()
	}
	now := sw.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	row, err := sqlc.New().InsertReleaseDatabaseSwitch(ctx, s.pool, sqlc.InsertReleaseDatabaseSwitchParams{
		ID: mustPgUUID(sw.ID), AccountID: mustPgUUID(sw.AccountID), AppID: mustPgUUID(sw.AppID),
		Scope: normalizedDeploymentScope(sw.Scope), FromDeploymentID: dbSwitchNullUUID(sw.FromDeploymentID),
		TargetDeploymentID: mustPgUUID(sw.TargetDeploymentID), UndoneDeploymentIds: pgUUIDList(sw.UndoneDeploymentIDs),
		SourceDatabaseID: mustPgUUID(sw.SourceDatabaseID), TargetName: sw.TargetName,
		RestorePoint: pgTimestamptzUTC(sw.RestorePoint), RequestedBy: sw.RequestedBy, Now: pgTimestamptzUTC(now),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return DatabaseSwitch{}, ErrConflict
		}
		return DatabaseSwitch{}, fmt.Errorf("state: insert database switch: %w", mapErr(err))
	}
	return databaseSwitchFromRow(row), nil
}

func (s *PgStore) DatabaseSwitch(ctx context.Context, accountID, id string) (DatabaseSwitch, error) {
	if _, err := uuid.Parse(id); err != nil {
		return DatabaseSwitch{}, ErrNotFound
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return DatabaseSwitch{}, ErrNotFound
	}
	row, err := sqlc.New().GetReleaseDatabaseSwitch(ctx, s.pool, sqlc.GetReleaseDatabaseSwitchParams{ID: mustPgUUID(id), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return DatabaseSwitch{}, mapErr(err)
	}
	return databaseSwitchFromRow(row), nil
}

func (s *PgStore) ListDatabaseSwitches(ctx context.Context, accountID, appID string, limit int) ([]DatabaseSwitch, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := sqlc.New().ListReleaseDatabaseSwitches(ctx, s.pool, sqlc.ListReleaseDatabaseSwitchesParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), MaxRows: int32(limit)}) //nolint:gosec // bounded above
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]DatabaseSwitch, 0, len(rows))
	for _, r := range rows {
		out = append(out, databaseSwitchFromRow(r))
	}
	return out, nil
}

func (s *PgStore) ClaimDueDatabaseSwitches(ctx context.Context, token string, now, until time.Time, limit int) ([]DatabaseSwitch, error) {
	if token == "" || !until.After(now) || limit <= 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ClaimDueReleaseDatabaseSwitches(ctx, s.pool, sqlc.ClaimDueReleaseDatabaseSwitchesParams{
		Token: token, LeaseUntil: pgTimestamptzUTC(until), Now: pgTimestamptzUTC(now), MaxRows: int32(limit)}) //nolint:gosec // bounded above
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]DatabaseSwitch, 0, len(rows))
	for _, r := range rows {
		out = append(out, databaseSwitchFromRow(r))
	}
	return out, nil
}

func (s *PgStore) SaveDatabaseSwitch(ctx context.Context, sw DatabaseSwitch, now time.Time) (DatabaseSwitch, error) {
	if err := validDatabaseSwitchProgress(sw); err != nil {
		return DatabaseSwitch{}, err
	}
	if sw.LeaseToken == "" {
		return DatabaseSwitch{}, ErrConflict
	}
	row, err := sqlc.New().SaveReleaseDatabaseSwitch(ctx, s.pool, sqlc.SaveReleaseDatabaseSwitchParams{
		TargetDatabaseID: dbSwitchNullUUID(sw.TargetDatabaseID), CutoverID: dbSwitchNullUUID(sw.CutoverID), Phase: sw.Phase,
		FailureCode: sw.FailureCode, FailureDetail: sw.FailureDetail, LastErrorCode: sw.LastErrorCode,
		FencedAt: dbSwitchNullTime(sw.FencedAt), DrainRequestedAt: dbSwitchNullTime(sw.DrainRequestedAt),
		ReleaseSwitchedAt: dbSwitchNullTime(sw.ReleaseSwitchedAt), ActivatedAt: dbSwitchNullTime(sw.ActivatedAt),
		FinishedAt: dbSwitchNullTime(sw.FinishedAt), PhaseStartedAt: pgTimestamptzUTC(sw.PhaseStartedAt),
		DrainRetired: int32(sw.Drain.Retired), DrainParked: int32(sw.Drain.Parked), //nolint:gosec // drain counts are bounded by the plan cap
		DrainPending: int32(sw.Drain.Pending), DrainBlocked: int32(sw.Drain.Blocked), //nolint:gosec // drain counts are bounded by the plan cap
		AttemptCount: int32(min(sw.AttemptCount, 1_000_000)), RetryAt: pgTimestamptzUTC(sw.RetryAt), //nolint:gosec // capped
		Now: pgTimestamptzUTC(now), ID: mustPgUUID(sw.ID), Token: sw.LeaseToken,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DatabaseSwitch{}, ErrConflict
	}
	if err != nil {
		return DatabaseSwitch{}, fmt.Errorf("state: save database switch: %w", mapErr(err))
	}
	return databaseSwitchFromRow(row), nil
}

func (s *PgStore) RequestDatabaseSwitchCancel(ctx context.Context, accountID, appID, id string, now time.Time) (DatabaseSwitch, error) {
	if _, err := uuid.Parse(id); err != nil {
		return DatabaseSwitch{}, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.RequestReleaseDatabaseSwitchCancel(ctx, s.pool, sqlc.RequestReleaseDatabaseSwitchCancelParams{
		Now: pgTimestamptzUTC(now), ID: mustPgUUID(id), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err == nil {
		return databaseSwitchFromRow(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DatabaseSwitch{}, mapErr(err)
	}
	current, err := q.GetReleaseDatabaseSwitchForApp(ctx, s.pool, sqlc.GetReleaseDatabaseSwitchForAppParams{
		ID: mustPgUUID(id), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return DatabaseSwitch{}, mapErr(err)
	}
	return databaseSwitchFromRow(current), ErrConflict
}

func (s *PgStore) DrainingDatabaseSwitches(ctx context.Context, limit int) ([]DatabaseSwitch, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := sqlc.New().ListDrainingReleaseDatabaseSwitches(ctx, s.pool, int32(limit)) //nolint:gosec // bounded above
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]DatabaseSwitch, 0, len(rows))
	for _, r := range rows {
		out = append(out, databaseSwitchFromRow(r))
	}
	return out, nil
}

func (s *PgStore) ReleasesUndoneByDatabaseSwitches(ctx context.Context, appID, scope string) ([]string, error) {
	if !qualificationRecoveryUUIDValid(appID) {
		return nil, nil
	}
	ids, err := sqlc.New().ListReleasesUndoneByDatabaseSwitches(ctx, s.pool, sqlc.ListReleasesUndoneByDatabaseSwitchesParams{
		AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope)})
	if err != nil {
		return nil, mapErr(err)
	}
	return ids, nil
}
