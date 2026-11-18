package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// PlatformProbeWake is a wake caused only by a baseline health probe
// (ADR-20261116120000 §3): the gateway that admitted it records when the
// wake started and when the probe's response ended.
type PlatformProbeWake struct {
	InstanceID  string
	AppID       string
	WokeAt      time.Time
	ProbeDoneAt time.Time
}

// PlatformProbeWakeStore is the probe wake ledger: gatewayd writes it,
// schedd parks probe-only instances early from it and meterd leaves the
// probe's seconds unbilled. Optional on Store.
type PlatformProbeWakeStore interface {
	// RecordPlatformProbeWake records one wake; a repeat of the same
	// (instance, woke_at) is ignored. ErrInvalidArgument for an empty
	// instance or a probe that ended before the wake started, ErrNotFound
	// for an unknown app.
	RecordPlatformProbeWake(ctx context.Context, w PlatformProbeWake) error
	// RecentPlatformProbeWakes returns each instance's latest probe end at
	// or after since.
	RecentPlatformProbeWakes(ctx context.Context, since time.Time) (map[string]time.Time, error)
	// PlatformProbeUnbilledSeconds returns each instance's seconds in
	// [start, end) covered by a wake window [woke_at, probe_done_at+tail].
	PlatformProbeUnbilledSeconds(ctx context.Context, start, end time.Time, tail time.Duration) (map[string]int64, error)
	// DeletePlatformProbeWakesBefore deletes up to limit wakes whose probe
	// ended before cutoff and reports how many.
	DeletePlatformProbeWakesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

func validPlatformProbeWake(w PlatformProbeWake) bool {
	return w.InstanceID != "" && len(w.InstanceID) <= 128 && w.AppID != "" &&
		!w.WokeAt.IsZero() && !w.ProbeDoneAt.Before(w.WokeAt)
}

// overlapSeconds is the whole seconds [a0, a1) and [b0, b1) share.
func overlapSeconds(a0, a1, b0, b1 time.Time) int64 {
	lo, hi := a0, a1
	if b0.After(lo) {
		lo = b0
	}
	if b1.Before(hi) {
		hi = b1
	}
	if !hi.After(lo) {
		return 0
	}
	return int64(hi.Sub(lo) / time.Second)
}

// RecordPlatformProbeWake implements PlatformProbeWakeStore.
func (s *PgStore) RecordPlatformProbeWake(ctx context.Context, w PlatformProbeWake) error {
	if !validPlatformProbeWake(w) {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		insert into platform_probe_wakes (instance_id, app_id, woke_at, probe_done_at)
		values ($1, $2::uuid, $3, $4)
		on conflict (instance_id, woke_at) do nothing`,
		w.InstanceID, w.AppID, w.WokeAt.UTC(), w.ProbeDoneAt.UTC())
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: record platform probe wake: %w", err)
	}
	return nil
}

// RecentPlatformProbeWakes implements PlatformProbeWakeStore.
func (s *PgStore) RecentPlatformProbeWakes(ctx context.Context, since time.Time) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `
		select instance_id, max(probe_done_at)
		  from platform_probe_wakes
		 where probe_done_at >= $1
		 group by instance_id`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("state: recent platform probe wakes: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var done time.Time
		if err := rows.Scan(&id, &done); err != nil {
			return nil, fmt.Errorf("state: scan platform probe wake: %w", err)
		}
		out[id] = done.UTC()
	}
	return out, rows.Err()
}

// PlatformProbeUnbilledSeconds implements PlatformProbeWakeStore.
func (s *PgStore) PlatformProbeUnbilledSeconds(ctx context.Context, start, end time.Time, tail time.Duration) (map[string]int64, error) {
	out := map[string]int64{}
	if !start.Before(end) {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select instance_id, woke_at, probe_done_at
		  from platform_probe_wakes
		 where woke_at < $2 and probe_done_at + $3::interval > $1`, start.UTC(), end.UTC(), tail)
	if err != nil {
		return nil, fmt.Errorf("state: platform probe unbilled seconds: %w", err)
	}
	defer rows.Close()
	var wakes []PlatformProbeWake
	for rows.Next() {
		var w PlatformProbeWake
		if err := rows.Scan(&w.InstanceID, &w.WokeAt, &w.ProbeDoneAt); err != nil {
			return nil, fmt.Errorf("state: scan platform probe wake: %w", err)
		}
		wakes = append(wakes, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sumProbeWindows(wakes, start, end, tail), nil
}

// sumProbeWindows adds each instance's wake windows' overlap with
// [start, end), merging windows that overlap each other so no second is
// counted twice.
func sumProbeWindows(wakes []PlatformProbeWake, start, end time.Time, tail time.Duration) map[string]int64 {
	byInstance := map[string][]PlatformProbeWake{}
	for _, w := range wakes {
		byInstance[w.InstanceID] = append(byInstance[w.InstanceID], w)
	}
	out := map[string]int64{}
	for id, ws := range byInstance {
		sort.Slice(ws, func(i, j int) bool { return ws[i].WokeAt.Before(ws[j].WokeAt) })
		var total int64
		var curStart, curEnd time.Time
		for i, w := range ws {
			wEnd := w.ProbeDoneAt.Add(tail)
			if i > 0 && !w.WokeAt.After(curEnd) {
				if wEnd.After(curEnd) {
					curEnd = wEnd
				}
				continue
			}
			if i > 0 {
				total += overlapSeconds(curStart, curEnd, start, end)
			}
			curStart, curEnd = w.WokeAt, wEnd
		}
		total += overlapSeconds(curStart, curEnd, start, end)
		if total > 0 {
			out[id] = total
		}
	}
	return out
}

// DeletePlatformProbeWakesBefore implements PlatformProbeWakeStore.
func (s *PgStore) DeletePlatformProbeWakesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		delete from platform_probe_wakes
		 where (instance_id, woke_at) in (select instance_id, woke_at from platform_probe_wakes
		                                   where probe_done_at < $1 limit $2)`, cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("state: delete platform probe wakes: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RecordPlatformProbeWake mirrors PgStore.
func (m *MemStore) RecordPlatformProbeWake(_ context.Context, w PlatformProbeWake) error {
	if !validPlatformProbeWake(w) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[w.AppID]; !ok {
		return ErrNotFound
	}
	w.WokeAt, w.ProbeDoneAt = w.WokeAt.UTC(), w.ProbeDoneAt.UTC()
	for _, existing := range m.platformProbeWakes {
		if existing.InstanceID == w.InstanceID && existing.WokeAt.Equal(w.WokeAt) {
			return nil
		}
	}
	m.platformProbeWakes = append(m.platformProbeWakes, w)
	return nil
}

// RecentPlatformProbeWakes mirrors PgStore.
func (m *MemStore) RecentPlatformProbeWakes(_ context.Context, since time.Time) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]time.Time{}
	for _, w := range m.platformProbeWakes {
		if w.ProbeDoneAt.Before(since) {
			continue
		}
		if w.ProbeDoneAt.After(out[w.InstanceID]) {
			out[w.InstanceID] = w.ProbeDoneAt
		}
	}
	return out, nil
}

// PlatformProbeUnbilledSeconds mirrors PgStore.
func (m *MemStore) PlatformProbeUnbilledSeconds(_ context.Context, start, end time.Time, tail time.Duration) (map[string]int64, error) {
	if !start.Before(end) {
		return map[string]int64{}, nil
	}
	m.mu.Lock()
	var wakes []PlatformProbeWake
	for _, w := range m.platformProbeWakes {
		if w.WokeAt.Before(end) && w.ProbeDoneAt.Add(tail).After(start) {
			wakes = append(wakes, w)
		}
	}
	m.mu.Unlock()
	return sumProbeWindows(wakes, start, end, tail), nil
}

// DeletePlatformProbeWakesBefore mirrors PgStore.
func (m *MemStore) DeletePlatformProbeWakesBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	kept := m.platformProbeWakes[:0]
	for _, w := range m.platformProbeWakes {
		if w.ProbeDoneAt.Before(cutoff) && n < int64(limit) {
			n++
			continue
		}
		kept = append(kept, w)
	}
	m.platformProbeWakes = kept
	return n, nil
}
