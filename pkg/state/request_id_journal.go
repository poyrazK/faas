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

// RequestIDJournalBatchWriter inserts many mappings in one statement
// (ADR-1521). It returns the ids it recorded; an entry whose app is not owned
// by its account, or whose id already holds different content, is left out.
type RequestIDJournalBatchWriter interface {
	RecordRequestIDJournalBatch(context.Context, []RequestIDJournalEntry) ([]string, error)
}

// RequestIDJournalReader resolves exact retained IDs after restart.
type RequestIDJournalReader interface {
	FindRequestIDJournalByAppAndIdentifier(context.Context, string, string, string, time.Time, time.Time, time.Time) (RequestIDJournalEntry, error)
}

type RequestIDJournalStore interface {
	RequestIDJournalWriter
	RequestIDJournalReader
}

var (
	_ RequestIDJournalStore       = (*PgStore)(nil)
	_ RequestIDJournalBatchWriter = (*PgStore)(nil)
)

// RecordRequestIDJournal inserts one ID mapping. The record UUID is created at
// the gateway before the RPC so a transport retry can be idempotent without
// collapsing distinct requests that reuse the same public ID.
func (s *PgStore) RecordRequestIDJournal(ctx context.Context, entry RequestIDJournalEntry) error {
	recordID, accountID, appID, err := parseRequestIDJournalEntry(entry)
	if err != nil {
		return err
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

// RecordRequestIDJournalBatch inserts entries in one statement (ADR-1521).
// Record ids must be unique within the call. An invalid entry fails the whole
// call before anything is written; callers validate records first.
func (s *PgStore) RecordRequestIDJournalBatch(ctx context.Context, entries []RequestIDJournalEntry) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	arg := sqlc.RecordRequestIDJournalBatchParams{}
	seen := make(map[uuid.UUID]bool, len(entries))
	for _, entry := range entries {
		recordID, accountID, appID, err := parseRequestIDJournalEntry(entry)
		if err != nil {
			return nil, err
		}
		if seen[recordID] {
			return nil, fmt.Errorf("state: request ID journal batch repeats record id %s", recordID)
		}
		seen[recordID] = true
		arg.Ids = append(arg.Ids, NewPgtypeUUID(recordID))
		arg.AccountIds = append(arg.AccountIds, NewPgtypeUUID(accountID))
		arg.AppIds = append(arg.AppIds, NewPgtypeUUID(appID))
		arg.RequestIds = append(arg.RequestIds, entry.RequestID)
		arg.TraceIds = append(arg.TraceIds, entry.TraceID)
		arg.ReceivedAts = append(arg.ReceivedAts, pgtype.Timestamptz{Time: entry.ReceivedAt.UTC(), Valid: true})
		arg.ExpiresAts = append(arg.ExpiresAts, pgtype.Timestamptz{Time: entry.ExpiresAt.UTC(), Valid: true})
	}
	rows, err := s.appErrorsQueries().RecordRequestIDJournalBatch(ctx, s.pool, arg)
	if err != nil {
		return nil, fmt.Errorf("state: record request ID journal batch: %w", err)
	}
	recorded := make([]string, 0, len(rows))
	for _, id := range rows {
		recorded = append(recorded, pgUUIDString(id))
	}
	return recorded, nil
}

func parseRequestIDJournalEntry(entry RequestIDJournalEntry) (recordID, accountID, appID uuid.UUID, err error) {
	if recordID, err = uuid.Parse(entry.ID); err != nil {
		return recordID, accountID, appID, fmt.Errorf("state: request ID journal record id: %w", err)
	}
	if accountID, err = uuid.Parse(entry.AccountID); err != nil {
		return recordID, accountID, appID, fmt.Errorf("state: request ID journal account id: %w", err)
	}
	if appID, err = uuid.Parse(entry.AppID); err != nil {
		return recordID, accountID, appID, fmt.Errorf("state: request ID journal app id: %w", err)
	}
	if entry.ReceivedAt.IsZero() || entry.ExpiresAt.IsZero() || !entry.ExpiresAt.After(entry.ReceivedAt) {
		return recordID, accountID, appID, fmt.Errorf("state: request ID journal requires an ordered receive and expiry time")
	}
	return recordID, accountID, appID, nil
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
