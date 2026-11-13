package support

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// SnapshotMaxBytes bounds a stored ticket snapshot; the column check
// enforces the same limit.
const SnapshotMaxBytes = 32 << 10

// ErrSnapshotTooLarge is returned when an encoded snapshot exceeds
// SnapshotMaxBytes.
var ErrSnapshotTooLarge = errors.New("support: ticket snapshot too large")

// SaveTicketSnapshot stores the account health captured when a ticket was
// opened (ADR-20261112210000). The first snapshot wins: replaying the same
// create (idempotency) or a later call never replaces it. snapshot must be
// a JSON object without conversation text.
func (s *Service) SaveTicketSnapshot(ctx context.Context, ticketID string, capturedAt time.Time, snapshot json.RawMessage) error {
	if s == nil || s.Pool == nil {
		return errors.New("support: unavailable")
	}
	if len(snapshot) > SnapshotMaxBytes {
		return ErrSnapshotTooLarge
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO support_ticket_snapshots(ticket_id, captured_at, snapshot)
		VALUES ($1, $2, $3) ON CONFLICT (ticket_id) DO NOTHING`, uid(ticketID), capturedAt.UTC(), []byte(snapshot))
	return err
}

// TicketSnapshot returns the stored snapshot, or ok=false when the ticket
// has none. Callers must already have authorized the reader for the ticket.
func (s *Service) TicketSnapshot(ctx context.Context, ticketID string) (snapshot json.RawMessage, capturedAt time.Time, ok bool, err error) {
	if s == nil || s.Pool == nil {
		return nil, time.Time{}, false, errors.New("support: unavailable")
	}
	var raw []byte
	err = s.Pool.QueryRow(ctx, `SELECT snapshot, captured_at FROM support_ticket_snapshots WHERE ticket_id=$1`, uid(ticketID)).
		Scan(&raw, &capturedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return raw, capturedAt.UTC(), true, nil
}
