package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errSubscriptionEndedObsolete = errors.New("subscription ended notification no longer applies")

type subscriptionEndedSubmissionGateKey struct{}

type subscriptionEndedSubmissionStore interface {
	shareSubscriptionEndedAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) SubscriptionEndedOutboxReady(ctx context.Context) error {
	if err := outboxq.New().SubscriptionEndedDeliveryReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := outboxq.New().SubscriptionEndedLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInvalid
	}
	return nil
}

func subscriptionEndedMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "subscription-ended:")
}

func (s *Service) subscriptionEndedSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !subscriptionEndedMessage(m) {
		return ctx, nil
	}
	if !s.subscriptionEndedOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(subscriptionEndedSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, subscriptionEndedSubmissionGateKey{}, s.subscriptionEndedOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the confirmation. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) shareSubscriptionEndedAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareSubscriptionEndedSubmissionAccount(ctx, tx, outboxq.ShareSubscriptionEndedSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errSubscriptionEndedObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		row.Status != "active" || row.Plan != "free" || row.SubscriptionID != "" || row.DeletionRequestedAt.Valid || row.PastDueAt.Valid ||
		!row.SubscriptionEndedRevision.Valid || row.SubscriptionMailRevision != row.SubscriptionEndedRevision.Int64 || !strings.EqualFold(row.Email, r.Address) {
		return errSubscriptionEndedObsolete
	}
	return nil
}
