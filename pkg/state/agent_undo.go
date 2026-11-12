package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentUndoRecord says how to reverse one write an agent key made
// (ADR-20261112080601). Inverse is nil when the write cannot be undone;
// NotUndoableReason then says why.
type AgentUndoRecord struct {
	ID                string
	ActivityID        string
	KeyID             string
	AccountID         string
	OrgID             string
	Method            string
	Path              string
	Kind              string
	Summary           string
	Inverse           json.RawMessage
	Guard             json.RawMessage
	NotUndoableReason string
	CreatedAt         time.Time
	UndoneAt          *time.Time
	UndoneBy          string
}

// AgentUndoStore keeps agent undo records. apid is the only writer.
type AgentUndoStore interface {
	// RecordAgentUndo stores a record; an empty ID is generated.
	RecordAgentUndo(ctx context.Context, rec AgentUndoRecord) error
	// AgentUndoRecords returns the key's records created at or after since,
	// newest first, at most limit.
	AgentUndoRecords(ctx context.Context, keyID string, since time.Time, limit int) ([]AgentUndoRecord, error)
	// MarkAgentUndone marks an undoable record undone by a person; false
	// when it was already undone (or cannot be undone).
	MarkAgentUndone(ctx context.Context, id, undoneBy string) (bool, error)
	// DeleteAgentUndoRecordsBefore removes records older than before.
	DeleteAgentUndoRecordsBefore(ctx context.Context, before time.Time) (int64, error)
}

func (s *PgStore) RecordAgentUndo(ctx context.Context, rec AgentUndoRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	return mapErr(sqlc.New().InsertAgentUndoRecord(ctx, s.pool, sqlc.InsertAgentUndoRecordParams{
		ID: mustPgUUID(rec.ID), ActivityID: mustPgUUID(rec.ActivityID), KeyID: mustPgUUID(rec.KeyID),
		AccountID: mustPgUUID(rec.AccountID), OrgID: mustPgUUID(rec.OrgID), Method: rec.Method, Path: rec.Path,
		Kind: rec.Kind, Summary: rec.Summary, Inverse: nullJSON(rec.Inverse), Guard: nullJSON(rec.Guard),
		NotUndoableReason: pgText(rec.NotUndoableReason), CreatedAt: pgTime(rec.CreatedAt),
	}))
}

func (s *PgStore) AgentUndoRecords(ctx context.Context, keyID string, since time.Time, limit int) ([]AgentUndoRecord, error) {
	if uuid.Validate(keyID) != nil {
		return []AgentUndoRecord{}, nil
	}
	rows, err := sqlc.New().ListAgentUndoRecords(ctx, s.pool, sqlc.ListAgentUndoRecordsParams{
		KeyID: mustPgUUID(keyID), Since: pgTime(since), RowLimit: int32(limit), //nolint:gosec // callers bound limit
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AgentUndoRecord, 0, len(rows))
	for _, r := range rows {
		rec := AgentUndoRecord{
			ID: pgUUIDString(r.ID), ActivityID: pgUUIDString(r.ActivityID), KeyID: pgUUIDString(r.KeyID),
			AccountID: pgUUIDString(r.AccountID), OrgID: pgUUIDString(r.OrgID), Method: r.Method, Path: r.Path,
			Kind: r.Kind, Summary: r.Summary, Inverse: rawJSON(r.Inverse), Guard: rawJSON(r.Guard),
			NotUndoableReason: r.NotUndoableReason.String, CreatedAt: r.CreatedAt.Time.UTC(),
			UndoneBy: pgUUIDString(r.UndoneBy),
		}
		if r.UndoneAt.Valid {
			at := r.UndoneAt.Time.UTC()
			rec.UndoneAt = &at
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *PgStore) MarkAgentUndone(ctx context.Context, id, undoneBy string) (bool, error) {
	if uuid.Validate(id) != nil || uuid.Validate(undoneBy) != nil {
		return false, nil
	}
	n, err := sqlc.New().MarkAgentUndoRecordUndone(ctx, s.pool, sqlc.MarkAgentUndoRecordUndoneParams{
		ID: mustPgUUID(id), UndoneBy: mustPgUUID(undoneBy),
	})
	return n == 1, mapErr(err)
}

func (s *PgStore) DeleteAgentUndoRecordsBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := sqlc.New().DeleteAgentUndoRecordsBefore(ctx, s.pool, pgTime(before))
	return n, mapErr(err)
}

func nullJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(append([]byte(nil), b...))
}

// --- MemStore ---------------------------------------------------------------

func (m *MemStore) RecordAgentUndo(_ context.Context, rec AgentUndoRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if (len(rec.Inverse) == 0) == (rec.NotUndoableReason == "") {
		return ErrConflict // agent_undo_records_undoable_check
	}
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	rec.Inverse, rec.Guard = rawJSON(rec.Inverse), rawJSON(rec.Guard)
	m.agentUndoRecords = append(m.agentUndoRecords, rec)
	return nil
}

func (m *MemStore) AgentUndoRecords(_ context.Context, keyID string, since time.Time, limit int) ([]AgentUndoRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []AgentUndoRecord{}
	for _, rec := range m.agentUndoRecords {
		if rec.KeyID == keyID && !rec.CreatedAt.Before(since) {
			out = append(out, rec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) MarkAgentUndone(_ context.Context, id, undoneBy string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.agentUndoRecords {
		rec := &m.agentUndoRecords[i]
		if rec.ID != id {
			continue
		}
		if rec.UndoneAt != nil || len(rec.Inverse) == 0 {
			return false, nil
		}
		at := time.Now().UTC()
		rec.UndoneAt, rec.UndoneBy = &at, undoneBy
		return true, nil
	}
	return false, nil
}

func (m *MemStore) DeleteAgentUndoRecordsBefore(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.agentUndoRecords[:0]
	var n int64
	for _, rec := range m.agentUndoRecords {
		if rec.CreatedAt.Before(before) {
			n++
			continue
		}
		kept = append(kept, rec)
	}
	m.agentUndoRecords = kept
	return n, nil
}
