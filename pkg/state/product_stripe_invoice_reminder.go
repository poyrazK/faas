package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) BeginProductStripeInvoiceReminder(ctx context.Context, account, export, token, reviewHash string, now time.Time) error {
	if len(reviewHash) != 64 || now.IsZero() {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().BeginProductStripeInvoiceReminder(ctx, s.pool, sqlc.BeginProductStripeInvoiceReminderParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export), Token: mustPgUUID(token), ReviewHash: reviewHash, StartedAt: pgTime(now),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (s *PgStore) CompleteProductStripeInvoiceReminder(ctx context.Context, account, export, token, reviewHash string, now time.Time) error {
	if now.IsZero() {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().CompleteProductStripeInvoiceReminder(ctx, s.pool, sqlc.CompleteProductStripeInvoiceReminderParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export), Token: mustPgUUID(token), ReviewHash: reviewHash, SentAt: pgTime(now),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (m *MemStore) BeginProductStripeInvoiceReminder(_ context.Context, account, export, token, reviewHash string, now time.Time) error {
	if len(reviewHash) != 64 || now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	if !e.canRecordStripeReminder(account, token) || e.stripeSend.Reminder.ReviewHash != "" {
		return ErrConflict
	}
	e.stripeSend.Reminder = ProductStripeInvoiceReminder{ReviewHash: reviewHash, StartedAt: now}
	m.statementInvoiceExports[export] = e
	return nil
}

func (m *MemStore) CompleteProductStripeInvoiceReminder(_ context.Context, account, export, token, reviewHash string, now time.Time) error {
	if now.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	reminder := e.stripeSend.Reminder
	if !e.canRecordStripeReminder(account, token) || reminder.StartedAt.IsZero() || reminder.ReviewHash != reviewHash || !reminder.SentAt.IsZero() {
		return ErrConflict
	}
	if now.Before(reminder.StartedAt) {
		now = reminder.StartedAt
	}
	e.stripeSend.Reminder.SentAt = now
	m.statementInvoiceExports[export] = e
	return nil
}

func (e StatementInvoiceExport) canRecordStripeReminder(account, token string) bool {
	return e.AccountID == account && e.claimToken == token && e.claimedUntil.After(time.Now()) && e.stripeSend.Stage == StripeInvoiceSent
}
