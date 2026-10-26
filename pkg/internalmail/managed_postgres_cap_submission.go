package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

// Managed PostgreSQL compute cap warnings (ADR-20261025094915). Maild checks
// at submission that the account still exists, is not being deleted and has
// the address the warning was queued for; apid remains the outbox writer.

var errManagedPostgresCapObsolete = errors.New("managed postgres cap notification no longer applies")

type managedPostgresCapSubmissionGateKey struct{}

type managedPostgresCapSubmissionStore interface {
	shareManagedPostgresCapAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) ManagedPostgresCapOutboxReady(ctx context.Context) error {
	return outboxq.New().ManagedPostgresCapDeliveryReady(ctx, s.pool)
}

func managedPostgresCapMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "managed-postgres-cap:")
}

func (s *Service) managedPostgresCapSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !managedPostgresCapMessage(m) {
		return ctx, nil
	}
	if !s.managedPostgresCapOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(managedPostgresCapSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, managedPostgresCapSubmissionGateKey{}, s.managedPostgresCapOutboxEnabled), nil
}

func (s *PGStore) shareManagedPostgresCapAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareManagedPostgresCapSubmissionAccount(ctx, tx, outboxq.ShareManagedPostgresCapSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errManagedPostgresCapObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		(row.Status != "active" && row.Status != "past_due" && row.Status != "suspended") || row.DeletionRequestedAt.Valid ||
		!strings.EqualFold(row.Email, r.Address) {
		return errManagedPostgresCapObsolete
	}
	return nil
}
