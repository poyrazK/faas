package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialBudgetStopStore = (*PgStore)(nil)

func insertFinancialBudgetStop(ctx context.Context, db sqlc.DBTX, s FinancialBudgetStop) error {
	payload, err := json.Marshal(s.BudgetStop)
	if err != nil {
		return err
	}
	return sqlc.New().FinancialBudgetStopInsert(ctx, db, sqlc.FinancialBudgetStopInsertParams{
		ID: mustPgUUID(s.ID), AccountID: mustPgUUID(s.AccountID), PolicyID: mustPgUUID(s.PolicyID), Revision: s.Revision,
		PeriodStart: financialTimestamp(s.PeriodStart), PeriodEnd: financialTimestamp(s.PeriodEnd),
		RequestedAt: financialTimestamp(s.RequestedAt), DrainDeadline: financialTimestamp(s.DrainDeadline), ResumeRule: s.ResumeRule, Payload: payload,
	})
}

func (s *PgStore) ActiveFinancialBudgetStops(ctx context.Context, account string, now time.Time) ([]FinancialBudgetStop, error) {
	if uuid.Validate(account) != nil || now.IsZero() {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	rows, err := q.FinancialBudgetStopActive(ctx, s.pool, sqlc.FinancialBudgetStopActiveParams{AccountID: mustPgUUID(account), NowAt: financialTimestamp(now), PageSize: api.FinancialBudgetsPerAccount + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.FinancialBudgetsPerAccount {
		return nil, ErrFinancialBudgetLimit
	}
	out := make([]FinancialBudgetStop, 0, len(rows))
	for _, row := range rows {
		stop := FinancialBudgetStop{AccountID: account}
		if err := json.Unmarshal(row.Payload, &stop.BudgetStop); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(row.Spec, &stop.Spec); err != nil {
			return nil, err
		}
		out = append(out, stop)
	}
	return out, nil
}

func (s *PgStore) FinancialBudgetStopTargets(ctx context.Context, account, id string) ([]financial.BudgetStopTarget, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialBudgetStopTargetList(ctx, s.pool, sqlc.FinancialBudgetStopTargetListParams{AccountID: mustPgUUID(account), StopID: mustPgUUID(id), PageSize: api.FinancialAllocationMax + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.FinancialAllocationMax {
		return nil, ErrFinancialAllocationLimit
	}
	out := make([]financial.BudgetStopTarget, 0, len(rows))
	for _, target := range rows {
		out = append(out, financial.BudgetStopTarget{AppID: pgUUIDString(target.AppID), EnvironmentID: target.EnvironmentID, State: target.State, ObservedAt: target.ObservedAt.Time.UTC(), ResidentCount: int(target.ResidentCount), LastError: target.LastError})
	}
	return out, nil
}

func (s *PgStore) RecordFinancialBudgetStopTarget(ctx context.Context, account, id string, target financial.BudgetStopTarget) error {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil || !validFinancialBudgetStopTarget(target) {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().FinancialBudgetStopTargetPut(ctx, s.pool, sqlc.FinancialBudgetStopTargetPutParams{
		AccountID: mustPgUUID(account), StopID: mustPgUUID(id), AppID: mustPgUUID(target.AppID), EnvironmentID: target.EnvironmentID,
		State: target.State, ObservedAt: financialTimestamp(target.ObservedAt), ResidentCount: int32(target.ResidentCount), LastError: target.LastError,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}
