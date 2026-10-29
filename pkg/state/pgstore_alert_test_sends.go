package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var _ AlertTestSendStore = (*PgStore)(nil)

const alertTestSendColumns = `id::text, account_id::text, kind, subject_id::text, event, state,
	coalesce(claim_token::text, ''), result, created_at`

func scanAlertTestSend(row pgx.Row) (AlertTestSend, error) {
	var (
		t      AlertTestSend
		result []byte
	)
	err := row.Scan(&t.ID, &t.AccountID, &t.Kind, &t.SubjectID, &t.Event, &t.State, &t.ClaimToken, &result, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertTestSend{}, ErrNotFound
	}
	if err != nil {
		return AlertTestSend{}, err
	}
	if len(result) > 0 {
		var r AlertTestSendResult
		if err := json.Unmarshal(result, &r); err != nil {
			return AlertTestSend{}, fmt.Errorf("state: decode alert test send result: %w", err)
		}
		t.Result = &r
	}
	return t, nil
}

// CreateAlertTestSend records a pending test send (ADR 20261022111812).
func (s *PgStore) CreateAlertTestSend(ctx context.Context, in AlertTestSend) (AlertTestSend, error) {
	if len(in.Event) == 0 {
		in.Event = json.RawMessage(`{}`)
	}
	if !validAlertTestSend(in) {
		return AlertTestSend{}, ErrInvalidArgument
	}
	t, err := scanAlertTestSend(s.pool.QueryRow(ctx, `
		INSERT INTO alert_test_sends (id, account_id, kind, subject_id, event)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5)
		RETURNING `+alertTestSendColumns, in.ID, in.AccountID, in.Kind, in.SubjectID, in.Event))
	if err != nil {
		return AlertTestSend{}, fmt.Errorf("state: create alert test send: %w", mapErr(err))
	}
	return t, nil
}

// GetAlertTestSend returns the account's test send.
func (s *PgStore) GetAlertTestSend(ctx context.Context, accountID, id string) (AlertTestSend, error) {
	return scanAlertTestSend(s.pool.QueryRow(ctx,
		`SELECT `+alertTestSendColumns+` FROM alert_test_sends WHERE id::text = $1 AND account_id::text = $2`, id, accountID))
}

// ClaimAlertTestSend claims the oldest open test send whose claim, if any,
// has lapsed.
func (s *PgStore) ClaimAlertTestSend(ctx context.Context, lease time.Duration) (AlertTestSend, bool, error) {
	t, err := scanAlertTestSend(s.pool.QueryRow(ctx, `
		UPDATE alert_test_sends SET state = 'claimed', claim_token = $1::uuid,
		       claimed_until = now() + make_interval(secs => $2)
		 WHERE id = (
		       SELECT id FROM alert_test_sends
		        WHERE state = 'pending' OR (state = 'claimed' AND claimed_until < now())
		        ORDER BY created_at
		        LIMIT 1
		        FOR UPDATE SKIP LOCKED)
		RETURNING `+alertTestSendColumns, uuid.NewString(), lease.Seconds()))
	if errors.Is(err, ErrNotFound) {
		return AlertTestSend{}, false, nil
	}
	if err != nil {
		return AlertTestSend{}, false, fmt.Errorf("state: claim alert test send: %w", err)
	}
	return t, true, nil
}

// CompleteAlertTestSend stores the result while claimToken still holds the
// claim; a lapsed claim reports ErrConflict.
func (s *PgStore) CompleteAlertTestSend(ctx context.Context, id, claimToken string, result AlertTestSendResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE alert_test_sends SET state = 'done', result = $3, completed_at = now(),
		       claim_token = NULL, claimed_until = NULL
		 WHERE id::text = $1 AND state = 'claimed' AND claim_token::text = $2`, id, claimToken, body)
	if err != nil {
		return fmt.Errorf("state: complete alert test send: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

// DeleteAlertTestSend removes the account's test send.
func (s *PgStore) DeleteAlertTestSend(ctx context.Context, accountID, id string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM alert_test_sends WHERE id::text = $1 AND account_id::text = $2`, id, accountID); err != nil {
		return fmt.Errorf("state: delete alert test send: %w", err)
	}
	return nil
}

// PruneAlertTestSends removes test sends created before cutoff.
func (s *PgStore) PruneAlertTestSends(ctx context.Context, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_test_sends WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("state: prune alert test sends: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
