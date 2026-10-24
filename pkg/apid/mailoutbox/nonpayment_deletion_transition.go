package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

// ScheduleNonpaymentDeletionWithNotice commits the deletion-pending transition,
// encrypted notice and meterd follow-up receipt together. The locked overdue
// period and database clock decide eligibility; retries keep the saved deadline.
func (s *Service) ScheduleNonpaymentDeletionWithNotice(ctx context.Context, id string, pastDueAt time.Time, requestID string) (api.InternalMailNonpaymentDeletionResponse, error) {
	var result api.InternalMailNonpaymentDeletionResponse
	if !s.nonpaymentDeletionEnabled() {
		return result, internalmail.ErrDisabled
	}
	if !validID(id) || !validID(requestID) || pastDueAt.IsZero() {
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
	a, err := s.queries.LockNonpaymentDeletionAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !a.PastDueAt.Valid || !a.PastDueAt.Time.Equal(pastDueAt) ||
		(a.Status != "suspended" && a.Status != "deleted_pending") ||
		(a.Status == "suspended" && a.DeletionRequestedAt.Valid) ||
		(a.Status == "deleted_pending" && !a.DeletionRequestedAt.Valid) ||
		a.CheckedAt.Time.Before(pastDueAt.Add(api.InternalMailDunningDeleteAfter)) {
		return result, nil
	}
	period, err := s.nonpaymentDeletionPeriod(ctx, tx, a)
	if err != nil || !period.IntentID.Valid {
		return result, err
	}
	if period.Namespace != s.namespace || period.ProducerID != s.producer || period.FromAddress != s.from {
		return result, internalmail.ErrConflict
	}
	result.IntentID, result.DeletionRequestedAt = uuidString(period.IntentID.Bytes), period.DeletionRequestedAt.Time
	lease, err := s.queries.ClaimNonpaymentDeletionFollowup(ctx, tx, q.ClaimNonpaymentDeletionFollowupParams{IntentID: period.IntentID, LeaseID: pgID(requestID), LeaseSeconds: int32(api.InternalMailNonpaymentDeletionFollowupLease / time.Second)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Followup, result.LeaseID = true, uuidString(lease.Bytes)
	}
	if !s.nonpaymentDeletionEnabled() {
		return api.InternalMailNonpaymentDeletionResponse{}, internalmail.ErrDisabled
	}
	return result, tx.Commit(ctx)
}

func (s *Service) nonpaymentDeletionPeriod(ctx context.Context, tx pgx.Tx, a q.LockNonpaymentDeletionAccountRow) (q.NonpaymentDeletionPeriodRow, error) {
	period, err := s.queries.NonpaymentDeletionPeriod(ctx, tx, q.NonpaymentDeletionPeriodParams{AccountID: a.ID, PastDueAt: a.PastDueAt})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return period, err
	}
	if a.Status == "suspended" && err == nil {
		return q.NonpaymentDeletionPeriodRow{}, nil // an old command cannot undo restoration
	}
	if a.Status == "deleted_pending" && (errors.Is(err, pgx.ErrNoRows) ||
		period.BillingRevision != a.BillingMailRevision || !period.DeletionRequestedAt.Time.Equal(a.DeletionRequestedAt.Time)) {
		return q.NonpaymentDeletionPeriodRow{}, nil // no historical backfill or lifecycle ABA
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
	r, err := s.queries.ScheduleNonpaymentDeletionAccount(ctx, tx, a.ID)
	if err != nil {
		// A deletion guard (live managed PostgreSQL databases, shared app
		// ownership) holds until someone resolves it; report it as such so
		// meterd's follow-up does not read it as an outage.
		return period, state.AccountDeletionGuardError(err)
	}
	if err := s.queries.CancelPreDeletionDunningIntents(ctx, tx, a.ID); err != nil {
		return period, err
	}
	intent, err := s.saveNonpaymentDeletion(ctx, tx, a.ID, a.Email, recovery.RecoveryID, r.DeletionRequestedAt.Time, a.PastDueAt.Time, r.BillingMailRevision)
	if err != nil {
		return period, err
	}
	if err := s.queries.RecordNonpaymentDeletion(ctx, tx, q.RecordNonpaymentDeletionParams{AccountID: a.ID, PastDueAt: a.PastDueAt, IntentID: intent.ID, DeletionRequestedAt: r.DeletionRequestedAt, BillingRevision: r.BillingMailRevision}); err != nil {
		return period, err
	}
	return s.queries.NonpaymentDeletionPeriod(ctx, tx, q.NonpaymentDeletionPeriodParams{AccountID: a.ID, PastDueAt: a.PastDueAt})
}

// CompleteNonpaymentDeletionFollowup acknowledges the exact saved meterd lease.
// Notifications are at least once across lost acknowledgements; email is separate.
func (s *Service) CompleteNonpaymentDeletionFollowup(ctx context.Context, id, intentID, leaseID string) error {
	if !s.nonpaymentDeletionEnabled() {
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
	if _, err := s.queries.LockNonpaymentDeletionAccount(ctx, tx, pgID(id)); err != nil {
		return err
	}
	n, err := s.queries.CompleteNonpaymentDeletionFollowup(ctx, tx, q.CompleteNonpaymentDeletionFollowupParams{Namespace: s.namespace, ProducerID: s.producer, AccountID: pgID(id), IntentID: pgID(intentID), LeaseID: pgID(leaseID)})
	if err != nil {
		return err
	}
	if n != 1 {
		return internalmail.ErrConflict
	}
	if !s.nonpaymentDeletionEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
