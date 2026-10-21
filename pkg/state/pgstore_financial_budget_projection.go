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

var _ FinancialBudgetProjectionStore = (*PgStore)(nil)

// insertFinancialBudgetProjection records a projected-overrun alert once per
// revision and month, with a delivery for every bound channel.
func insertFinancialBudgetProjection(ctx context.Context, tx pgx.Tx, a FinancialBudgetAlertDelivery) error {
	payload, err := json.Marshal(a.BudgetAlert)
	if err != nil {
		return err
	}
	id, _ := parsePgUUID(a.ID)
	aid, _ := parsePgUUID(a.AccountID)
	pid, _ := parsePgUUID(a.PolicyID)
	q := sqlc.New()
	if err := q.FinancialBudgetProjectionInsert(ctx, tx, sqlc.FinancialBudgetProjectionInsertParams{ID: id, AccountID: aid, PolicyID: pid, Revision: a.PolicyRevision, PeriodStart: financialTimestamp(a.Evaluation.PeriodStart), LimitMillicents: a.ThresholdMillicents, Payload: payload, NextAttemptAt: financialTimestamp(a.Evaluation.EvaluatedAt)}); err != nil {
		return err
	}
	return q.FinancialBudgetProjectionChannelsEnqueue(ctx, tx, sqlc.FinancialBudgetProjectionChannelsEnqueueParams{NowAt: financialTimestamp(a.Evaluation.EvaluatedAt), AlertID: id})
}

func financialBudgetProjectionFromRow(row sqlc.FinancialBudgetProjectionAlert) (FinancialBudgetAlertDelivery, error) {
	out := FinancialBudgetAlertDelivery{AccountID: pgUUIDString(row.AccountID), PolicyID: pgUUIDString(row.PolicyID), LeaseToken: pgUUIDString(row.LeaseToken)}
	if err := json.Unmarshal(row.Payload, &out.BudgetAlert); err != nil {
		return out, err
	}
	out.ID, out.Kind, out.PolicyRevision = pgUUIDString(row.ID), financial.BudgetAlertProjection, row.Revision
	out.ThresholdMillicents = row.LimitMillicents
	out.State, out.Attempts, out.LastError = row.State, int(row.Attempts), row.LastError
	out.NextAttemptAt = row.NextAttemptAt.Time.UTC()
	if row.DeliveredAt.Valid {
		at := row.DeliveredAt.Time.UTC()
		out.DeliveredAt = &at
	}
	return out, nil
}

func (s *PgStore) ClaimFinancialBudgetProjection(ctx context.Context, now time.Time) (FinancialBudgetAlertDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertDelivery{}, ErrInvalidArgument
	}
	token, _ := parsePgUUID(uuid.NewString())
	row, err := sqlc.New().FinancialBudgetProjectionClaim(ctx, s.pool, sqlc.FinancialBudgetProjectionClaimParams{NowAt: financialTimestamp(now), LeaseToken: token, LeaseUntil: financialTimestamp(now.Add(api.FinancialBudgetAlertLease))})
	if err != nil {
		return FinancialBudgetAlertDelivery{}, financialBudgetStoreError(err)
	}
	a, err := financialBudgetProjectionFromRow(row)
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

func (s *PgStore) SettleFinancialBudgetProjection(ctx context.Context, id, token string, now time.Time, delivered bool) error {
	if uuid.Validate(id) != nil || uuid.Validate(token) != nil || now.IsZero() {
		return ErrInvalidArgument
	}
	pid, _ := parsePgUUID(id)
	lease, _ := parsePgUUID(token)
	q := sqlc.New()
	a, err := q.FinancialBudgetProjectionDeliveryGet(ctx, s.pool, sqlc.FinancialBudgetProjectionDeliveryGetParams{ID: pid, LeaseToken: lease})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return financialBudgetStoreError(err)
	}
	p := sqlc.FinancialBudgetProjectionSettleParams{ID: pid, LeaseToken: lease, NowAt: financialTimestamp(now), State: "retrying", LastError: "mail_delivery_failed", NextAttemptAt: financialTimestamp(now.Add(financialBudgetAlertRetry(int(a.Attempts))))}
	if delivered {
		p.State, p.LastError, p.DeliveredAt = "delivered", "", pgtype.Timestamptz{Time: now.UTC(), Valid: true}
		p.NextAttemptAt = financialTimestamp(now)
	}
	_, err = q.FinancialBudgetProjectionSettle(ctx, s.pool, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}
