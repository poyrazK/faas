// adr: 20261023171905 — schedd records each node's quiet pauses so billing can
// subtract them, and closes a pause whose end it missed at its last sighting.
package instancestats

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type ledgerWrite struct {
	node    string
	covered time.Time
	pauses  []state.QuietPause
}

// fakePauseStore implements state.QuietPauseLedgerStore only.
type fakePauseStore struct {
	open      map[string][]state.QuietPause
	loadErr   error
	recordErr error
	loads     int
	writes    []ledgerWrite
}

func (f *fakePauseStore) RecordQuietPauses(_ context.Context, node string, covered time.Time, pauses []state.QuietPause) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.writes = append(f.writes, ledgerWrite{node: node, covered: covered, pauses: append([]state.QuietPause{}, pauses...)})
	return nil
}

func (f *fakePauseStore) OpenQuietPauses(_ context.Context, node string) ([]state.QuietPause, error) {
	f.loads++
	return f.open[node], f.loadErr
}

func (f *fakePauseStore) QuietPausedSeconds(context.Context, time.Time, time.Time) (map[string]state.QuietPausedSeconds, error) {
	return nil, nil
}

func (f *fakePauseStore) DeleteQuietPausesBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

var ledgerT0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func ts(sec int) time.Time { return ledgerT0.Add(time.Duration(sec) * time.Second) }

func pausedRow(instance string, sampled, since int) InstanceStat {
	return InstanceStat{InstanceID: instance, NodeID: "n1", SampledAt: ts(sampled), QuietPaused: true,
		QuietPause: sched.QuietPauseRecord{Since: ts(since)}}
}

func runningRow(instance string, sampled int) InstanceStat {
	return InstanceStat{InstanceID: instance, NodeID: "n1", SampledAt: ts(sampled)}
}

// withLast adds a completed pause [start, end] to row.
func withLast(row InstanceStat, start, end int) InstanceStat {
	row.QuietPause.LastStarted, row.QuietPause.LastEnded = ts(start), ts(end)
	return row
}

func open(instance string, start int) state.QuietPause {
	return state.QuietPause{InstanceID: instance, StartedAt: ts(start)}
}

func closed(instance string, start, end int) state.QuietPause {
	return state.QuietPause{InstanceID: instance, StartedAt: ts(start), EndedAt: ts(end)}
}

func TestPauseLedgerRecordsReports(t *testing.T) {
	type step struct {
		rows []InstanceStat
		want *ledgerWrite // nil: no write this step
	}
	tests := []struct {
		name  string
		open  []state.QuietPause // ledger state a restarted schedd loads
		steps []step
	}{
		{
			name: "a pause opens, then its completed report closes it",
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 8)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{open("a", 8)}}},
				{rows: []InstanceStat{pausedRow("a", 20, 8)}, want: &ledgerWrite{covered: ts(20), pauses: []state.QuietPause{}}},
				{rows: []InstanceStat{withLast(runningRow("a", 30), 8, 25)}, want: &ledgerWrite{covered: ts(30), pauses: []state.QuietPause{closed("a", 8, 25)}}},
				// vmmd repeats the completed pause until the next one ends.
				{rows: []InstanceStat{withLast(runningRow("a", 40), 8, 25)}, want: &ledgerWrite{covered: ts(40), pauses: []state.QuietPause{}}},
			},
		},
		{
			name: "a projected report seen again is skipped",
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 8)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{open("a", 8)}}},
				{rows: []InstanceStat{pausedRow("a", 10, 8)}},
			},
		},
		{
			name: "a pause that ended unseen is closed at its last sighting",
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 8)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{open("a", 8)}}},
				{rows: []InstanceStat{pausedRow("a", 20, 8)}, want: &ledgerWrite{covered: ts(20), pauses: []state.QuietPause{}}},
				// Two pauses later: [8, ?] ended unseen, [22, 26] completed, 28 is open.
				{rows: []InstanceStat{withLast(pausedRow("a", 30, 28), 22, 26)}, want: &ledgerWrite{covered: ts(30),
					pauses: []state.QuietPause{closed("a", 8, 20), closed("a", 22, 26), open("a", 28)}}},
			},
		},
		{
			name: "a VM missing from its node's report closes its open pause",
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 8), runningRow("b", 10)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{open("a", 8)}}},
				{rows: []InstanceStat{runningRow("b", 20)}, want: &ledgerWrite{covered: ts(20), pauses: []state.QuietPause{closed("a", 8, 10)}}},
			},
		},
		{
			name: "a new VM under the same id ends the old one's pause",
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 8)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{open("a", 8)}}},
				{rows: []InstanceStat{runningRow("a", 20)}, want: &ledgerWrite{covered: ts(20), pauses: []state.QuietPause{closed("a", 8, 10)}}},
			},
		},
		{
			name: "after a restart a still-open pause is kept and an ended one bills from its start",
			open: []state.QuietPause{open("a", 5), open("b", 6)},
			steps: []step{
				{rows: []InstanceStat{pausedRow("a", 10, 5), runningRow("b", 10)}, want: &ledgerWrite{covered: ts(10),
					pauses: []state.QuietPause{closed("b", 6, 6)}}},
				{rows: []InstanceStat{withLast(runningRow("a", 20), 5, 15), runningRow("b", 20)}, want: &ledgerWrite{covered: ts(20),
					pauses: []state.QuietPause{closed("a", 5, 15)}}},
			},
		},
		{
			name: "a completed pause after a restart keeps its exact end",
			open: []state.QuietPause{open("a", 5)},
			steps: []step{
				{rows: []InstanceStat{withLast(runningRow("a", 10), 5, 9)}, want: &ledgerWrite{covered: ts(10), pauses: []state.QuietPause{closed("a", 5, 9)}}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakePauseStore{open: map[string][]state.QuietPause{"n1": tc.open}}
			ledger := NewPauseLedger(store, 0, nil, nil)
			for i, s := range tc.steps {
				before := len(store.writes)
				ledger.Observe(t.Context(), s.rows, s.rows[0].SampledAt)
				if s.want == nil {
					if len(store.writes) != before {
						t.Fatalf("step %d: unexpected write %+v", i, store.writes[len(store.writes)-1])
					}
					continue
				}
				if len(store.writes) != before+1 {
					t.Fatalf("step %d: writes = %d, want one more than %d", i, len(store.writes), before)
				}
				got := store.writes[len(store.writes)-1]
				s.want.node = "n1"
				if !reflect.DeepEqual(got, *s.want) {
					t.Fatalf("step %d: write = %+v\nwant %+v", i, got, *s.want)
				}
			}
			if store.loads != 1 {
				t.Fatalf("open pauses loaded %d times, want once", store.loads)
			}
		})
	}
}

func TestPauseLedgerBatchesAndRetries(t *testing.T) {
	store := &fakePauseStore{}
	ledger := NewPauseLedger(store, 2*time.Second, nil, nil)
	ctx := t.Context()

	ledger.Observe(ctx, []InstanceStat{pausedRow("a", 1, 0)}, ts(1))
	// Within flushEvery: queued, not written.
	ledger.Observe(ctx, []InstanceStat{withLast(runningRow("a", 2), 0, 2)}, ts(2))
	if len(store.writes) != 1 {
		t.Fatalf("writes = %d after a report inside the flush interval, want 1", len(store.writes))
	}
	// A failed write keeps the queue and the watermark.
	store.recordErr = errors.New("database down")
	ledger.Observe(ctx, []InstanceStat{pausedRow("a", 4, 3)}, ts(4))
	if len(store.writes) != 1 {
		t.Fatalf("a failed write was recorded")
	}
	store.recordErr = nil
	ledger.Observe(ctx, []InstanceStat{pausedRow("a", 6, 3)}, ts(6))
	want := ledgerWrite{node: "n1", covered: ts(6), pauses: []state.QuietPause{closed("a", 0, 2), open("a", 3)}}
	if got := store.writes[len(store.writes)-1]; !reflect.DeepEqual(got, want) {
		t.Fatalf("retried write = %+v\nwant %+v", got, want)
	}
}

func TestPauseLedgerWaitsForOpenPauses(t *testing.T) {
	store := &fakePauseStore{loadErr: errors.New("database down")}
	ledger := NewPauseLedger(store, 0, nil, nil)
	ledger.Observe(t.Context(), []InstanceStat{pausedRow("a", 10, 8)}, ts(10))
	if len(store.writes) != 0 {
		t.Fatal("wrote a report before the node's open pauses were loaded")
	}
	store.loadErr = nil
	ledger.Observe(t.Context(), []InstanceStat{pausedRow("a", 20, 8)}, ts(20))
	want := ledgerWrite{node: "n1", covered: ts(20), pauses: []state.QuietPause{open("a", 8)}}
	if len(store.writes) != 1 || !reflect.DeepEqual(store.writes[0], want) {
		t.Fatalf("writes = %+v, want %+v", store.writes, want)
	}
}

// withTotal sets vmmd's completed paused total, in seconds, on row.
func withTotal(row InstanceStat, sec int) InstanceStat {
	total := time.Duration(sec) * time.Second
	row.QuietPause.PausedTotal = &total
	return row
}

// The ledger counts the paused time vmmd measured between reports, the
// reference meterd's subtracted seconds must stay under: completed pauses
// plus open pause time, from each instance's first report on, with a new
// incarnation's time counted whole.
func TestPauseLedgerMeasuresVMMDPausedTime(t *testing.T) {
	metrics := NewPauseLedgerMetrics(nil)
	ledger := NewPauseLedger(&fakePauseStore{}, 0, nil, metrics)
	steps := []struct {
		rows []InstanceStat
		want float64 // measured seconds so far
	}{
		// First sighting: the baseline, nothing counted.
		{[]InstanceStat{pausedRow("a", 10, 8)}, 0},
		// Still paused: 10 more open seconds.
		{[]InstanceStat{pausedRow("a", 20, 8)}, 10},
		// Resumed at :25: the total holds the whole pause, 5 s are new.
		{[]InstanceStat{withTotal(withLast(runningRow("a", 30), 8, 25), 17)}, 15},
		// Paused again since :35.
		{[]InstanceStat{withTotal(withLast(pausedRow("a", 40, 35), 8, 25), 17)}, 20},
		// A new incarnation (smaller total): its 2 paused seconds are new.
		{[]InstanceStat{pausedRow("a", 50, 48)}, 22},
		// Gone from the report, then seen again: a new baseline.
		{[]InstanceStat{runningRow("b", 60)}, 22},
		{[]InstanceStat{pausedRow("a", 70, 65)}, 22},
		{[]InstanceStat{pausedRow("a", 80, 65)}, 32},
	}
	for i, step := range steps {
		ledger.Observe(t.Context(), step.rows, step.rows[0].SampledAt)
		if got := testutil.ToFloat64(metrics.measuredS.WithLabelValues("n1")); got != step.want {
			t.Fatalf("step %d: measured = %v s, want %v", i, got, step.want)
		}
	}
}

func TestPauseLedgerNilIsInert(t *testing.T) {
	var ledger *PauseLedger
	ledger.Observe(t.Context(), []InstanceStat{pausedRow("a", 10, 8)}, ts(10))
}

// timestamptzPauseStore keeps pauses the way PostgreSQL does: keyed by
// instance and started_at at microsecond precision, refusing a write that
// touches one row twice (ON CONFLICT DO UPDATE cannot affect a row twice).
type timestamptzPauseStore struct {
	fakePauseStore
	rows map[pauseKey]time.Time
}

func (s *timestamptzPauseStore) RecordQuietPauses(ctx context.Context, node string, covered time.Time, pauses []state.QuietPause) error {
	touched := make(map[pauseKey]bool, len(pauses))
	for _, p := range pauses {
		k := pauseKey{instance: p.InstanceID, start: p.StartedAt.Truncate(time.Microsecond)}
		if touched[k] {
			return errors.New("ON CONFLICT DO UPDATE command cannot affect row a second time")
		}
		touched[k] = true
	}
	if s.rows == nil {
		s.rows = make(map[pauseKey]time.Time)
	}
	for _, p := range pauses {
		k := pauseKey{instance: p.InstanceID, start: p.StartedAt.Truncate(time.Microsecond)}
		end := p.EndedAt.Truncate(time.Microsecond)
		if prev, ok := s.rows[k]; !ok || (!end.IsZero() && (prev.IsZero() || end.After(prev))) {
			s.rows[k] = end
		}
	}
	return s.fakePauseStore.RecordQuietPauses(ctx, node, covered, pauses)
}

func (s *timestamptzPauseStore) OpenQuietPauses(_ context.Context, node string) ([]state.QuietPause, error) {
	s.loads++
	var open []state.QuietPause
	for k, end := range s.rows {
		if end.IsZero() {
			open = append(open, state.QuietPause{InstanceID: k.instance, StartedAt: k.start})
		}
	}
	return open, nil
}

// vmmd stamps a pause's start in nanoseconds; the ledger reads it back in
// microseconds after a schedd restart. The still-open pause must match its
// next report instead of being closed and reopened under the same key, which
// failed every later write for the node.
func TestPauseLedgerRestartMatchesNanosecondStarts(t *testing.T) {
	start := ledgerT0.Add(8*time.Second + 123_456_789*time.Nanosecond)
	end := start.Add(17*time.Second + 999*time.Nanosecond)
	paused := func(sampled int) InstanceStat {
		return InstanceStat{InstanceID: "a", NodeID: "n1", SampledAt: ts(sampled), QuietPaused: true,
			QuietPause: sched.QuietPauseRecord{Since: start}}
	}
	store := &timestamptzPauseStore{}
	ctx := t.Context()
	NewPauseLedger(store, 0, nil, nil).Observe(ctx, []InstanceStat{paused(10)}, ts(10))

	restarted := NewPauseLedger(store, 0, nil, nil)
	restarted.Observe(ctx, []InstanceStat{paused(20)}, ts(20))
	ran := runningRow("a", 30)
	ran.QuietPause.LastStarted, ran.QuietPause.LastEnded = start, end
	restarted.Observe(ctx, []InstanceStat{ran}, ts(30))

	if len(store.writes) != 3 {
		t.Fatalf("writes = %d, want one per report (a failed write is not recorded)", len(store.writes))
	}
	if got := store.writes[1].pauses; len(got) != 0 {
		t.Fatalf("report after the restart wrote %+v, want nothing: the pause is still the one recorded", got)
	}
	want := map[pauseKey]time.Time{{instance: "a", start: start.Truncate(time.Microsecond)}: end.Truncate(time.Microsecond)}
	if !reflect.DeepEqual(store.rows, want) {
		t.Fatalf("ledger rows = %v, want %v", store.rows, want)
	}
}
