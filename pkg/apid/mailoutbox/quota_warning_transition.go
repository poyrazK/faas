package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// WarnQuotaWithNotice checks authoritative usage and commits the daily stamp,
// encrypted snapshot and follow-up receipt together. The command cannot choose
// usage, quota, recipient, body or issue time.
func (s *Service) WarnQuotaWithNotice(ctx context.Context, id string, day time.Time, plan api.Plan, requestID string) (api.InternalMailQuotaWarningResponse, error) {
	var result api.InternalMailQuotaWarningResponse
	if !s.quotaWarningEnabled() {
		return result, internalmail.ErrDisabled
	}
	if !validID(id) || !validID(requestID) || day.IsZero() || !day.Equal(day.UTC().Truncate(24*time.Hour)) || !quotaWarningPaidPlan(plan) {
		return result, internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return result, err
	}
	a, err := s.queries.LockQuotaWarningAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	// Read time after the account lock, including waits spanning midnight.
	today, err := s.queries.QuotaWarningDay(ctx, tx)
	if err != nil {
		return result, err
	}
	if !today.Time.Equal(day) || a.Plan != string(plan) || !quotaWarningAccountAvailable(a.Status) || a.DeletionRequestedAt.Valid {
		return result, nil
	}
	period, err := s.quotaWarningPeriod(ctx, tx, a, today)
	if err != nil || !period.IntentID.Valid {
		return result, err
	}
	if period.Namespace != s.namespace || period.ProducerID != s.producer || period.FromAddress != s.from {
		return result, internalmail.ErrConflict
	}
	result = quotaWarningResponse(period)
	lease, err := s.queries.ClaimQuotaWarningFollowup(ctx, tx, q.ClaimQuotaWarningFollowupParams{IntentID: period.IntentID, LeaseID: pgID(requestID), LeaseSeconds: int32(api.InternalMailQuotaWarningFollowupLease / time.Second)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Followup, result.LeaseID = true, uuidString(lease.Bytes)
	}
	if !s.quotaWarningEnabled() {
		return api.InternalMailQuotaWarningResponse{}, internalmail.ErrDisabled
	}
	current, err := s.queries.QuotaWarningDay(ctx, tx)
	if err != nil {
		return result, err
	}
	if !current.Time.Equal(day) {
		return api.InternalMailQuotaWarningResponse{}, internalmail.ErrConflict
	}
	return result, tx.Commit(ctx)
}

func (s *Service) quotaWarningPeriod(ctx context.Context, tx pgx.Tx, a q.LockQuotaWarningAccountRow, day pgtype.Timestamptz) (q.QuotaWarningPeriodRow, error) {
	period, err := s.queries.QuotaWarningPeriod(ctx, tx, q.QuotaWarningPeriodParams{AccountID: a.ID, WarningDay: day})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return period, err
	}
	if err == nil && (!a.LastQuotaWarningAt.Valid || !a.LastQuotaWarningAt.Time.Equal(day.Time) || period.QuotaRevision != a.QuotaMailRevision || period.Plan != a.Plan) {
		return q.QuotaWarningPeriodRow{}, nil
	}
	if errors.Is(err, pgx.ErrNoRows) && a.LastQuotaWarningAt.Valid && !a.LastQuotaWarningAt.Time.Before(day.Time) {
		return q.QuotaWarningPeriodRow{}, nil // no legacy backfill or stamp rewind
	}
	recovery, recoveryErr := s.recovery(ctx, tx)
	if recoveryErr != nil {
		return period, recoveryErr
	}
	if recovery.Held || err == nil && period.RecoveryID != recovery.RecoveryID {
		return period, internalmail.ErrRecoveryHeld
	}
	if err == nil {
		return period, nil
	}
	month := time.Date(day.Time.UTC().Year(), day.Time.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	used, err := s.queries.QuotaWarningUsage(ctx, tx, q.QuotaWarningUsageParams{AccountID: a.ID, Minute: pgtype.Timestamptz{Time: month, Valid: true}, Minute_2: pgtype.Timestamptz{Time: month.AddDate(0, 1, 0), Valid: true}})
	if err != nil {
		return period, err
	}
	quota := api.Plan(a.Plan).PlanIncludedGBHours()
	if used < 0 || api.InternalMailQuotaWarningUsageGBHours(used) < float64(quota) {
		return q.QuotaWarningPeriodRow{}, nil
	}
	if err := s.queries.CancelOlderQuotaWarningIntents(ctx, tx, q.CancelOlderQuotaWarningIntentsParams{AccountID: a.ID, QuotaWarningDay: day}); err != nil {
		return period, err
	}
	stamped, err := s.queries.StampQuotaWarning(ctx, tx, q.StampQuotaWarningParams{ID: a.ID, LastQuotaWarningAt: day})
	if err != nil {
		return period, err
	}
	payload := quotaWarningPayload{Day: day.Time, ObservedAt: stamped.ObservedAt.Time, Plan: api.Plan(a.Plan), UsedMBSeconds: used, QuotaGB: quota, UnsubscribeURL: s.quotaWarningUnsubscribe}
	intent, err := s.saveQuotaWarning(ctx, tx, a.ID, a.Email, recovery.RecoveryID, payload, stamped.QuotaMailRevision)
	if err != nil {
		return period, err
	}
	if err := s.queries.RecordQuotaWarning(ctx, tx, q.RecordQuotaWarningParams{AccountID: a.ID, WarningDay: day, IntentID: intent.ID, ObservedAt: stamped.ObservedAt, Plan: a.Plan, UsedMbSeconds: used, QuotaGb: int32(quota), QuotaRevision: stamped.QuotaMailRevision}); err != nil {
		return period, err
	}
	return s.queries.QuotaWarningPeriod(ctx, tx, q.QuotaWarningPeriodParams{AccountID: a.ID, WarningDay: day})
}

func quotaWarningResponse(p q.QuotaWarningPeriodRow) api.InternalMailQuotaWarningResponse {
	return api.InternalMailQuotaWarningResponse{IntentID: uuidString(p.IntentID.Bytes), Day: p.WarningDay.Time, ObservedAt: p.ObservedAt.Time, Plan: api.Plan(p.Plan), UsedMBSeconds: p.UsedMbSeconds, QuotaGB: int(p.QuotaGb)}
}

// CompleteQuotaWarningFollowup only records the exact notification lease proof.
// It neither acknowledges SMTP custody nor creates another mail intent.
func (s *Service) CompleteQuotaWarningFollowup(ctx context.Context, id, intentID, leaseID string) error {
	if !s.quotaWarningEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(id) || !validID(intentID) || !validID(leaseID) {
		return internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if _, err := s.queries.LockQuotaWarningAccount(ctx, tx, pgID(id)); err != nil {
		return err
	}
	n, err := s.queries.CompleteQuotaWarningFollowup(ctx, tx, q.CompleteQuotaWarningFollowupParams{Namespace: s.namespace, ProducerID: s.producer, AccountID: pgID(id), IntentID: pgID(intentID), LeaseID: pgID(leaseID)})
	if err != nil {
		return err
	}
	if n != 1 {
		return internalmail.ErrConflict
	}
	if !s.quotaWarningEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
