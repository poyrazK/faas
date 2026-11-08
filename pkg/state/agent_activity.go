package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AgentActivity is one write request an agent key made
// (ADR-20261107220000).
type AgentActivity struct {
	ID         string
	KeyID      string
	AccountID  string
	OrgID      string
	Method     string
	Path       string
	Status     int
	ApprovalID string // the approval request it went through on, if any
	CreatedAt  time.Time
}

// AgentActivityCursor pages a key's activity: rows strictly older than
// (At, ID). The zero cursor starts at the newest row.
type AgentActivityCursor struct {
	At time.Time
	ID string
}

// AgentActivityStore keeps agent keys' write requests for
// api.AgentActivityRetention. apid is the only writer.
type AgentActivityStore interface {
	RecordAgentActivity(ctx context.Context, row AgentActivity) error
	// ListAgentActivity returns the key's rows created at or after since,
	// newest first, before the cursor, at most limit.
	ListAgentActivity(ctx context.Context, keyID string, since time.Time, before AgentActivityCursor, limit int) ([]AgentActivity, error)
	// DeleteAgentActivityBefore deletes at most batch rows created before
	// cutoff and reports how many.
	DeleteAgentActivityBefore(ctx context.Context, cutoff time.Time, batch int) (int64, error)
}

// maxCursor is past every row, for the first page.
var (
	maxCursorTime = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	maxCursorID   = "ffffffff-ffff-ffff-ffff-ffffffffffff"
)

func (c AgentActivityCursor) bounds() (time.Time, string) {
	if c.At.IsZero() {
		return maxCursorTime, maxCursorID
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return c.At, maxCursorID
	}
	return c.At, c.ID
}

func (s *PgStore) RecordAgentActivity(ctx context.Context, row AgentActivity) error {
	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	return mapErr(sqlc.New().InsertAgentActivity(ctx, s.pool, sqlc.InsertAgentActivityParams{
		ID: mustPgUUID(row.ID), KeyID: mustPgUUID(row.KeyID), AccountID: mustPgUUID(row.AccountID),
		OrgID: mustPgUUID(row.OrgID), Method: row.Method, Path: row.Path, Status: int32(row.Status),
		ApprovalID: mustPgUUID(row.ApprovalID),
	}))
}

func (s *PgStore) ListAgentActivity(ctx context.Context, keyID string, since time.Time, before AgentActivityCursor, limit int) ([]AgentActivity, error) {
	if uuid.Validate(keyID) != nil { // not a key id: no rows
		return []AgentActivity{}, nil //nolint:nilerr // a malformed key id has no activity, not an error
	}
	at, id := before.bounds()
	rows, err := sqlc.New().ListAgentActivity(ctx, s.pool, sqlc.ListAgentActivityParams{
		KeyID: mustPgUUID(keyID), Since: pgTime(since), BeforeAt: pgTime(at), BeforeID: mustPgUUID(id), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AgentActivity, 0, len(rows))
	for _, r := range rows {
		out = append(out, AgentActivity{
			ID: pgUUIDString(r.ID), KeyID: pgUUIDString(r.KeyID), AccountID: pgUUIDString(r.AccountID), OrgID: pgUUIDString(r.OrgID),
			Method: r.Method, Path: r.Path, Status: int(r.Status), ApprovalID: pgUUIDString(r.ApprovalID), CreatedAt: r.CreatedAt.Time,
		})
	}
	return out, nil
}

func (s *PgStore) DeleteAgentActivityBefore(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	n, err := sqlc.New().DeleteAgentActivityBefore(ctx, s.pool, sqlc.DeleteAgentActivityBeforeParams{Cutoff: pgTime(cutoff), Batch: int32(batch)})
	return n, mapErr(err)
}

// --- MemStore ---------------------------------------------------------------

func (m *MemStore) RecordAgentActivity(_ context.Context, row AgentActivity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	m.agentActivity = append(m.agentActivity, row)
	return nil
}

func (m *MemStore) ListAgentActivity(_ context.Context, keyID string, since time.Time, before AgentActivityCursor, limit int) ([]AgentActivity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, id := before.bounds()
	out := []AgentActivity{}
	for _, row := range m.agentActivity {
		if row.KeyID != keyID || row.CreatedAt.Before(since) {
			continue
		}
		if row.CreatedAt.After(at) || row.CreatedAt.Equal(at) && row.ID >= id {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) DeleteAgentActivityBefore(_ context.Context, cutoff time.Time, batch int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.agentActivity[:0]
	var n int64
	for _, row := range m.agentActivity {
		if row.CreatedAt.Before(cutoff) && n < int64(batch) {
			n++
			continue
		}
		kept = append(kept, row)
	}
	m.agentActivity = kept
	return n, nil
}
