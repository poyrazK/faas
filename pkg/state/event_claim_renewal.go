package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublishedEventLeaseRenewal is how often a sweep renews the receipt and
// recipient claims it still holds (ADR-20261028131500). A claim lasts
// PublishedEventLease, so a slow sweep keeps it and a crashed one loses it
// within a minute.
const PublishedEventLeaseRenewal = 20 * time.Second

// PublishedEventClaimRenewalStore renews claims a sweep still holds. A claim
// another worker took since has a new token and is left alone.
type PublishedEventClaimRenewalStore interface {
	RenewPublishedEventClaims(ctx context.Context, works []*PublishedEventWork, now time.Time) error
	RenewPublishedEventRecipientClaims(ctx context.Context, works []*PublishedEventRecipientWork, now time.Time) error
}

func (s *PgStore) RenewPublishedEventClaims(ctx context.Context, works []*PublishedEventWork, now time.Time) error {
	if len(works) == 0 {
		return nil
	}
	params := sqlc.EventRoutingRenewReceiptClaimsParams{NowAt: pgtypeFromTime(now), Ids: make([]int64, 0, len(works)), ClaimTokens: make([]pgtype.UUID, 0, len(works))}
	for _, work := range works {
		params.Ids = append(params.Ids, work.ID)
		params.ClaimTokens = append(params.ClaimTokens, mustPgUUID(work.ClaimToken))
	}
	if _, err := sqlc.New().EventRoutingRenewReceiptClaims(ctx, s.pool, params); err != nil {
		return fmt.Errorf("renew event receipt claims: %w", err)
	}
	return nil
}

func (s *PgStore) RenewPublishedEventRecipientClaims(ctx context.Context, works []*PublishedEventRecipientWork, now time.Time) error {
	if len(works) == 0 {
		return nil
	}
	params := sqlc.EventRecipientRenewClaimsParams{NowAt: pgtypeFromTime(now), OutboxIds: make([]int64, 0, len(works)),
		SubscriptionIds: make([]string, 0, len(works)), ClaimTokens: make([]pgtype.UUID, 0, len(works))}
	for _, work := range works {
		params.OutboxIds = append(params.OutboxIds, work.OutboxID)
		params.SubscriptionIds = append(params.SubscriptionIds, work.Recipient.ID)
		params.ClaimTokens = append(params.ClaimTokens, mustPgUUID(work.ClaimToken))
	}
	if _, err := sqlc.New().EventRecipientRenewClaims(ctx, s.pool, params); err != nil {
		return fmt.Errorf("renew event recipient claims: %w", err)
	}
	return nil
}

func (m *MemStore) RenewPublishedEventClaims(_ context.Context, works []*PublishedEventWork, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, claimed := range works {
		work := m.routingReceiptLocked(claimed.ID)
		if work != nil && !work.Delivered && !work.RecipientClaims && work.ClaimToken != "" && work.ClaimToken == claimed.ClaimToken {
			work.LeaseUntil = now.Add(PublishedEventLease)
		}
	}
	return nil
}

func (m *MemStore) RenewPublishedEventRecipientClaims(_ context.Context, works []*PublishedEventRecipientWork, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, claimed := range works {
		receipt := m.routingReceiptLocked(claimed.OutboxID)
		if receipt == nil {
			continue
		}
		if work := receipt.routingRecipients[claimed.Recipient.ID]; work != nil && work.State == "processing" && work.ClaimToken == claimed.ClaimToken {
			work.LeaseUntil = now.Add(PublishedEventLease)
		}
	}
	return nil
}
