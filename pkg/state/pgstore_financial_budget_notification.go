package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialBudgetNotificationStore = (*PgStore)(nil)

func (s *PgStore) ListFinancialBudgetNotifyPolicies(ctx context.Context, limit int) ([]FinancialBudget, error) {
	if limit < 1 || limit > api.FinancialBudgetNotifyBatch {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialBudgetNotifyPolicies(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("financial budget notify policies: %w", err)
	}
	out := make([]FinancialBudget, 0, len(rows))
	for _, row := range rows {
		p, err := financialBudgetFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *PgStore) ClaimFinancialBudgetNotification(ctx context.Context, n FinancialBudgetNotification) (bool, error) {
	if !validFinancialBudgetNotification(n) {
		return false, ErrInvalidArgument
	}
	accountID, _ := parsePgUUID(n.AccountID)
	policyID, _ := parsePgUUID(n.PolicyID)
	inserted, err := sqlc.New().FinancialBudgetNotificationClaim(ctx, s.pool, sqlc.FinancialBudgetNotificationClaimParams{
		AccountID:           accountID,
		PolicyID:            policyID,
		PeriodStart:         pgtype.Timestamptz{Time: n.PeriodStart.UTC(), Valid: true},
		ThresholdMillicents: n.ThresholdMillicents,
		PolicyRevision:      n.PolicyRevision,
		KnownMillicents:     n.KnownMillicents,
		CoverageComplete:    n.CoverageComplete,
	})
	if err != nil {
		return false, financialBudgetStoreError(err)
	}
	return inserted == 1, nil
}

func (s *PgStore) ListPendingFinancialBudgetNotifications(ctx context.Context, limit int) ([]FinancialBudgetNotification, error) {
	if limit < 1 || limit > api.FinancialBudgetNotifyBatch {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().FinancialBudgetNotificationsPending(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("financial budget pending notifications: %w", err)
	}
	out := make([]FinancialBudgetNotification, 0, len(rows))
	for _, row := range rows {
		n := FinancialBudgetNotification{
			AccountID:           pgUUIDString(row.AccountID),
			PolicyID:            pgUUIDString(row.PolicyID),
			PeriodStart:         row.PeriodStart.Time.UTC(),
			ThresholdMillicents: row.ThresholdMillicents,
			PolicyRevision:      row.PolicyRevision,
			KnownMillicents:     row.KnownMillicents,
			CoverageComplete:    row.CoverageComplete,
			Status:              row.Status,
			Attempts:            int(row.Attempts),
			LastError:           row.LastError,
			ClaimedAt:           row.ClaimedAt.Time.UTC(),
		}
		if row.FinishedAt.Valid {
			at := row.FinishedAt.Time.UTC()
			n.FinishedAt = &at
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *PgStore) RecordFinancialBudgetNotificationOutcome(ctx context.Context, policyID string, periodStart time.Time, maxThreshold int64, outcome, lastError string) (int64, error) {
	if !validFinancialBudgetNotificationOutcome(policyID, periodStart, maxThreshold, outcome, lastError) {
		return 0, ErrInvalidArgument
	}
	id, _ := parsePgUUID(policyID)
	changed, err := sqlc.New().FinancialBudgetNotificationsRecord(ctx, s.pool, sqlc.FinancialBudgetNotificationsRecordParams{
		Outcome:                outcome,
		LastError:              lastError,
		PolicyID:               id,
		PeriodStart:            pgtype.Timestamptz{Time: periodStart.UTC(), Valid: true},
		MaxThresholdMillicents: maxThreshold,
	})
	if err != nil {
		return 0, fmt.Errorf("financial budget notification outcome: %w", err)
	}
	return changed, nil
}
