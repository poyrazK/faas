package internalmail

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) lockSuppressionAddress(ctx context.Context, tx pgx.Tx, address string) error {
	return s.queries.LockSuppressionAddress(ctx, tx, q.LockSuppressionAddressParams{Namespace: s.namespace, Address: strings.ToLower(address)})
}

func (s *PGStore) shareSuppressionAddress(ctx context.Context, tx pgx.Tx, address string) error {
	return s.queries.ShareSuppressionAddress(ctx, tx, q.ShareSuppressionAddressParams{Namespace: s.namespace, Address: strings.ToLower(address)})
}

func (s *PGStore) shareAdmissionSuppressions(ctx context.Context, tx pgx.Tx, recipients []Recipient) error {
	addresses := make([]string, 0, len(recipients))
	for _, r := range recipients {
		addresses = append(addresses, strings.ToLower(r.Address))
	}
	sort.Strings(addresses)
	for i, address := range addresses {
		if i > 0 && address == addresses[i-1] {
			continue
		}
		if err := s.shareSuppressionAddress(ctx, tx, address); err != nil {
			return err
		}
	}
	return nil
}

func (s *PGStore) recipientSuppressed(ctx context.Context, id string) (bool, error) {
	return s.recipientSuppressedWith(ctx, s.pool, id)
}

func (s *PGStore) recipientSuppressedWith(ctx context.Context, db q.DBTX, id string) (bool, error) {
	blocked, err := s.queries.RecipientSuppressed(ctx, db, q.RecipientSuppressedParams{Namespace: s.namespace, ID: pgID(id)})
	if err == nil && !blocked.Valid {
		return false, ErrInvalid
	}
	return blocked.Bool, storeError(err)
}

// Acquire before namespace/domain/producer/recipient/bucket locks. Feedback
// takes this address exclusively before its reputation or recipient locks.
func (s *PGStore) shareRecipientSuppression(ctx context.Context, tx pgx.Tx, id string) error {
	address, err := s.queries.RecipientSuppressionAddress(ctx, tx, q.RecipientSuppressionAddressParams{Namespace: s.namespace, ID: pgID(id)})
	if err != nil {
		return storeError(err)
	}
	if err := s.shareSuppressionAddress(ctx, tx, address); err != nil {
		return err
	}
	blocked, err := s.recipientSuppressedWith(ctx, tx, id)
	if err != nil {
		return err
	}
	if blocked {
		return ErrRecipientSuppressed
	}
	return nil
}
