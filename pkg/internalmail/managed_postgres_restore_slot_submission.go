package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

// Managed PostgreSQL restore slot notices (ADR-20261031123259). Maild checks
// at submission that the account still exists, is not being deleted and has
// the address the notice was queued for; apid remains the outbox writer.

var errManagedPostgresRestoreSlotObsolete = errors.New("managed postgres restore slot notification no longer applies")

type managedPostgresRestoreSlotSubmissionGateKey struct{}

type managedPostgresRestoreSlotSubmissionStore interface {
	shareManagedPostgresRestoreSlotAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) ManagedPostgresRestoreSlotOutboxReady(ctx context.Context) error {
	return outboxq.New().ManagedPostgresRestoreSlotDeliveryReady(ctx, s.pool)
}

func managedPostgresRestoreSlotMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "managed-postgres-restore-slot:")
}

func (s *Service) managedPostgresRestoreSlotSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !managedPostgresRestoreSlotMessage(m) {
		return ctx, nil
	}
	if !s.managedPostgresRestoreSlotOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(managedPostgresRestoreSlotSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, managedPostgresRestoreSlotSubmissionGateKey{}, s.managedPostgresRestoreSlotOutboxEnabled), nil
}

func (s *PGStore) shareManagedPostgresRestoreSlotAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareManagedPostgresRestoreSlotSubmissionAccount(ctx, tx, outboxq.ShareManagedPostgresRestoreSlotSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errManagedPostgresRestoreSlotObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		(row.Status != "active" && row.Status != "past_due" && row.Status != "suspended") || row.DeletionRequestedAt.Valid ||
		!strings.EqualFold(row.Email, r.Address) {
		return errManagedPostgresRestoreSlotObsolete
	}
	return nil
}
