package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errAccountRestorationObsolete = errors.New("account restoration notification no longer applies")

type accountRestoredSubmissionGateKey struct{}

type accountRestoredSubmissionStore interface {
	shareAccountRestoredAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) AccountRestoredOutboxReady(ctx context.Context) error {
	if err := outboxq.New().AccountRestoredDeliveryReady(ctx, s.pool); err != nil {
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

func accountRestoredMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "account-restored:")
}

func (s *Service) accountRestoredSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !accountRestoredMessage(m) {
		return ctx, nil
	}
	if !s.accountRestoredOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(accountRestoredSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, accountRestoredSubmissionGateKey{}, s.accountRestoredOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the confirmation. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) shareAccountRestoredAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareAccountRestoredSubmissionAccount(ctx, tx, outboxq.ShareAccountRestoredSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errAccountRestorationObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		row.Status != "active" || row.DeletionRequestedAt.Valid || row.PastDueAt.Valid ||
		!row.AccountRestoredRevision.Valid || row.BillingMailRevision != row.AccountRestoredRevision.Int64 || !strings.EqualFold(row.Email, r.Address) {
		return errAccountRestorationObsolete
	}
	return nil
}
