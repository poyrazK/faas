package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errNonpaymentDeletionObsolete = errors.New("non-payment deletion notification no longer applies")

type nonpaymentDeletionSubmissionGateKey struct{}

type nonpaymentDeletionSubmissionStore interface {
	shareNonpaymentDeletionAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) NonpaymentDeletionOutboxReady(ctx context.Context) error {
	if err := outboxq.New().NonpaymentDeletionDeliveryReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := outboxq.New().AccountRestoredLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInvalid
	}
	return nil
}

func nonpaymentDeletionMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "nonpayment-deletion:")
}

func (s *Service) nonpaymentDeletionSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !nonpaymentDeletionMessage(m) {
		return ctx, nil
	}
	if !s.nonpaymentDeletionOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(nonpaymentDeletionSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, nonpaymentDeletionSubmissionGateKey{}, s.nonpaymentDeletionOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the notice. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) shareNonpaymentDeletionAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareNonpaymentDeletionSubmissionAccount(ctx, tx, outboxq.ShareNonpaymentDeletionSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNonpaymentDeletionObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		row.Status != "deleted_pending" || !row.DeletionRequestedAt.Valid || !row.NonpaymentDeletionAt.Valid || !row.DeletionRequestedAt.Time.Equal(row.NonpaymentDeletionAt.Time) || !row.PastDueAt.Valid || !row.NonpaymentPastDueAt.Valid || !row.PastDueAt.Time.Equal(row.NonpaymentPastDueAt.Time) ||
		!row.NonpaymentDeletionRevision.Valid || row.BillingMailRevision != row.NonpaymentDeletionRevision.Int64 || !strings.EqualFold(row.Email, r.Address) {
		return errNonpaymentDeletionObsolete
	}
	return nil
}
