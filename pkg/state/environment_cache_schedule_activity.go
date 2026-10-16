package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EnvironmentCacheScheduleActivityReader exposes only the selected immutable target.
type EnvironmentCacheScheduleActivityReader interface {
	GetEnvironmentCacheScheduleActivity(context.Context, string, string, string, string, int) (api.EnvironmentCacheScheduleActivity, error)
}

func cacheScheduleEvent(v api.EnvironmentCacheSchedule, kind string) api.EnvironmentCacheScheduleEvent {
	e := api.EnvironmentCacheScheduleEvent{ScheduleID: v.ScheduleID, Kind: kind, Status: v.Status, Action: "policy", ExecuteAt: v.ExecuteAt, OccurredAt: time.Now().UTC()}
	if v.Purge != nil {
		e.Action = "purge"
	} else if v.Policy != nil && v.Policy.Inherited {
		e.Action = "inheritance"
	}
	if kind == "accepted" {
		e.RelatedScheduleID = v.ReplacesScheduleID
	}
	if kind == "rescheduled" {
		e.RelatedScheduleID = v.ReplacedByScheduleID
	}
	if kind == "failed" {
		e.ErrorCode = v.ErrorCode
	}
	if kind == "executed" {
		if v.PolicyResult != nil {
			revision := v.PolicyResult.Revision
			e.PolicyRevision = &revision
		}
		if v.PurgeResult != nil {
			generation := v.PurgeResult.Generation
			e.PurgeGeneration = &generation
		}
	}
	return e
}
func appendCacheScheduleEvent(ctx context.Context, tx pgx.Tx, account string, v api.EnvironmentCacheSchedule, kind string) error {
	payload, err := json.Marshal(cacheScheduleEvent(v, kind))
	if err != nil {
		return err
	}
	return sqlc.New().AppendEnvironmentCacheScheduleEvent(ctx, tx, sqlc.AppendEnvironmentCacheScheduleEventParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(v.AppID), ProjectID: mustPgUUID(v.ProjectID), EnvironmentID: mustPgUUID(v.EnvironmentID), Scope: v.Scope, Payload: payload})
}
func (m *MemStore) appendCacheScheduleEventLocked(account string, v api.EnvironmentCacheSchedule, kind string) {
	if m.environmentCacheScheduleEvents == nil {
		m.environmentCacheScheduleEvents = map[string][]api.EnvironmentCacheScheduleEvent{}
	}
	key := account + "/" + v.AppID + "/" + v.EnvironmentID
	e := cacheScheduleEvent(v, kind)
	e.Sequence = m.environmentCacheScheduleEventFloors[key] + int64(len(m.environmentCacheScheduleEvents[key])) + 1
	m.environmentCacheScheduleEvents[key] = append(m.environmentCacheScheduleEvents[key], e)
}
func scheduleActivityPage(current api.EnvironmentCachePolicy, c api.EnvironmentCacheActivityCursor, entries []api.EnvironmentCacheScheduleEvent, limit int) api.EnvironmentCacheScheduleActivity {
	out := api.EnvironmentCacheScheduleActivity{AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Scope: current.Scope, Entries: entries}
	if len(entries) > limit {
		out.HasMore = true
		out.Entries = entries[:limit]
	}
	if len(out.Entries) > 0 {
		c.Sequence = out.Entries[len(out.Entries)-1].Sequence
	}
	out.Cursor = api.EncodeEnvironmentCacheActivityCursor(c)
	return out
}
func (s *PgStore) GetEnvironmentCacheScheduleActivity(ctx context.Context, account, app, scope, after string, limit int) (api.EnvironmentCacheScheduleActivity, error) {
	var out api.EnvironmentCacheScheduleActivity
	limit, err := api.NormalizeEnvironmentCacheActivityPage(after, limit)
	if err != nil {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	c, err := api.ParseEnvironmentCacheActivityCursor(after, account, app, current.ProjectID, current.EnvironmentID)
	if err != nil {
		return out, ErrInvalidArgument
	}
	q := sqlc.New()
	bounds, err := q.ReadEnvironmentCacheScheduleEventBounds(ctx, tx, sqlc.ReadEnvironmentCacheScheduleEventBoundsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID)})
	if err != nil {
		return out, err
	}
	head := bounds.Head
	if c.Sequence < bounds.Floor {
		return out, expiredScheduleActivity(current, c, bounds.Floor, head)
	}
	if c.Sequence > head {
		return out, ErrInvalidArgument
	}
	rows, err := q.ReadEnvironmentCacheScheduleEvents(ctx, tx, sqlc.ReadEnvironmentCacheScheduleEventsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID), AfterSequence: c.Sequence, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, err
	}
	entries := make([]api.EnvironmentCacheScheduleEvent, 0, len(rows))
	sequence := c.Sequence
	for _, row := range rows {
		var e api.EnvironmentCacheScheduleEvent
		if err = json.Unmarshal(row.Event, &e); err != nil {
			return out, err
		}
		if row.Scope != scope {
			return out, ErrRouteHealthRevision
		}
		if e.Sequence != sequence+1 {
			return out, ErrConflict
		}
		sequence = e.Sequence
		entries = append(entries, e)
	}
	if sequence < head && len(entries) <= limit {
		return out, ErrConflict
	}
	out = scheduleActivityPage(current, c, entries, limit)
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEnvironmentCacheScheduleActivity(_ context.Context, account, app, scope, after string, limit int) (api.EnvironmentCacheScheduleActivity, error) {
	var out api.EnvironmentCacheScheduleActivity
	limit, err := api.NormalizeEnvironmentCacheActivityPage(after, limit)
	if err != nil {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	c, err := api.ParseEnvironmentCacheActivityCursor(after, account, app, current.ProjectID, current.EnvironmentID)
	if err != nil {
		return out, ErrInvalidArgument
	}
	key := account + "/" + app + "/" + current.EnvironmentID
	rows := m.environmentCacheScheduleEvents[key]
	floor := m.environmentCacheScheduleEventFloors[key]
	head := floor + int64(len(rows))
	if c.Sequence < floor {
		return out, expiredScheduleActivity(current, c, floor, head)
	}
	if c.Sequence > head {
		return out, ErrInvalidArgument
	}
	entries := make([]api.EnvironmentCacheScheduleEvent, 0, limit+1)
	for _, e := range rows {
		if e.Sequence > c.Sequence {
			raw, _ := json.Marshal(e)
			var copy api.EnvironmentCacheScheduleEvent
			_ = json.Unmarshal(raw, &copy)
			entries = append(entries, copy)
			if len(entries) > limit {
				break
			}
		}
	}
	return scheduleActivityPage(current, c, entries, limit), nil
}
