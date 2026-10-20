package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialPreviewCommandStore = (*PgStore)(nil)

func (s *PgStore) RegisterFinancialPreviewCommand(ctx context.Context, r FinancialPreviewCommandRuntime) error {
	if !validFinancialPreviewCommand(r) || uuid.Validate(r.NodeID) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	environmentID := pgtype.UUID{}
	if r.EnvironmentID != "" {
		environmentID = mustPgUUID(r.EnvironmentID)
	}
	if _, err := q.FinancialBudgetAccountLock(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return err
	}
	n, err := q.FinancialPreviewCommandRegister(ctx, tx, sqlc.FinancialPreviewCommandRegisterParams{
		TaskID: mustPgUUID(r.TaskID), LeaseToken: mustPgUUID(r.LeaseToken), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(r.AppID),
		DeploymentID: mustPgUUID(r.DeploymentID), NodeID: mustPgUUID(r.NodeID), EnvironmentID: environmentID,
		RamMb: int32(r.RAMMB), CpuMillicores: int32(r.CPUMillicores), RecordedAt: financialTimestamp(r.RecordedAt), PrepareUntil: financialTimestamp(r.PrepareUntil),
	})
	if err != nil {
		return financialBudgetStoreError(err)
	}
	if n == 0 {
		held, err := q.FinancialPreviewCommandHeld(ctx, tx, sqlc.FinancialPreviewCommandHeldParams{AppID: mustPgUUID(r.AppID), DeploymentID: mustPgUUID(r.DeploymentID), ObservedAt: financialTimestamp(r.RecordedAt)})
		if err != nil {
			return err
		}
		if held {
			return ErrFinancialBudgetHeld
		}
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ActivateFinancialPreviewCommand(ctx context.Context, id, token string) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().FinancialPreviewCommandActivate(ctx, s.pool, sqlc.FinancialPreviewCommandActivateParams{TaskID: mustPgUUID(id), LeaseToken: mustPgUUID(token)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) RetireFinancialPreviewCommand(ctx context.Context, id, token string, now time.Time) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().FinancialPreviewCommandRetire(ctx, s.pool, sqlc.FinancialPreviewCommandRetireParams{TaskID: mustPgUUID(id), LeaseToken: mustPgUUID(token), RetiredAt: financialTimestamp(now)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) FinancialPreviewCommands(ctx context.Context, account, app string) ([]FinancialPreviewCommandRuntime, error) {
	if uuid.Validate(account) != nil || uuid.Validate(app) != nil {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	rows, err := q.FinancialPreviewCommandList(ctx, s.pool, sqlc.FinancialPreviewCommandListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), PageSize: api.FinancialAllocationMax + 1})
	if err != nil {
		return nil, err
	}
	unknown, err := q.FinancialPreviewCommandUnowned(ctx, s.pool, sqlc.FinancialPreviewCommandUnownedParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), PageSize: api.FinancialAllocationMax + 1})
	if err != nil {
		return nil, err
	}
	if len(rows)+len(unknown) > api.FinancialAllocationMax {
		return nil, ErrFinancialAllocationLimit
	}
	out := make([]FinancialPreviewCommandRuntime, 0, len(rows)+len(unknown))
	for _, r := range rows {
		out = append(out, FinancialPreviewCommandRuntime{TaskID: pgUUIDString(r.TaskID), LeaseToken: pgUUIDString(r.LeaseToken), AccountID: account, AppID: app,
			DeploymentID: pgUUIDString(r.DeploymentID), NodeID: pgUUIDString(r.NodeID), EnvironmentID: pgUUIDString(r.EnvironmentID), Phase: r.Phase,
			RAMMB: int(r.RamMb), CPUMillicores: int(r.CpuMillicores), RecordedAt: r.RecordedAt.Time.UTC(), PrepareUntil: r.PrepareUntil.Time.UTC()})
	}
	for _, t := range unknown {
		out = append(out, FinancialPreviewCommandRuntime{AccountID: account, AppID: app, TaskID: pgUUIDString(t.ID), DeploymentID: pgUUIDString(t.DeploymentID), Phase: "unknown"})
	}
	return out, nil
}
