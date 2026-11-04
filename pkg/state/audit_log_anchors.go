package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AuditLogAnchor seals the audit_log rows received in [WindowStart,
// WindowEnd): their count and digest, chained to the previous anchor by MAC
// (ADR-20261026163015). pkg/auditanchor computes and checks the fields.
type AuditLogAnchor struct {
	Seq         int64
	WindowStart time.Time
	WindowEnd   time.Time
	RowCount    int64
	RowsDigest  string
	PrevMAC     string
	MAC         string
	KeyID       string
	CreatedAt   time.Time
}

// AuditLogAnchorStore appends and reads audit log anchors and pages the
// audit rows an anchor covers.
type AuditLogAnchorStore interface {
	// LatestAuditLogAnchor returns the newest anchor; false before the first.
	LatestAuditLogAnchor(ctx context.Context) (AuditLogAnchor, bool, error)
	// ListAuditLogAnchors returns up to limit anchors from seq fromSeq on.
	ListAuditLogAnchors(ctx context.Context, fromSeq int64, limit int) ([]AuditLogAnchor, error)
	// FirstAuditLogAnchorEndingAfter returns the first anchor whose window
	// ends after the given time; false when none does.
	FirstAuditLogAnchorEndingAfter(ctx context.Context, after time.Time) (int64, bool, error)
	// InsertAuditLogAnchor appends an anchor; false when its seq is taken.
	InsertAuditLogAnchor(ctx context.Context, a AuditLogAnchor) (bool, error)
	// AuditLogWindowPage returns up to limit audit rows received in
	// [start, end), ordered by (ReceivedAt, ID), after the row after (nil
	// for the first page). Data is the stored payload, "" when it is NULL.
	AuditLogWindowPage(ctx context.Context, start, end time.Time, after *AuditLog, limit int) ([]AuditLog, error)
}

var (
	_ AuditLogAnchorStore = (*PgStore)(nil)
	_ AuditLogAnchorStore = (*MemStore)(nil)
)

func (s *PgStore) LatestAuditLogAnchor(ctx context.Context) (AuditLogAnchor, bool, error) {
	rows, err := sqlc.New().LatestAuditLogAnchor(ctx, s.pool)
	if err != nil {
		return AuditLogAnchor{}, false, fmt.Errorf("state: latest audit log anchor: %w", err)
	}
	if len(rows) == 0 {
		return AuditLogAnchor{}, false, nil
	}
	return auditLogAnchorFromRow(rows[0]), true, nil
}

func (s *PgStore) ListAuditLogAnchors(ctx context.Context, fromSeq int64, limit int) ([]AuditLogAnchor, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListAuditLogAnchors(ctx, s.pool, sqlc.ListAuditLogAnchorsParams{FromSeq: fromSeq, PageLimit: int32(min(limit, 10000))})
	if err != nil {
		return nil, fmt.Errorf("state: list audit log anchors: %w", err)
	}
	out := make([]AuditLogAnchor, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditLogAnchorFromRow(row))
	}
	return out, nil
}

func (s *PgStore) FirstAuditLogAnchorEndingAfter(ctx context.Context, after time.Time) (int64, bool, error) {
	seqs, err := sqlc.New().FirstAuditLogAnchorEndingAfter(ctx, s.pool, pgtype.Timestamptz{Time: after, Valid: true})
	if err != nil {
		return 0, false, fmt.Errorf("state: first audit log anchor after %s: %w", after.Format(time.RFC3339), err)
	}
	if len(seqs) == 0 {
		return 0, false, nil
	}
	return seqs[0], true, nil
}

func (s *PgStore) InsertAuditLogAnchor(ctx context.Context, a AuditLogAnchor) (bool, error) {
	n, err := sqlc.New().InsertAuditLogAnchor(ctx, s.pool, sqlc.InsertAuditLogAnchorParams{
		Seq: a.Seq, WindowStart: pgtype.Timestamptz{Time: a.WindowStart, Valid: true},
		WindowEnd: pgtype.Timestamptz{Time: a.WindowEnd, Valid: true}, RowCount: a.RowCount,
		RowsDigest: a.RowsDigest, PrevMac: a.PrevMAC, Mac: a.MAC, KeyID: a.KeyID,
	})
	if err != nil {
		return false, fmt.Errorf("state: insert audit log anchor %d: %w", a.Seq, err)
	}
	return n == 1, nil
}

func (s *PgStore) AuditLogWindowPage(ctx context.Context, start, end time.Time, after *AuditLog, limit int) ([]AuditLog, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	arg := sqlc.AuditLogWindowPageParams{
		WindowStart: pgtype.Timestamptz{Time: start, Valid: true},
		WindowEnd:   pgtype.Timestamptz{Time: end, Valid: true},
		AfterAt:     pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true},
		AfterID:     pgtype.UUID{Valid: true},
		PageLimit:   int32(min(limit, 10000)),
	}
	if after != nil {
		arg.AfterAt = pgtype.Timestamptz{Time: after.ReceivedAt, Valid: true}
		arg.AfterID = pgtype.UUID{Bytes: after.ID, Valid: true}
	}
	rows, err := sqlc.New().AuditLogWindowPage(ctx, s.pool, arg)
	if err != nil {
		return nil, fmt.Errorf("state: page audit log window: %w", err)
	}
	out := make([]AuditLog, 0, len(rows))
	for _, row := range rows {
		entry := AuditLog{ID: row.ID.Bytes, Kind: row.Kind, AccountEmail: row.AccountEmail, Actor: row.Actor,
			ReceivedAt: row.ReceivedAt.Time, Data: json.RawMessage(row.Data)}
		if row.AccountID.Valid {
			id := uuid.UUID(row.AccountID.Bytes)
			entry.AccountID = &id
		}
		out = append(out, entry)
	}
	return out, nil
}

func auditLogAnchorFromRow(row sqlc.AuditLogAnchor) AuditLogAnchor {
	return AuditLogAnchor{
		Seq: row.Seq, WindowStart: row.WindowStart.Time, WindowEnd: row.WindowEnd.Time, RowCount: row.RowCount,
		RowsDigest: row.RowsDigest, PrevMAC: row.PrevMac, MAC: row.Mac, KeyID: row.KeyID, CreatedAt: row.CreatedAt.Time,
	}
}

func (m *MemStore) LatestAuditLogAnchor(_ context.Context) (AuditLogAnchor, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.auditLogAnchors) == 0 {
		return AuditLogAnchor{}, false, nil
	}
	return m.auditLogAnchors[len(m.auditLogAnchors)-1], true, nil
}

func (m *MemStore) ListAuditLogAnchors(_ context.Context, fromSeq int64, limit int) ([]AuditLogAnchor, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditLogAnchor
	for _, a := range m.auditLogAnchors {
		if a.Seq >= fromSeq && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemStore) FirstAuditLogAnchorEndingAfter(_ context.Context, after time.Time) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.auditLogAnchors {
		if a.WindowEnd.After(after) {
			return a.Seq, true, nil
		}
	}
	return 0, false, nil
}

// InsertAuditLogAnchor keeps the anchors in seq order, as the table's
// primary key does.
func (m *MemStore) InsertAuditLogAnchor(_ context.Context, a AuditLogAnchor) (bool, error) {
	if a.Seq < 1 || !a.WindowEnd.After(a.WindowStart) || a.RowCount < 0 || (a.Seq == 1) != (a.PrevMAC == "") {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, found := slices.BinarySearchFunc(m.auditLogAnchors, a.Seq, func(x AuditLogAnchor, seq int64) int {
		return compareInt64(x.Seq, seq)
	})
	if found {
		return false, nil
	}
	a.CreatedAt = time.Now()
	m.auditLogAnchors = slices.Insert(m.auditLogAnchors, i, a)
	return true, nil
}

// AuditLogWindowPage orders by the microsecond PostgreSQL keeps, then by ID
// bytes, as uuid ordering does.
func (m *MemStore) AuditLogWindowPage(_ context.Context, start, end time.Time, after *AuditLog, limit int) ([]AuditLog, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	rows := make([]AuditLog, 0, len(m.auditLog))
	for _, entry := range m.auditLog {
		entry.ReceivedAt = entry.ReceivedAt.Truncate(time.Microsecond)
		if entry.ReceivedAt.Before(start) || !entry.ReceivedAt.Before(end) {
			continue
		}
		if after != nil && compareAuditLogOrder(entry, *after) <= 0 {
			continue
		}
		entry.Data = append(json.RawMessage(nil), entry.Data...)
		rows = append(rows, entry)
	}
	m.mu.Unlock()
	slices.SortFunc(rows, compareAuditLogOrder)
	return rows[:min(limit, len(rows))], nil
}

func compareAuditLogOrder(a, b AuditLog) int {
	if c := a.ReceivedAt.Truncate(time.Microsecond).Compare(b.ReceivedAt.Truncate(time.Microsecond)); c != 0 {
		return c
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
