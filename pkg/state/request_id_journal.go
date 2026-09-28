package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RequestIDJournalEntry is the minimum durable mapping needed to resolve a
// public request ID independently from sampled request telemetry. It contains
// no path, payload, headers, or response data.
type RequestIDJournalEntry struct {
	ID         string
	AccountID  string
	AppID      string
	RequestID  string
	TraceID    string
	ReceivedAt time.Time
	ExpiresAt  time.Time
}

// RequestIDJournalWriter is the durable insert capability. It is deliberately
// separate from Store so MemStore cannot accidentally pretend to provide a
// persistence guarantee that it does not have.
type RequestIDJournalWriter interface {
	RecordRequestIDJournal(context.Context, RequestIDJournalEntry) error
}

// RequestIDJournalReader resolves exact retained IDs after restart.
type RequestIDJournalReader interface {
	FindRequestIDJournalByAppAndIdentifier(context.Context, string, string, string, time.Time, time.Time, time.Time) (RequestIDJournalEntry, error)
}

type RequestIDJournalStore interface {
	RequestIDJournalWriter
	RequestIDJournalReader
}

var _ RequestIDJournalStore = (*PgStore)(nil)

// RecordRequestIDJournal inserts one ID mapping. The record UUID is created at
// the gateway before the RPC so a transport retry can be idempotent without
// collapsing distinct requests that reuse the same public ID.
func (s *PgStore) RecordRequestIDJournal(ctx context.Context, entry RequestIDJournalEntry) error {
	recordID, err := uuid.Parse(entry.ID)
	if err != nil {
		return fmt.Errorf("state: request ID journal record id: %w", err)
	}
	accountID, err := uuid.Parse(entry.AccountID)
	if err != nil {
		return fmt.Errorf("state: request ID journal account id: %w", err)
	}
	appID, err := uuid.Parse(entry.AppID)
	if err != nil {
		return fmt.Errorf("state: request ID journal app id: %w", err)
	}
	if entry.ReceivedAt.IsZero() || entry.ExpiresAt.IsZero() || !entry.ExpiresAt.After(entry.ReceivedAt) {
		return fmt.Errorf("state: request ID journal requires an ordered receive and expiry time")
	}
	_, err = s.appErrorsQueries().RecordRequestIDJournal(ctx, s.pool, sqlc.RecordRequestIDJournalParams{
		ID:         NewPgtypeUUID(recordID),
		AccountID:  NewPgtypeUUID(accountID),
		AppID:      NewPgtypeUUID(appID),
		RequestID:  entry.RequestID,
		TraceID:    entry.TraceID,
		ReceivedAt: pgtype.Timestamptz{Time: entry.ReceivedAt.UTC(), Valid: true},
		ExpiresAt:  pgtype.Timestamptz{Time: entry.ExpiresAt.UTC(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("state: record request ID journal: %w", err)
	}
	return nil
}

// FindRequestIDJournalByAppAndIdentifier returns the newest non-expired
// mapping for this exact account/app pair. The explicit account condition is
// defense in depth in addition to loadApp's ownership check.
func (s *PgStore) FindRequestIDJournalByAppAndIdentifier(
	ctx context.Context,
	accountIDRaw, appIDRaw, requestID string,
	receivedFrom, receivedUntil, now time.Time,
) (RequestIDJournalEntry, error) {
	accountID, err := uuid.Parse(accountIDRaw)
	if err != nil {
		return RequestIDJournalEntry{}, fmt.Errorf("state: request ID journal account id: %w", err)
	}
	appID, err := uuid.Parse(appIDRaw)
	if err != nil {
		return RequestIDJournalEntry{}, fmt.Errorf("state: request ID journal app id: %w", err)
	}
	row, err := s.appErrorsQueries().GetRequestIDJournalByAppAndIdentifier(ctx, s.pool, sqlc.GetRequestIDJournalByAppAndIdentifierParams{
		AccountID:     NewPgtypeUUID(accountID),
		AppID:         NewPgtypeUUID(appID),
		RequestID:     requestID,
		ReceivedFrom:  pgtype.Timestamptz{Time: receivedFrom.UTC(), Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: receivedUntil.UTC(), Valid: true},
		NowAt:         pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	})
	if err != nil {
		return RequestIDJournalEntry{}, err
	}
	entry := RequestIDJournalEntry{
		ID:         uuid.UUID(row.ID.Bytes).String(),
		AccountID:  accountID.String(),
		AppID:      appID.String(),
		RequestID:  row.RequestID,
		ReceivedAt: row.ReceivedAt.Time,
		ExpiresAt:  row.ExpiresAt.Time,
	}
	if row.TraceID.Valid {
		entry.TraceID = row.TraceID.String
	}
	return entry, nil
}
