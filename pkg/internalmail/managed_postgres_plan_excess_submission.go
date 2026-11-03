package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

// Managed PostgreSQL plan excess notices (ADR-20261102125208). Maild checks
// at submission that the account still exists, is not being deleted and has
// the address the notice was queued for; apid remains the outbox writer.

var errManagedPostgresPlanExcessObsolete = errors.New("managed postgres plan excess notification no longer applies")

type managedPostgresPlanExcessSubmissionGateKey struct{}

type managedPostgresPlanExcessSubmissionStore interface {
	shareManagedPostgresPlanExcessAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) ManagedPostgresPlanExcessOutboxReady(ctx context.Context) error {
	return outboxq.New().ManagedPostgresPlanExcessDeliveryReady(ctx, s.pool)
}

func managedPostgresPlanExcessMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "managed-postgres-plan-excess:")
}

func (s *Service) managedPostgresPlanExcessSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !managedPostgresPlanExcessMessage(m) {
		return ctx, nil
	}
	if !s.managedPostgresPlanExcessOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(managedPostgresPlanExcessSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, managedPostgresPlanExcessSubmissionGateKey{}, s.managedPostgresPlanExcessOutboxEnabled), nil
}

func (s *PGStore) shareManagedPostgresPlanExcessAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareManagedPostgresPlanExcessSubmissionAccount(ctx, tx, outboxq.ShareManagedPostgresPlanExcessSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errManagedPostgresPlanExcessObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		(row.Status != "active" && row.Status != "past_due" && row.Status != "suspended") || row.DeletionRequestedAt.Valid ||
		!strings.EqualFold(row.Email, r.Address) {
		return errManagedPostgresPlanExcessObsolete
	}
	return nil
}
