package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WakeTimelinePageStore is optional to keep the legacy Store contract stable.
// App scoping and canonical boot selection precede keyset filtering/limiting.
type WakeTimelinePageStore interface {
	ListAppWakeTimelinePage(context.Context, string, string, time.Time, WakeTimelinePosition, int) ([]Event, error)
}

type WakeTimelinePosition struct {
	At time.Time
	ID int64
}

func (s *PgStore) ListAppWakeTimelinePage(ctx context.Context, appID, wakeID string, since time.Time, after WakeTimelinePosition, limit int) ([]Event, error) {
	if limit < 1 || limit > api.WakeTimelinePageMax+1 || after.ID < 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.appErrorsQueries().ListAppWakeTimelinePage(ctx, s.pool, sqlc.ListAppWakeTimelinePageParams{
		AppID: appID, WakeID: wakeID, Since: pgtype.Timestamptz{Time: since, Valid: true}, AfterAt: pgtype.Timestamptz{Time: after.At, Valid: true}, AfterID: after.ID, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		e := Event{ID: row.ID, At: row.At.Time, Actor: row.Actor, Kind: row.Kind, Data: json.RawMessage(row.Data)}
		out = append(out, e)
	}
	return out, nil
}

func (m *MemStore) ListAppWakeTimelinePage(_ context.Context, appID, wakeID string, since time.Time, after WakeTimelinePosition, limit int) ([]Event, error) {
	if limit < 1 || limit > api.WakeTimelinePageMax+1 || after.ID < 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Event{}
	for _, e := range m.events {
		var p struct {
			AppID  string `json:"app_id"`
			WakeID string `json:"wake_id"`
		}
		if json.Unmarshal(e.Data, &p) == nil && p.AppID == appID && p.WakeID == wakeID {
			e.At = e.At.UTC().Truncate(time.Microsecond)
			out = append(out, e)
		}
	}
	sortWakeTimelineEvents(out)
	out = deduplicateWakeBootStarted(out)
	page := make([]Event, 0, min(len(out), limit))
	for _, e := range out {
		if !e.At.After(since) || after.ID > 0 && (e.At.Before(after.At) || e.At.Equal(after.At) && e.ID <= after.ID) {
			continue
		}
		page = append(page, e)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}
