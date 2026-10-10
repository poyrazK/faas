package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func appForkExecFromSQLC(row sqlc.AppForkExec) AppForkExec {
	return AppForkExec{
		ID: pgUUIDString(row.ID), ForkID: pgUUIDString(row.ForkID), AccountID: pgUUIDString(row.AccountID),
		AppID: pgUUIDString(row.AppID), RequestedBy: row.RequestedBy, Command: row.Command, CommandShell: row.CommandShell,
		TimeoutSeconds: int(row.TimeoutSeconds), MaxOutputBytes: int(row.MaxOutputBytes),
		Status: AppForkExecStatus(row.Status), ExitCode: executionIntPtr(row.ExitCode), OutputTruncated: row.OutputTruncated,
		Stdout: row.Stdout, Stderr: row.Stderr, FailureCode: executionStringPtr(row.FailureCode),
		FailureMessage: executionStringPtr(row.FailureMessage), CreatedAt: row.CreatedAt.Time.UTC(),
		StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: timestamptzToTimePtr(row.FinishedAt),
		UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func appForkExecRow(row sqlc.AppForkExec, err error, noRow error) (AppForkExec, error) {
	if isNoRows(err) {
		return AppForkExec{}, noRow
	}
	if err != nil {
		return AppForkExec{}, mapErr(err)
	}
	return appForkExecFromSQLC(row), nil
}

func (s *PgStore) CreateAppForkExec(ctx context.Context, params CreateAppForkExecParams) (AppForkExec, error) {
	p, err := validateCreateAppForkExec(params)
	if err != nil {
		return AppForkExec{}, err
	}
	row, err := sqlc.New().InsertAppForkExec(ctx, s.pool, sqlc.InsertAppForkExecParams{
		RequestedBy: p.RequestedBy, Command: p.Command, CommandShell: p.CommandShell,
		TimeoutSeconds: int32(p.TimeoutSeconds), MaxOutputBytes: int32(p.MaxOutputBytes), //nolint:gosec // validated ranges
		Now: pgTime(p.CreatedAt), ForkID: mustPgUUID(p.ForkID), AppID: mustPgUUID(p.AppID),
		AccountID: mustPgUUID(p.AccountID), MaxPending: int32(p.MaxPending), //nolint:gosec // small limit
	})
	return appForkExecRow(row, err, ErrAppForkExecRefused)
}

func (s *PgStore) AppForkExecByID(ctx context.Context, accountID, appID, forkID, execID string) (AppForkExec, error) {
	row, err := sqlc.New().GetAppForkExec(ctx, s.pool, sqlc.GetAppForkExecParams{
		ExecID: mustPgUUID(execID), ForkID: mustPgUUID(forkID), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
	})
	return appForkExecRow(row, err, ErrNotFound)
}

func (s *PgStore) ListAppForkExecs(ctx context.Context, accountID, appID, forkID string, limit int) ([]AppForkExec, error) {
	rows, err := sqlc.New().ListAppForkExecs(ctx, s.pool, sqlc.ListAppForkExecsParams{
		ForkID: mustPgUUID(forkID), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
		RowLimit: int32(clampAppForkListLimit(limit)), //nolint:gosec // clamped to 100
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AppForkExec, 0, len(rows))
	for _, row := range rows {
		out = append(out, appForkExecFromSQLC(row))
	}
	return out, nil
}

func (s *PgStore) ClaimNextAppForkExec(ctx context.Context, owner string, now time.Time) (AppForkExec, error) {
	row, err := sqlc.New().ClaimNextAppForkExec(ctx, s.pool, sqlc.ClaimNextAppForkExecParams{Now: pgTime(now), Owner: owner})
	return appForkExecRow(row, err, ErrNotFound)
}

func (s *PgStore) FinishAppForkExec(ctx context.Context, p FinishAppForkExecParams) (AppForkExec, error) {
	if err := p.validate(); err != nil {
		return AppForkExec{}, err
	}
	var exit pgtype.Int4
	if p.ExitCode != nil {
		exit = pgtype.Int4{Int32: int32(*p.ExitCode), Valid: true} //nolint:gosec // process exit codes
	}
	row, err := sqlc.New().FinishAppForkExec(ctx, s.pool, sqlc.FinishAppForkExecParams{
		Status: string(p.Status), ExitCode: exit, OutputTruncated: p.OutputTruncated,
		Stdout: nonNilBytes(p.Stdout), Stderr: nonNilBytes(p.Stderr),
		FailureCode:    pgtype.Text{String: p.FailureCode, Valid: p.FailureCode != ""},
		FailureMessage: pgtype.Text{String: p.FailureMessage, Valid: p.FailureMessage != ""},
		Now:            pgTime(p.FinishedAt), ExecID: mustPgUUID(p.ID),
	})
	return appForkExecRow(row, err, ErrNotFound)
}

func (s *PgStore) FailOrphanedAppForkExecs(ctx context.Context, grace time.Duration, now time.Time) ([]AppForkExec, error) {
	rows, err := sqlc.New().FailOrphanedAppForkExecs(ctx, s.pool, sqlc.FailOrphanedAppForkExecsParams{
		Now: pgTime(now), GraceSeconds: int32(grace / time.Second), //nolint:gosec // seconds
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AppForkExec, 0, len(rows))
	for _, row := range rows {
		out = append(out, appForkExecFromSQLC(row))
	}
	return out, nil
}

func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}
