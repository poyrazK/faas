package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// WakeHistoryPageStore is an optional read capability; Store stays unchanged.
type WakeHistoryPageStore interface {
	ListAppWakeHistoryPage(context.Context, string, time.Time, time.Time, WakeHistoryPosition, int) ([]WakeHistoryRecord, error)
}

type WakeHistoryPosition struct {
	At time.Time
	ID string
}
type WakeHistoryRecord struct {
	InstanceID, AppID, WakeID, State string
	StartedAt, BootAt, CompletedAt   time.Time
	BootData                         json.RawMessage
	HasMeta                          bool
}

func (s *PgStore) ListAppWakeHistoryPage(ctx context.Context, appID string, since, until time.Time, after WakeHistoryPosition, limit int) ([]WakeHistoryRecord, error) {
	id, err := uuid.Parse(appID)
	if err != nil || limit < 1 || limit > api.WakeHistoryPageMax+1 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.appErrorsQueries().ListAppWakeHistoryPage(ctx, s.pool, sqlc.ListAppWakeHistoryPageParams{
		AppID: pgtype.UUID{Bytes: id, Valid: true}, Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: until, Valid: true}, AfterAt: pgtype.Timestamptz{Time: after.At, Valid: true}, AfterID: after.ID, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]WakeHistoryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, WakeHistoryRecord{InstanceID: r.InstanceID, AppID: r.AppID, WakeID: r.WakeID, State: r.State, StartedAt: r.StartedAt.Time, BootData: r.BootData, BootAt: r.BootAt.Time, CompletedAt: r.CompletedAt.Time, HasMeta: r.HasMeta})
	}
	return out, nil
}

func (m *MemStore) ListAppWakeHistoryPage(_ context.Context, appID string, since, until time.Time, after WakeHistoryPosition, limit int) ([]WakeHistoryRecord, error) {
	if limit < 1 || limit > api.WakeHistoryPageMax+1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []WakeHistoryRecord{}
	for _, ins := range m.instances {
		at := ins.StartedAt.UTC().Truncate(time.Microsecond)
		if ins.AppID != appID || at.IsZero() || at.Before(since) || at.After(until) || after.ID != "" && (at.After(after.At) || at.Equal(after.At) && ins.ID >= after.ID) {
			continue
		}
		rows = append(rows, WakeHistoryRecord{InstanceID: ins.ID, AppID: appID, WakeID: ins.WakeID, State: ins.State, StartedAt: at})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartedAt.Equal(rows[j].StartedAt) {
			return rows[i].InstanceID > rows[j].InstanceID
		}
		return rows[i].StartedAt.After(rows[j].StartedAt)
	})
	rows = rows[:min(len(rows), limit)]
	for i := range rows {
		r := &rows[i]
		var boot Event
		for _, e := range m.events {
			var p struct {
				AppID  string `json:"app_id"`
				WakeID string `json:"wake_id"`
			}
			if json.Unmarshal(e.Data, &p) != nil || p.AppID != appID || p.WakeID != r.WakeID {
				continue
			}
			e.At = e.At.UTC().Truncate(time.Microsecond)
			if e.Kind == "wake.boot_started" && (!r.HasMeta || preferHistoryBoot(e, boot)) {
				boot = e
				r.BootAt = e.At
				r.BootData = append(json.RawMessage(nil), e.Data...)
				r.HasMeta = true
			}
			if e.Kind == "wake.boot_completed" && (r.CompletedAt.IsZero() || e.At.Before(r.CompletedAt)) {
				r.CompletedAt = e.At
			}
		}
	}
	return rows, nil
}

// Match SQL ordering even when two legacy observations have different actors.
func preferHistoryBoot(candidate, current Event) bool {
	if (candidate.Actor == "schedd") != (current.Actor == "schedd") {
		return candidate.Actor == "schedd"
	}
	if candidate.At.Equal(current.At) {
		return candidate.ID < current.ID
	}
	return candidate.At.Before(current.At)
}
