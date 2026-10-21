package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialBudgetMonitorStore = (*PgStore)(nil)

func (s *PgStore) RecordFinancialBudgetEvaluation(ctx context.Context, account, id string, revision int64, e financial.BudgetEvaluation) error {
	if !validFinancialBudgetMutation(account, id, "meterd", revision) || !validFinancialBudgetEvaluation(e) {
		return ErrInvalidArgument
	}
	aid, _ := parsePgUUID(account)
	pid, _ := parsePgUUID(id)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.FinancialBudgetAccountLock(ctx, tx, aid); err != nil {
		return financialBudgetStoreError(err)
	}
	row, err := q.FinancialBudgetGet(ctx, tx, sqlc.FinancialBudgetGetParams{AccountID: aid, ID: pid})
	if err != nil {
		return financialBudgetStoreError(err)
	}
	p, err := financialBudgetFromRow(row)
	if err != nil {
		return err
	}
	if p.Revision != revision || p.DeletedAt != nil || !p.Spec.Enabled || !financial.Monitored(p.Spec) {
		return ErrConflict
	}
	e = cloneFinancialBudgetEvaluation(e)
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = q.FinancialBudgetEvaluationPut(ctx, tx, sqlc.FinancialBudgetEvaluationPutParams{AccountID: aid, PolicyID: pid, Revision: revision, PeriodStart: financialTimestamp(e.PeriodStart), EvaluatedAt: financialTimestamp(e.EvaluatedAt), EvidenceThroughID: e.EvidenceThroughID, Observation: data, Unavailable: e.KnownMillicents == nil})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	for _, alert := range financialBudgetAlerts(p, e) {
		payload, err := json.Marshal(alert.BudgetAlert)
		if err != nil {
			return err
		}
		alertID, _ := parsePgUUID(alert.ID)
		if err := q.FinancialBudgetAlertInsert(ctx, tx, sqlc.FinancialBudgetAlertInsertParams{ID: alertID, AccountID: aid, PolicyID: pid, Revision: revision, PeriodStart: financialTimestamp(e.PeriodStart), ThresholdMillicents: alert.ThresholdMillicents, Payload: payload, NextAttemptAt: financialTimestamp(e.EvaluatedAt)}); err != nil {
			return err
		}
		if err := enqueueFinancialBudgetAlertChannels(ctx, tx, alertID, e.EvaluatedAt); err != nil {
			return err
		}
	}
	if projection, ok := financialBudgetProjectionAlert(p, e); ok {
		if err := insertFinancialBudgetProjection(ctx, tx, projection); err != nil {
			return err
		}
	}
	if stop, crossed := financialBudgetStop(p, e); crossed {
		if err := insertFinancialBudgetStop(ctx, tx, stop); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) FinancialBudgetRuntime(ctx context.Context, account, id string, revision int64, month time.Time) (financial.BudgetRuntime, error) {
	if !validFinancialBudgetMutation(account, id, "reader", revision) || month.IsZero() || month.UTC().Day() != 1 || !month.Equal(month.UTC().Truncate(24*time.Hour)) {
		return financial.BudgetRuntime{}, ErrInvalidArgument
	}
	p, err := s.GetFinancialBudget(ctx, account, id)
	if err != nil {
		return financial.BudgetRuntime{}, err
	}
	if p.Revision != revision {
		return financial.BudgetRuntime{}, ErrConflict
	}
	aid, _ := parsePgUUID(account)
	pid, _ := parsePgUUID(id)
	q := sqlc.New()
	out := financial.BudgetRuntime{Alerts: []financial.BudgetAlert{}}
	data, err := q.FinancialBudgetEvaluationGet(ctx, s.pool, sqlc.FinancialBudgetEvaluationGetParams{AccountID: aid, PolicyID: pid, Revision: p.Revision, PeriodStart: financialTimestamp(month)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.Evaluation = &financial.BudgetEvaluation{}
		if err := json.Unmarshal(data, out.Evaluation); err != nil {
			return out, err
		}
	}
	rows, err := q.FinancialBudgetAlertList(ctx, s.pool, sqlc.FinancialBudgetAlertListParams{AccountID: aid, PolicyID: pid, Revision: p.Revision, PeriodStart: financialTimestamp(month), PageSize: api.FinancialBudgetThresholds + 1})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		a, err := financialBudgetAlertFromRow(row)
		if err != nil {
			return out, err
		}
		out.Alerts = append(out.Alerts, a.BudgetAlert)
	}
	projection, err := q.FinancialBudgetProjectionGet(ctx, s.pool, sqlc.FinancialBudgetProjectionGetParams{AccountID: aid, PolicyID: pid, Revision: p.Revision, PeriodStart: financialTimestamp(month)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	a, err := financialBudgetProjectionFromRow(projection)
	if err != nil {
		return out, err
	}
	out.Projection = &a.BudgetAlert
	return out, nil
}

func financialBudgetAlertFromRow(row sqlc.FinancialBudgetAlert) (FinancialBudgetAlertDelivery, error) {
	out := FinancialBudgetAlertDelivery{AccountID: pgUUIDString(row.AccountID), PolicyID: pgUUIDString(row.PolicyID), LeaseToken: pgUUIDString(row.LeaseToken)}
	if err := json.Unmarshal(row.Payload, &out.BudgetAlert); err != nil {
		return out, err
	}
	out.ID, out.PolicyRevision = pgUUIDString(row.ID), row.Revision
	out.State, out.Attempts, out.LastError = row.State, int(row.Attempts), row.LastError
	out.NextAttemptAt = row.NextAttemptAt.Time.UTC()
	if row.DeliveredAt.Valid {
		at := row.DeliveredAt.Time.UTC()
		out.DeliveredAt = &at
	}
	return out, nil
}

func (s *PgStore) ClaimFinancialBudgetAlert(ctx context.Context, now time.Time) (FinancialBudgetAlertDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertDelivery{}, ErrInvalidArgument
	}
	token, _ := parsePgUUID(uuid.NewString())
	row, err := sqlc.New().FinancialBudgetAlertClaim(ctx, s.pool, sqlc.FinancialBudgetAlertClaimParams{NowAt: financialTimestamp(now), LeaseToken: token, LeaseUntil: financialTimestamp(now.Add(api.FinancialBudgetAlertLease))})
	if err != nil {
		return FinancialBudgetAlertDelivery{}, financialBudgetStoreError(err)
	}
	a, err := financialBudgetAlertFromRow(row)
	if err != nil {
		return a, err
	}
	p, err := s.GetFinancialBudget(ctx, a.AccountID, a.PolicyID)
	if err != nil {
		return a, err
	}
	if p.Revision != a.PolicyRevision || p.DeletedAt != nil || !p.Spec.Enabled || !financial.Monitored(p.Spec) {
		return a, ErrConflict
	}
	a.Spec = p.Spec
	return a, nil
}

func (s *PgStore) SettleFinancialBudgetAlert(ctx context.Context, id, token string, now time.Time, delivered bool) error {
	aid, err := parsePgUUID(id)
	if err != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	lease, err := parsePgUUID(token)
	if err != nil {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	a, err := q.FinancialBudgetAlertDeliveryGet(ctx, s.pool, sqlc.FinancialBudgetAlertDeliveryGetParams{ID: aid, LeaseToken: lease})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return financialBudgetStoreError(err)
	}
	p := sqlc.FinancialBudgetAlertSettleParams{ID: aid, LeaseToken: lease, NowAt: financialTimestamp(now), State: "retrying", LastError: "mail_delivery_failed", NextAttemptAt: financialTimestamp(now.Add(financialBudgetAlertRetry(int(a.Attempts))))}
	if delivered {
		p.State, p.LastError, p.DeliveredAt = "delivered", "", pgtype.Timestamptz{Time: now.UTC(), Valid: true}
		p.NextAttemptAt = financialTimestamp(now)
	}
	_, err = q.FinancialBudgetAlertSettle(ctx, s.pool, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}
