package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) SetFinancialBudgetChannels(ctx context.Context, account, policy string, channelIDs []string) error {
	if !validFinancialBudgetChannelIDs(account, policy, channelIDs) {
		return ErrInvalidArgument
	}
	aid, _ := parsePgUUID(account)
	pid, _ := parsePgUUID(policy)
	ids := make([]pgtype.UUID, 0, len(channelIDs))
	for _, id := range channelIDs {
		cid, _ := parsePgUUID(id)
		ids = append(ids, cid)
	}
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
	if row.DeletedAt.Valid {
		return ErrNotFound
	}
	if err := q.FinancialBudgetChannelsDelete(ctx, tx, sqlc.FinancialBudgetChannelsDeleteParams{AccountID: aid, PolicyID: pid}); err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := q.FinancialBudgetChannelsInsert(ctx, tx, sqlc.FinancialBudgetChannelsInsertParams{ChannelIds: ids, AccountID: aid, PolicyID: pid}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListFinancialBudgetChannels(ctx context.Context, account, policy string) ([]NotificationChannel, error) {
	if !validFinancialBudgetChannelIDs(account, policy, nil) {
		return nil, ErrInvalidArgument
	}
	aid, _ := parsePgUUID(account)
	pid, _ := parsePgUUID(policy)
	rows, err := sqlc.New().FinancialBudgetChannelsList(ctx, s.pool, sqlc.FinancialBudgetChannelsListParams{AccountID: aid, PolicyID: pid})
	if err != nil {
		return nil, err
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelFromRow(row))
	}
	return out, nil
}

// enqueueFinancialBudgetAlertChannels adds a delivery for every channel bound
// to the alert's policy, inside the evaluation's transaction.
func enqueueFinancialBudgetAlertChannels(ctx context.Context, tx pgx.Tx, alertID pgtype.UUID, now time.Time) error {
	return sqlc.New().FinancialBudgetAlertChannelsEnqueue(ctx, tx, sqlc.FinancialBudgetAlertChannelsEnqueueParams{NowAt: financialTimestamp(now), AlertID: alertID})
}

func (s *PgStore) ClaimFinancialBudgetAlertChannel(ctx context.Context, now time.Time) (FinancialBudgetAlertChannelDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertChannelDelivery{}, ErrInvalidArgument
	}
	token, _ := parsePgUUID(uuid.NewString())
	q := sqlc.New()
	row, err := q.FinancialBudgetAlertChannelClaim(ctx, s.pool, sqlc.FinancialBudgetAlertChannelClaimParams{NowAt: financialTimestamp(now), LeaseToken: token, LeaseUntil: financialTimestamp(now.Add(api.FinancialBudgetAlertLease))})
	if err != nil {
		return FinancialBudgetAlertChannelDelivery{}, financialBudgetStoreError(err)
	}
	out := financialBudgetAlertChannelFromRow(row)
	alert, err := q.FinancialBudgetAlertGetByID(ctx, s.pool, row.AlertID)
	if err != nil {
		return out, financialBudgetStoreError(err)
	}
	if out.Alert, err = financialBudgetAlertFromRow(alert); err != nil {
		return out, err
	}
	// The claimed lease, not the email alert's, fences this delivery.
	out.Alert.LeaseToken = ""
	p, err := s.GetFinancialBudget(ctx, out.Alert.AccountID, out.Alert.PolicyID)
	if err != nil {
		return out, err
	}
	if p.Revision != out.Alert.PolicyRevision || p.DeletedAt != nil || !p.Spec.Enabled || !financial.Monitored(p.Spec) {
		return out, ErrConflict
	}
	out.Alert.Spec = p.Spec
	if out.Channel, err = s.GetNotificationChannel(ctx, out.Alert.AccountID, pgUUIDString(row.ChannelID)); err != nil {
		return out, err
	}
	return out, nil
}

func financialBudgetAlertChannelFromRow(row sqlc.FinancialBudgetAlertChannelDelivery) FinancialBudgetAlertChannelDelivery {
	out := FinancialBudgetAlertChannelDelivery{State: row.State, Attempts: int(row.Attempts), LeaseToken: pgUUIDString(row.LeaseToken), NextAttemptAt: row.NextAttemptAt.Time.UTC(), LastError: row.LastError}
	out.Alert.ID, out.Channel.ID = pgUUIDString(row.AlertID), pgUUIDString(row.ChannelID)
	if row.DeliveredAt.Valid {
		at := row.DeliveredAt.Time.UTC()
		out.DeliveredAt = &at
	}
	return out
}

func (s *PgStore) SettleFinancialBudgetAlertChannel(ctx context.Context, alertID, channelID, token string, now time.Time, outcome string) error {
	if uuid.Validate(alertID) != nil || uuid.Validate(channelID) != nil || uuid.Validate(token) != nil || now.IsZero() || !validFinancialBudgetChannelOutcome(outcome) {
		return ErrInvalidArgument
	}
	aid, _ := parsePgUUID(alertID)
	cid, _ := parsePgUUID(channelID)
	lease, _ := parsePgUUID(token)
	q := sqlc.New()
	row, err := q.FinancialBudgetAlertChannelGet(ctx, s.pool, sqlc.FinancialBudgetAlertChannelGetParams{AlertID: aid, ChannelID: cid})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	state, lastError, next, delivered := financialBudgetChannelSettlement(int(row.Attempts), now, outcome)
	p := sqlc.FinancialBudgetAlertChannelSettleParams{State: state, LastError: lastError, NextAttemptAt: financialTimestamp(next), AlertID: aid, ChannelID: cid, LeaseToken: lease, NowAt: financialTimestamp(now)}
	if delivered != nil {
		p.DeliveredAt = pgtype.Timestamptz{Time: *delivered, Valid: true}
	}
	_, err = q.FinancialBudgetAlertChannelSettle(ctx, s.pool, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}
