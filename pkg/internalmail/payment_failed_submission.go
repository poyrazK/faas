package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errPaymentFailureObsolete = errors.New("payment failure notification no longer applies")

type paymentFailedSubmissionGateKey struct{}

type paymentFailedSubmissionStore interface {
	sharePaymentFailedAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) PaymentFailedOutboxReady(ctx context.Context) error {
	return outboxq.New().PaymentFailedDeliveryReady(ctx, s.pool)
}

func paymentFailedMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "payment-failed:")
}

func (s *Service) paymentFailedSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !paymentFailedMessage(m) {
		return ctx, nil
	}
	if !s.paymentFailedOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(paymentFailedSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, paymentFailedSubmissionGateKey{}, s.paymentFailedOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the warning. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) sharePaymentFailedAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().SharePaymentFailedSubmissionAccount(ctx, tx, outboxq.SharePaymentFailedSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errPaymentFailureObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		row.Status != "past_due" || row.DeletionRequestedAt.Valid || !row.PastDueAt.Valid ||
		!row.PaymentPastDueAt.Valid || !row.PastDueAt.Time.Equal(row.PaymentPastDueAt.Time) || !strings.EqualFold(row.Email, r.Address) {
		return errPaymentFailureObsolete
	}
	return nil
}
