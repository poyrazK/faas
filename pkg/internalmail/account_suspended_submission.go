package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errAccountSuspensionObsolete = errors.New("account suspension notification no longer applies")

type accountSuspendedSubmissionGateKey struct{}

type accountSuspendedSubmissionStore interface {
	shareAccountSuspendedAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) AccountSuspendedOutboxReady(ctx context.Context) error {
	if err := outboxq.New().AccountSuspendedDeliveryReady(ctx, s.pool); err != nil {
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

func accountSuspendedMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "account-suspended:")
}

func (s *Service) accountSuspendedSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !accountSuspendedMessage(m) {
		return ctx, nil
	}
	if !s.accountSuspendedOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(accountSuspendedSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, accountSuspendedSubmissionGateKey{}, s.accountSuspendedOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the notice. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) shareAccountSuspendedAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareAccountSuspendedSubmissionAccount(ctx, tx, outboxq.ShareAccountSuspendedSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errAccountSuspensionObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		row.Status != "suspended" || row.DeletionRequestedAt.Valid || !row.PastDueAt.Valid || !row.SuspensionPastDueAt.Valid || !row.PastDueAt.Time.Equal(row.SuspensionPastDueAt.Time) ||
		!row.AccountSuspendedRevision.Valid || row.BillingMailRevision != row.AccountSuspendedRevision.Int64 || !strings.EqualFold(row.Email, r.Address) {
		return errAccountSuspensionObsolete
	}
	return nil
}
