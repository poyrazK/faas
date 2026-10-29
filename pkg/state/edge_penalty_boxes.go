package state

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// Penalty boxes shared between gateways (ADR-20261022105846 amendment 1).

// EdgePenaltyBoxRetention is how long a box's row outlives the box.
const EdgePenaltyBoxRetention = time.Hour

// EdgePenaltyBox is one gateway's box of a client: the client's IPv4
// address or IPv6 /64, held for app by rule until Until. ID and CreatedAt
// are assigned by the store.
type EdgePenaltyBox struct {
	ID        int64
	AppID     string
	RuleID    string
	Client    netip.Prefix
	Until     time.Time
	CreatedAt time.Time
}

// EdgePenaltyBoxCursor orders boxes by (CreatedAt, ID); the zero cursor
// starts before every box.
type EdgePenaltyBoxCursor struct {
	CreatedAt time.Time
	ID        int64
}

// EdgePenaltyBoxStore is the amendment 1 capability; gatewayd-internal
// type-asserts it.
type EdgePenaltyBoxStore interface {
	// RecordEdgePenaltyBoxes appends boxes and notifies NotifyEdgePenaltyBox
	// in the same statement.
	RecordEdgePenaltyBoxes(ctx context.Context, boxes []EdgePenaltyBox, channel string) error
	// ListEdgePenaltyBoxes returns up to limit unexpired boxes after cursor
	// in cursor order, and the store's current time.
	ListEdgePenaltyBoxes(ctx context.Context, after EdgePenaltyBoxCursor, limit int) ([]EdgePenaltyBox, time.Time, error)
	// PruneEdgePenaltyBoxes deletes boxes that ended before before.
	PruneEdgePenaltyBoxes(ctx context.Context, before time.Time) (int64, error)
}

var (
	_ EdgePenaltyBoxStore = (*PgStore)(nil)
	_ EdgePenaltyBoxStore = (*MemStore)(nil)
)

func (s *PgStore) RecordEdgePenaltyBoxes(ctx context.Context, boxes []EdgePenaltyBox, channel string) error {
	if len(boxes) == 0 {
		return nil
	}
	n := len(boxes)
	apps, rules, clients, until := make([]string, n), make([]string, n), make([]string, n), make([]time.Time, n)
	for i, b := range boxes {
		apps[i], rules[i], clients[i], until[i] = b.AppID, b.RuleID, b.Client.Masked().String(), b.Until.UTC()
	}
	_, err := s.pool.Exec(ctx, `
		with ins as (
			insert into edge_penalty_boxes (app_id, rule_id, client, boxed_until)
			select a, r, c, u from unnest($1::uuid[], $2::uuid[], $3::cidr[], $4::timestamptz[]) as t(a, r, c, u)
			returning 1)
		select pg_notify($5, (select count(*) from ins)::text)`,
		apps, rules, clients, until, channel)
	if err != nil {
		return fmt.Errorf("state: record edge penalty boxes: %w", err)
	}
	return nil
}

func (s *PgStore) ListEdgePenaltyBoxes(ctx context.Context, after EdgePenaltyBoxCursor, limit int) ([]EdgePenaltyBox, time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `select now()`).Scan(&now); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: edge penalty boxes clock: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		select id, app_id::text, rule_id::text, client::text, boxed_until, created_at
		from edge_penalty_boxes
		where (created_at, id) > ($1, $2) and boxed_until > $3
		order by created_at, id
		limit $4`, after.CreatedAt.UTC(), after.ID, now, limit)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("state: list edge penalty boxes: %w", err)
	}
	defer rows.Close()
	var out []EdgePenaltyBox
	for rows.Next() {
		var b EdgePenaltyBox
		var client string
		if err := rows.Scan(&b.ID, &b.AppID, &b.RuleID, &client, &b.Until, &b.CreatedAt); err != nil {
			return nil, time.Time{}, fmt.Errorf("state: scan edge penalty box: %w", err)
		}
		if b.Client, err = netip.ParsePrefix(client); err != nil {
			return nil, time.Time{}, fmt.Errorf("state: edge penalty box client %q: %w", client, err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("state: list edge penalty boxes: %w", err)
	}
	return out, now, nil
}

func (s *PgStore) PruneEdgePenaltyBoxes(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from edge_penalty_boxes where boxed_until < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune edge penalty boxes: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (m *MemStore) RecordEdgePenaltyBoxes(_ context.Context, boxes []EdgePenaltyBox, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.edgePenaltyBoxNow()
	for _, b := range boxes {
		m.edgePenaltyBoxSeq++
		b.ID, b.CreatedAt, b.Client, b.Until = m.edgePenaltyBoxSeq, now, b.Client.Masked(), b.Until.UTC()
		m.edgePenaltyBoxes = append(m.edgePenaltyBoxes, b)
	}
	return nil
}

func edgePenaltyBoxAfter(b EdgePenaltyBox, c EdgePenaltyBoxCursor) bool {
	return b.CreatedAt.After(c.CreatedAt) || (b.CreatedAt.Equal(c.CreatedAt) && b.ID > c.ID)
}

func (m *MemStore) ListEdgePenaltyBoxes(_ context.Context, after EdgePenaltyBoxCursor, limit int) ([]EdgePenaltyBox, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.edgePenaltyBoxNow()
	var out []EdgePenaltyBox
	for _, b := range m.edgePenaltyBoxes {
		if edgePenaltyBoxAfter(b, after) && b.Until.After(now) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return edgePenaltyBoxAfter(out[j], EdgePenaltyBoxCursor{CreatedAt: out[i].CreatedAt, ID: out[i].ID})
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, now, nil
}

func (m *MemStore) PruneEdgePenaltyBoxes(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.edgePenaltyBoxes[:0]
	var n int64
	for _, b := range m.edgePenaltyBoxes {
		if b.Until.Before(before) {
			n++
			continue
		}
		kept = append(kept, b)
	}
	m.edgePenaltyBoxes = kept
	return n, nil
}

// edgePenaltyBoxNow is the MemStore's clock for boxes: SetEdgePenaltyBoxClock
// replaces it in tests.
func (m *MemStore) edgePenaltyBoxNow() time.Time {
	if m.edgePenaltyBoxClock != nil {
		return m.edgePenaltyBoxClock().UTC()
	}
	return time.Now().UTC()
}

// SetEdgePenaltyBoxClock sets the clock MemStore stamps and expires penalty
// boxes with; nil restores the wall clock.
func (m *MemStore) SetEdgePenaltyBoxClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edgePenaltyBoxClock = now
}
