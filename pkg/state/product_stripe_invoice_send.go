package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/lazyregexp"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	StripeInvoicePreparing  = "preparing"
	StripeInvoiceFinalizing = "finalizing"
	StripeInvoiceSending    = "sending"
	StripeInvoiceSent       = "sent"
)

// ProductStripeInvoiceSend records a single reviewed send for an existing export.
// The export ID serves as the stable provider idempotency namespace.
type ProductStripeInvoiceSend struct {
	ReviewHash, TermsHash string
	DueAt                 time.Time
	Stage                 string
	SendStartedAt         time.Time
	SendEventID           string
	SendEventAt           time.Time
	Reminder              ProductStripeInvoiceReminder
}

type ProductStripeInvoiceReminder struct {
	ReviewHash        string
	StartedAt, SentAt time.Time
}

type ProductStripeInvoiceSendStore interface {
	GetProductStripeInvoiceSend(ctx context.Context, account, export string) (ProductStripeInvoiceSend, error)
	BeginProductStripeInvoiceSend(ctx context.Context, account, export, token string, attempt ProductStripeInvoiceSend) error
	AdvanceProductStripeInvoiceSend(ctx context.Context, account, export, token, next string, now time.Time) error
	VerifyProductStripeInvoiceSend(ctx context.Context, account, export, token, eventID string, eventAt, retryBefore time.Time) error
	BeginProductStripeInvoiceReminder(ctx context.Context, account, export, token, reviewHash string, now time.Time) error
	CompleteProductStripeInvoiceReminder(ctx context.Context, account, export, token, reviewHash string, now time.Time) error
}

func stripeSendPreviousStage(next string) string {
	switch next {
	case StripeInvoiceFinalizing:
		return StripeInvoicePreparing
	case StripeInvoiceSending:
		return StripeInvoiceFinalizing
	case StripeInvoiceSent:
		return StripeInvoiceSending
	}
	return ""
}

func (s *PgStore) GetProductStripeInvoiceSend(ctx context.Context, account, export string) (ProductStripeInvoiceSend, error) {
	row, err := sqlc.New().ProductStripeInvoiceSend(ctx, s.pool, sqlc.ProductStripeInvoiceSendParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductStripeInvoiceSend{}, ErrNotFound
	}
	if err != nil {
		return ProductStripeInvoiceSend{}, err
	}
	return ProductStripeInvoiceSend{
		ReviewHash: row.ReviewHash, TermsHash: row.TermsHash, DueAt: row.DueAt.Time,
		Stage: row.Stage, SendStartedAt: row.SendStartedAt.Time, SendEventID: row.SendEventID, SendEventAt: row.SendEventAt.Time,
		Reminder: ProductStripeInvoiceReminder{ReviewHash: row.ReminderHash, StartedAt: row.ReminderStartedAt.Time, SentAt: row.ReminderSentAt.Time},
	}, nil
}

func (s *PgStore) BeginProductStripeInvoiceSend(ctx context.Context, account, export, token string, attempt ProductStripeInvoiceSend) error {
	n, err := sqlc.New().BeginProductStripeInvoiceSend(ctx, s.pool, sqlc.BeginProductStripeInvoiceSendParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export), Token: mustPgUUID(token),
		ReviewHash: attempt.ReviewHash, TermsHash: attempt.TermsHash, DueAt: pgTime(attempt.DueAt),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (s *PgStore) AdvanceProductStripeInvoiceSend(ctx context.Context, account, export, token, next string, now time.Time) error {
	previous := stripeSendPreviousStage(next)
	if previous == "" {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().AdvanceProductStripeInvoiceSend(ctx, s.pool, sqlc.AdvanceProductStripeInvoiceSendParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export), Token: mustPgUUID(token),
		PreviousStage: previous, NextStage: next, ObservedAt: pgTime(now),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (m *MemStore) GetProductStripeInvoiceSend(_ context.Context, account, export string) (ProductStripeInvoiceSend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	if e.AccountID != account || e.stripeSend.Stage == "" {
		return ProductStripeInvoiceSend{}, ErrNotFound
	}
	return e.stripeSend, nil
}

func (m *MemStore) BeginProductStripeInvoiceSend(_ context.Context, account, export, token string, attempt ProductStripeInvoiceSend) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	if e.AccountID != account || e.claimToken != token || !e.claimedUntil.After(time.Now()) ||
		e.Provider != BillingProviderStripe || e.Collection != CollectionNone || e.SourceKind != StatementSourceProductControl ||
		e.Status != StatementInvoiceExportCompleted || e.ProviderInvoiceID == "" || e.stripeSend.Stage != "" {
		return ErrConflict
	}
	attempt.Stage, attempt.SendStartedAt = StripeInvoicePreparing, time.Time{}
	attempt.SendEventID, attempt.SendEventAt = "", time.Time{}
	attempt.Reminder = ProductStripeInvoiceReminder{}
	e.stripeSend = attempt
	m.statementInvoiceExports[export] = e
	return nil
}

func (m *MemStore) AdvanceProductStripeInvoiceSend(_ context.Context, account, export, token, next string, now time.Time) error {
	previous := stripeSendPreviousStage(next)
	if previous == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	if e.AccountID != account || e.claimToken != token || !e.claimedUntil.After(time.Now()) || e.stripeSend.Stage != previous {
		return ErrConflict
	}
	e.stripeSend.Stage = next
	if next == StripeInvoiceSending {
		e.stripeSend.SendStartedAt = now
	}
	m.statementInvoiceExports[export] = e
	return nil
}

var stripeSendEventID = lazyregexp.New(`^evt_[A-Za-z0-9]{1,120}$`)

func (s *PgStore) VerifyProductStripeInvoiceSend(ctx context.Context, account, export, token, eventID string, eventAt, retryBefore time.Time) error {
	if !stripeSendEventID.MatchString(eventID) || eventAt.IsZero() {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().VerifyProductStripeInvoiceSend(ctx, s.pool, sqlc.VerifyProductStripeInvoiceSendParams{
		AccountID: mustPgUUID(account), ExportID: mustPgUUID(export), Token: mustPgUUID(token),
		EventID: pgText(eventID), EventAt: pgTime(eventAt), RetryBefore: pgTime(retryBefore),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

func (m *MemStore) VerifyProductStripeInvoiceSend(_ context.Context, account, export, token, eventID string, eventAt, retryBefore time.Time) error {
	if !stripeSendEventID.MatchString(eventID) || eventAt.IsZero() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.statementInvoiceExports[export]
	a := e.stripeSend
	if e.AccountID != account || e.claimToken != token || !e.claimedUntil.After(time.Now()) ||
		a.Stage != StripeInvoiceSending || a.SendStartedAt.After(retryBefore) || eventAt.Before(a.SendStartedAt.Truncate(time.Second)) {
		return ErrConflict
	}
	a.Stage, a.SendEventID, a.SendEventAt = StripeInvoiceSent, eventID, eventAt
	e.stripeSend = a
	m.statementInvoiceExports[export] = e
	return nil
}
