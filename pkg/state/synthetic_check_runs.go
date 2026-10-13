package state

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SyntheticCheckRun is one probe outcome (ADR-748). ErrorClass is "" for a
// success and otherwise one of the synthetic_check_runs_error_chk values;
// StatusCode is 0 when no response arrived.
type SyntheticCheckRun struct {
	CheckID    string
	StartedAt  time.Time
	OK         bool
	StatusCode int
	LatencyMS  int
	ErrorClass string
}

// RunnableSyntheticCheck is an enabled check on a live app, with what the
// runner needs to build its URL and decide whether it is due.
type RunnableSyntheticCheck struct {
	SyntheticCheck
	AppSlug   string
	LastRunAt time.Time // zero when the check has never run
}

// SyntheticCheckStats summarises a check's runs since a cutoff.
type SyntheticCheckStats struct {
	Runs, OKRuns int64
	P95LatencyMS float64 // over successful runs; 0 when there were none
}

// SyntheticRunStore holds synthetic check run history. Optional on Store.
type SyntheticRunStore interface {
	ListRunnableSyntheticChecks(ctx context.Context) ([]RunnableSyntheticCheck, error)
	RecordSyntheticCheckRun(ctx context.Context, run SyntheticCheckRun) error
	ListSyntheticCheckRuns(ctx context.Context, checkID string, limit int) ([]SyntheticCheckRun, error)
	SyntheticCheckStats(ctx context.Context, checkID string, since time.Time) (SyntheticCheckStats, error)
	PurgeSyntheticCheckRunsBefore(ctx context.Context, before time.Time) (int64, error)
}

func syntheticPgTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func runFromRow(row sqlc.SyntheticCheckRun) SyntheticCheckRun {
	return SyntheticCheckRun{
		CheckID:   uuidString(row.CheckID),
		StartedAt: row.StartedAt.Time.UTC(), OK: row.Ok, StatusCode: int(row.StatusCode),
		LatencyMS: int(row.LatencyMs), ErrorClass: row.ErrorClass,
	}
}

// ListRunnableSyntheticChecks returns every enabled check on a live app.
func (s *PgStore) ListRunnableSyntheticChecks(ctx context.Context) ([]RunnableSyntheticCheck, error) {
	rows, err := sqlc.New().ListRunnableSyntheticChecks(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list runnable synthetic checks: %w", err)
	}
	out := make([]RunnableSyntheticCheck, 0, len(rows))
	for _, row := range rows {
		c := syntheticCheckFromRow(sqlc.SyntheticCheck{
			ID: row.ID, AccountID: row.AccountID, AppID: row.AppID, Name: row.Name, Method: row.Method, Path: row.Path,
			ExpectedStatus: row.ExpectedStatus, TimeoutMs: row.TimeoutMs, IntervalSeconds: row.IntervalSeconds,
			Enabled: row.Enabled, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
		out = append(out, RunnableSyntheticCheck{SyntheticCheck: c, AppSlug: row.AppSlug, LastRunAt: row.LastRunAt.Time.UTC()})
	}
	return out, nil
}

// RecordSyntheticCheckRun stores one run; a duplicate start time is ignored.
func (s *PgStore) RecordSyntheticCheckRun(ctx context.Context, run SyntheticCheckRun) error {
	key, err := alertPgUUID(run.CheckID)
	if err != nil || !key.Valid {
		return ErrInvalidArgument
	}
	err = sqlc.New().InsertSyntheticCheckRun(ctx, s.pool, sqlc.InsertSyntheticCheckRunParams{
		CheckID: key, StartedAt: syntheticPgTime(run.StartedAt), Ok: run.OK,
		StatusCode: int32(run.StatusCode), LatencyMs: int32(min(run.LatencyMS, math.MaxInt32)), ErrorClass: run.ErrorClass, //nolint:gosec // bounded by CHECKs
	})
	if err != nil {
		return fmt.Errorf("state: record synthetic check run for %s: %w", run.CheckID, mapErr(err))
	}
	return nil
}

// ListSyntheticCheckRuns returns a check's most recent runs, newest first.
func (s *PgStore) ListSyntheticCheckRuns(ctx context.Context, checkID string, limit int) ([]SyntheticCheckRun, error) {
	key, err := alertPgUUID(checkID)
	if err != nil || !key.Valid {
		return nil, ErrNotFound
	}
	rows, err := sqlc.New().ListSyntheticCheckRuns(ctx, s.pool, sqlc.ListSyntheticCheckRunsParams{CheckID: key, MaxRows: int32(limit)}) //nolint:gosec // caller-bounded
	if err != nil {
		return nil, fmt.Errorf("state: list synthetic check runs for %s: %w", checkID, err)
	}
	out := make([]SyntheticCheckRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, runFromRow(row))
	}
	return out, nil
}

// SyntheticCheckStats summarises a check's runs since a cutoff.
func (s *PgStore) SyntheticCheckStats(ctx context.Context, checkID string, since time.Time) (SyntheticCheckStats, error) {
	key, err := alertPgUUID(checkID)
	if err != nil || !key.Valid {
		return SyntheticCheckStats{}, ErrNotFound
	}
	row, err := sqlc.New().SyntheticCheckRunStats(ctx, s.pool, sqlc.SyntheticCheckRunStatsParams{CheckID: key, Since: syntheticPgTime(since)})
	if err != nil {
		return SyntheticCheckStats{}, fmt.Errorf("state: synthetic check stats for %s: %w", checkID, err)
	}
	return SyntheticCheckStats{Runs: row.Runs, OKRuns: row.OkRuns, P95LatencyMS: row.P95LatencyMs}, nil
}

// PurgeSyntheticCheckRunsBefore drops runs past retention.
func (s *PgStore) PurgeSyntheticCheckRunsBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := sqlc.New().PurgeSyntheticCheckRunsBefore(ctx, s.pool, syntheticPgTime(before))
	if err != nil {
		return 0, fmt.Errorf("state: purge synthetic check runs: %w", err)
	}
	return n, nil
}

// ListRunnableSyntheticChecks mirrors PgStore.
func (m *MemStore) ListRunnableSyntheticChecks(_ context.Context) ([]RunnableSyntheticCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RunnableSyntheticCheck
	for appID, checks := range m.syntheticChecks {
		app, ok := m.apps[appID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		for _, c := range checks {
			if !c.Enabled {
				continue
			}
			r := RunnableSyntheticCheck{SyntheticCheck: c, AppSlug: app.Slug}
			for _, run := range m.syntheticRuns[c.ID] {
				if run.StartedAt.After(r.LastRunAt) {
					r.LastRunAt = run.StartedAt
				}
			}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RecordSyntheticCheckRun mirrors PgStore, including the foreign key.
func (m *MemStore) RecordSyntheticCheckRun(_ context.Context, run SyntheticCheckRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run.OK != (run.ErrorClass == "") {
		return ErrInvalidArgument
	}
	found := false
	for _, checks := range m.syntheticChecks {
		for _, c := range checks {
			found = found || c.ID == run.CheckID
		}
	}
	if !found {
		return ErrNotFound
	}
	if m.syntheticRuns == nil {
		m.syntheticRuns = map[string][]SyntheticCheckRun{}
	}
	for _, r := range m.syntheticRuns[run.CheckID] {
		if r.StartedAt.Equal(run.StartedAt) {
			return nil
		}
	}
	run.StartedAt = run.StartedAt.UTC()
	m.syntheticRuns[run.CheckID] = append(m.syntheticRuns[run.CheckID], run)
	return nil
}

// ListSyntheticCheckRuns mirrors PgStore.
func (m *MemStore) ListSyntheticCheckRuns(_ context.Context, checkID string, limit int) ([]SyntheticCheckRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]SyntheticCheckRun(nil), m.syntheticRuns[checkID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SyntheticCheckStats mirrors PgStore (nearest-rank p95 rather than
// interpolated; close enough for an in-memory test double).
func (m *MemStore) SyntheticCheckStats(_ context.Context, checkID string, since time.Time) (SyntheticCheckStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var st SyntheticCheckStats
	var lat []int
	for _, r := range m.syntheticRuns[checkID] {
		if r.StartedAt.Before(since) {
			continue
		}
		st.Runs++
		if r.OK {
			st.OKRuns++
			lat = append(lat, r.LatencyMS)
		}
	}
	if len(lat) > 0 {
		sort.Ints(lat)
		st.P95LatencyMS = float64(lat[(len(lat)*95+99)/100-1])
	}
	return st, nil
}

// PurgeSyntheticCheckRunsBefore mirrors PgStore.
func (m *MemStore) PurgeSyntheticCheckRunsBefore(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, runs := range m.syntheticRuns {
		kept := runs[:0]
		for _, r := range runs {
			if r.StartedAt.Before(before) {
				n++
				continue
			}
			kept = append(kept, r)
		}
		m.syntheticRuns[id] = kept
	}
	return n, nil
}

var (
	_ SyntheticRunStore = (*PgStore)(nil)
	_ SyntheticRunStore = (*MemStore)(nil)
)
