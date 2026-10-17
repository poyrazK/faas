package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// SuspendWithNotice owns the private meterd command. The database clock and
// locked current overdue period decide eligibility, never the caller's clock.
// Its receipt also lets meterd finish the existing follow-up after a lost reply.
func (s *Service) SuspendWithNotice(ctx context.Context, id string, pastDueAt time.Time, requestID string) (api.InternalMailSuspensionResponse, error) {
	var result api.InternalMailSuspensionResponse
	if !s.accountSuspendedEnabled() {
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
	a, err := s.queries.LockAccountSuspensionAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !a.PastDueAt.Valid || !a.PastDueAt.Time.Equal(pastDueAt) || a.DeletionRequestedAt.Valid ||
		(a.Status != "past_due" && a.Status != "suspended") || a.CheckedAt.Time.Before(pastDueAt.Add(api.InternalMailDunningSuspendAfter)) {
		return result, nil
	}
	period, err := s.suspensionPeriod(ctx, tx, a)
	if err != nil || !period.IntentID.Valid {
		return result, err
	}
	if period.Namespace != s.namespace || period.ProducerID != s.producer || period.FromAddress != s.from {
		return result, internalmail.ErrConflict
	}
	result.IntentID, result.SuspendedAt = uuidString(period.IntentID.Bytes), period.SuspendedAt.Time
	lease, err := s.queries.ClaimSuspensionFollowup(ctx, tx, q.ClaimSuspensionFollowupParams{IntentID: period.IntentID, LeaseID: pgID(requestID), LeaseSeconds: int32(api.InternalMailSuspensionFollowupLease / time.Second)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	if err == nil {
		result.Followup, result.LeaseID = true, uuidString(lease.Bytes)
	}
	if !s.accountSuspendedEnabled() {
		return api.InternalMailSuspensionResponse{}, internalmail.ErrDisabled
	}
	return result, tx.Commit(ctx)
}

func (s *Service) suspensionPeriod(ctx context.Context, tx pgx.Tx, a q.LockAccountSuspensionAccountRow) (q.AccountSuspensionPeriodRow, error) {
	period, err := s.queries.AccountSuspensionPeriod(ctx, tx, q.AccountSuspensionPeriodParams{AccountID: a.ID, PastDueAt: a.PastDueAt})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return period, err
	}
	if a.Status == "past_due" && err == nil {
		// An old command must not undo an operator's restoration within the
		// same overdue period. A genuinely new period has a new anchor.
		return q.AccountSuspensionPeriodRow{}, nil
	}
	if a.Status == "suspended" && errors.Is(err, pgx.ErrNoRows) {
		return q.AccountSuspensionPeriodRow{}, nil // no historical backfill
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
	r, err := s.queries.SuspendAccountWithNotice(ctx, tx, a.ID)
	if err != nil {
		return period, err
	}
	if err := s.queries.CancelPaymentFailedIntents(ctx, tx, a.ID); err != nil {
		return period, err
	}
	intent, err := s.saveAccountSuspended(ctx, tx, a.ID, a.Email, recovery.RecoveryID, r.SuspendedAt.Time, a.PastDueAt.Time, r.BillingMailRevision)
	if err != nil {
		return period, err
	}
	if err := s.queries.RecordAccountSuspension(ctx, tx, q.RecordAccountSuspensionParams{AccountID: a.ID, PastDueAt: a.PastDueAt, IntentID: intent.ID, SuspendedAt: r.SuspendedAt, BillingRevision: r.BillingMailRevision}); err != nil {
		return period, err
	}
	return s.queries.AccountSuspensionPeriod(ctx, tx, q.AccountSuspensionPeriodParams{AccountID: a.ID, PastDueAt: a.PastDueAt})
}

// CompleteSuspensionFollowup acknowledges only the exact lease returned to
// meterd. Follow-up is at least once; it never acknowledges or resends email.
func (s *Service) CompleteSuspensionFollowup(ctx context.Context, id, intentID, leaseID string) error {
	if !s.accountSuspendedEnabled() {
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
	if _, err := s.queries.LockAccountSuspensionAccount(ctx, tx, pgID(id)); err != nil {
		return err
	}
	n, err := s.queries.CompleteSuspensionFollowup(ctx, tx, q.CompleteSuspensionFollowupParams{Namespace: s.namespace, ProducerID: s.producer, AccountID: pgID(id), IntentID: pgID(intentID), LeaseID: pgID(leaseID)})
	if err != nil {
		return err
	}
	if n != 1 {
		return internalmail.ErrConflict
	}
	if !s.accountSuspendedEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
