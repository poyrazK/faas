// adr: 933
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) AcceptEntityOutboxDelivery(ctx context.Context, in AppWebhookDelivery) (string, error) {
	fingerprint, err := entityOutboxFingerprint(in)
	if err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin entity outbox acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	inserted, err := q.InsertEntityOutboxAcceptance(ctx, tx, sqlc.InsertEntityOutboxAcceptanceParams{
		MessageID: in.ID, AccountID: in.AccountID, AppID: in.AppID, Fingerprint: fingerprint,
	})
	if err != nil {
		return "", fmt.Errorf("reserve entity outbox acceptance: %w", err)
	}
	if inserted == 0 {
		previous, err := q.GetEntityOutboxAcceptanceFingerprint(ctx, tx, in.ID)
		if err != nil {
			return "", fmt.Errorf("read entity outbox acceptance: %w", err)
		}
		if previous != fingerprint {
			return "", ErrConflict
		}
		// A committed receipt proves acceptance even after terminal-history
		// retention or destination deletion. Do not recreate or reset delivery.
		return in.ID, nil
	}
	inserted, err = q.InsertEntityOutboxWebhookDelivery(ctx, tx, sqlc.InsertEntityOutboxWebhookDeliveryParams{
		MessageID: in.ID, WebhookID: in.WebhookID, AppID: in.AppID, AccountID: in.AccountID, Event: string(in.Event), Payload: in.Payload,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return "", ErrConflict
		}
		return "", fmt.Errorf("enqueue entity outbox delivery: %w", err)
	}
	if inserted != 1 {
		return "", ErrNotFound // Missing, disabled, wrong-scope destination; roll back receipt.
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit entity outbox acceptance: %w", err)
	}
	return in.ID, nil
}
