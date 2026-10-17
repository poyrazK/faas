package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errQuotaWarningObsolete = errors.New("quota warning notification no longer applies")

type quotaWarningSubmissionGateKey struct{}

type quotaWarningSubmissionStore interface {
	shareQuotaWarningAccount(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) QuotaWarningOutboxReady(ctx context.Context) error {
	if err := outboxq.New().QuotaWarningDeliveryReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := outboxq.New().QuotaWarningLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInvalid
	}
	return nil
}

func quotaWarningMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "quota-warning:")
}

func (s *Service) quotaWarningSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !quotaWarningMessage(m) {
		return ctx, nil
	}
	if !s.quotaWarningOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(quotaWarningSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, quotaWarningSubmissionGateKey{}, s.quotaWarningOutboxEnabled), nil
}

// The account lock precedes maild's submission locks. Recovery/address/status
// changes that commit first cancel the notice. Once submission start commits,
// SMTP custody may follow and cannot be recalled or automatically replayed.
// Maild reads metadata only; apid remains the billing/outbox writer.
func (s *PGStore) shareQuotaWarningAccount(ctx context.Context, tx pgx.Tx, r Recipient) error {
	row, err := outboxq.New().ShareQuotaWarningSubmissionAccount(ctx, tx, outboxq.ShareQuotaWarningSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errQuotaWarningObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) ||
		(row.Status != "active" && row.Status != "past_due" && row.Status != "suspended") || row.DeletionRequestedAt.Valid ||
		!row.QuotaWarningDay.Valid || !row.CurrentDay.Time.Equal(row.QuotaWarningDay.Time) || !row.LastQuotaWarningAt.Valid || !row.LastQuotaWarningAt.Time.Equal(row.QuotaWarningDay.Time) ||
		!row.QuotaWarningPlan.Valid || row.Plan != row.QuotaWarningPlan.String || !row.QuotaWarningRevision.Valid || row.QuotaMailRevision != row.QuotaWarningRevision.Int64 || !strings.EqualFold(row.Email, r.Address) {
		return errQuotaWarningObsolete
	}

	return nil
}
