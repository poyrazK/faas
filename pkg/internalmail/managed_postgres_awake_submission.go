package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

// Managed PostgreSQL awake notices (ADR-20261125231222). Maild checks
// at submission that the account still exists, is not being deleted and has
// the address the notice was queued for; apid remains the outbox writer.

var errManagedPostgresAwakeObsolete = errors.New("managed postgres awake notification no longer applies")

type managedPostgresAwakeSubmissionGateKey struct{}

type managedPostgresAwakeSubmissionStore interface {
	shareManagedPostgresAwakeAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) ManagedPostgresAwakeOutboxReady(ctx context.Context) error {
	return outboxq.New().ManagedPostgresAwakeDeliveryReady(ctx, s.pool)
}

func managedPostgresAwakeMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "managed-postgres-awake:")
}

func (s *Service) managedPostgresAwakeSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !managedPostgresAwakeMessage(m) {
		return ctx, nil
	}
	if !s.managedPostgresAwakeOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(managedPostgresAwakeSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, managedPostgresAwakeSubmissionGateKey{}, s.managedPostgresAwakeOutboxEnabled), nil
}

func (s *PGStore) shareManagedPostgresAwakeAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareManagedPostgresAwakeSubmissionAccount(ctx, tx, outboxq.ShareManagedPostgresAwakeSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errManagedPostgresAwakeObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		(row.Status != "active" && row.Status != "past_due" && row.Status != "suspended") || row.DeletionRequestedAt.Valid ||
		!strings.EqualFold(row.Email, r.Address) {
		return errManagedPostgresAwakeObsolete
	}
	return nil
}
