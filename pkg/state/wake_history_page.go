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
		m.fillWakeHistoryMetaLocked(&rows[i])
	}
	return rows, nil
}

// fillWakeHistoryMetaLocked adds the canonical boot and first completion of
// r's wake, matching the SQL lateral reads.
func (m *MemStore) fillWakeHistoryMetaLocked(r *WakeHistoryRecord) {
	var boot Event
	for _, e := range m.events {
		var p struct {
			AppID  string `json:"app_id"`
			WakeID string `json:"wake_id"`
		}
		if json.Unmarshal(e.Data, &p) != nil || p.AppID != r.AppID || p.WakeID != r.WakeID {
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

// WakeHistoryByWakeIDsStore reads specific wakes for the account trace view.
// It is optional so the Store contract stays unchanged.
type WakeHistoryByWakeIDsStore interface {
	ListWakeHistoryByWakeIDs(ctx context.Context, appIDs, wakeIDs []string) ([]WakeHistoryRecord, error)
}

// ListWakeHistoryByWakeIDs returns one record per known wake in wakeIDs whose
// instance belongs to one of appIDs: the wake's first instance, its canonical
// boot and its first completion. Unknown wakes are omitted.
func (s *PgStore) ListWakeHistoryByWakeIDs(ctx context.Context, appIDs, wakeIDs []string) ([]WakeHistoryRecord, error) {
	apps, err := wakeHistoryUUIDs(appIDs)
	if err != nil {
		return nil, err
	}
	wakes, err := wakeHistoryUUIDs(wakeIDs)
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 || len(wakes) == 0 {
		return nil, nil
	}
	rows, err := s.appErrorsQueries().ListWakeHistoryByWakeIDs(ctx, s.pool, sqlc.ListWakeHistoryByWakeIDsParams{AppIds: apps, WakeIds: wakes})
	if err != nil {
		return nil, err
	}
	out := make([]WakeHistoryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, WakeHistoryRecord{InstanceID: r.InstanceID, AppID: r.AppID, WakeID: r.WakeID, State: r.State, StartedAt: r.StartedAt.Time, BootData: r.BootData, BootAt: r.BootAt.Time, CompletedAt: r.CompletedAt.Time, HasMeta: r.HasMeta})
	}
	return out, nil
}

func (m *MemStore) ListWakeHistoryByWakeIDs(_ context.Context, appIDs, wakeIDs []string) ([]WakeHistoryRecord, error) {
	if _, err := wakeHistoryUUIDs(appIDs); err != nil {
		return nil, err
	}
	if _, err := wakeHistoryUUIDs(wakeIDs); err != nil {
		return nil, err
	}
	apps := map[string]bool{}
	for _, id := range appIDs {
		apps[id] = true
	}
	wakes := map[string]bool{}
	for _, id := range wakeIDs {
		wakes[id] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	first := map[string]WakeHistoryRecord{}
	for _, ins := range m.instances {
		at := ins.StartedAt.UTC().Truncate(time.Microsecond)
		if !apps[ins.AppID] || !wakes[ins.WakeID] || at.IsZero() {
			continue
		}
		if held, ok := first[ins.WakeID]; ok && (held.StartedAt.Before(at) || held.StartedAt.Equal(at) && held.InstanceID < ins.ID) {
			continue
		}
		first[ins.WakeID] = WakeHistoryRecord{InstanceID: ins.ID, AppID: ins.AppID, WakeID: ins.WakeID, State: ins.State, StartedAt: at}
	}
	rows := make([]WakeHistoryRecord, 0, len(first))
	for _, r := range first {
		m.fillWakeHistoryMetaLocked(&r)
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartedAt.Equal(rows[j].StartedAt) {
			return rows[i].WakeID < rows[j].WakeID
		}
		return rows[i].StartedAt.Before(rows[j].StartedAt)
	})
	return rows, nil
}

func wakeHistoryUUIDs(ids []string) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		out = append(out, pgtype.UUID{Bytes: parsed, Valid: true})
	}
	return out, nil
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
