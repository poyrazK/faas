// adr: 20261023171905 — the quiet pause ledger proves paused seconds: closed
// pauses are exact, open ones count only up to the node's watermark.
package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgQuietPauseLedger(t *testing.T) {
	ctx := context.Background()
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	node, other := uuid.NewString(), uuid.NewString()
	vm := uuid.NewString()
	minute := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return minute.Add(time.Duration(s * float64(time.Second))) }
	paused := func(start, end time.Time) map[string]state.QuietPausedSeconds {
		t.Helper()
		got, err := store.QuietPausedSeconds(ctx, start, end)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	record := func(nodeID string, covered time.Time, pauses ...state.QuietPause) {
		t.Helper()
		if err := store.RecordQuietPauses(ctx, nodeID, covered, pauses); err != nil {
			t.Fatal(err)
		}
	}

	// Open since :10, node covered to :40: only 30 s are proven, and the
	// pause is unproven for the minute ending at :60.
	record(node, at(40), state.QuietPause{InstanceID: vm, StartedAt: at(10)})
	if got := paused(minute, at(60))[vm]; got != (state.QuietPausedSeconds{Seconds: 30, Unproven: true}) {
		t.Fatalf("open pause, watermark at :40 = %+v, want 30 s unproven", got)
	}
	open, err := store.OpenQuietPauses(ctx, node)
	if err != nil || len(open) != 1 || open[0].InstanceID != vm || !open[0].StartedAt.Equal(at(10)) {
		t.Fatalf("OpenQuietPauses = %+v, %v", open, err)
	}

	// The same open report again, with the watermark past the minute:
	// the pause now counts to the minute's end.
	record(node, at(75), state.QuietPause{InstanceID: vm, StartedAt: at(10)})
	if got := paused(minute, at(60))[vm]; got != (state.QuietPausedSeconds{Seconds: 50}) {
		t.Fatalf("open pause, watermark at :75 = %+v, want 50 s proven", got)
	}

	// Closed at :55. A stale open report and an earlier end change nothing;
	// a watermark from the past does not move it back.
	record(node, at(80), state.QuietPause{InstanceID: vm, StartedAt: at(10), EndedAt: at(55)})
	record(node, at(20), state.QuietPause{InstanceID: vm, StartedAt: at(10)},
		state.QuietPause{InstanceID: uuid.NewString(), StartedAt: at(5), EndedAt: at(6)})
	record(node, at(81), state.QuietPause{InstanceID: vm, StartedAt: at(10), EndedAt: at(30)})
	if got := paused(minute, at(60))[vm]; got != (state.QuietPausedSeconds{Seconds: 45}) {
		t.Fatalf("closed pause = %+v, want 45 s", got)
	}
	if open, err := store.OpenQuietPauses(ctx, node); err != nil || len(open) != 0 {
		t.Fatalf("OpenQuietPauses after close = %+v, %v", open, err)
	}

	// A pause spanning two minutes is split between them; a pause closed on
	// another node needs no watermark there.
	record(node, at(90), state.QuietPause{InstanceID: vm, StartedAt: at(58.5), EndedAt: at(62)})
	if got := paused(minute, at(60))[vm].Seconds; got != 46 {
		t.Fatalf("first minute = %d s, want 46 (45 + 1.5 floored)", got)
	}
	if got := paused(at(60), at(120))[vm].Seconds; got != 2 {
		t.Fatalf("second minute = %d s, want 2", got)
	}
	vm2 := uuid.NewString()
	record(other, time.Time{}.Add(time.Second), state.QuietPause{InstanceID: vm2, StartedAt: at(1), EndedAt: at(11)})
	if got := paused(minute, at(60))[vm2]; got != (state.QuietPausedSeconds{Seconds: 10}) {
		t.Fatalf("closed pause behind a stale watermark = %+v, want 10 s", got)
	}

	// An open pause beyond its node's watermark proves nothing.
	vm3 := uuid.NewString()
	if err := store.RecordQuietPauses(ctx, uuid.NewString(), time.Time{}.Add(time.Second),
		[]state.QuietPause{{InstanceID: vm3, StartedAt: at(1)}}); err != nil {
		t.Fatal(err)
	}
	if got := paused(minute, at(60))[vm3]; got.Seconds != 0 || !got.Unproven {
		t.Fatalf("open pause before its watermark = %+v, want 0 s unproven", got)
	}
}

func TestPgQuietPauseLedgerRejectsInvalidPauses(t *testing.T) {
	ctx := context.Background()
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	now := time.Now().UTC()
	for name, p := range map[string]state.QuietPause{
		"end before start": {InstanceID: uuid.NewString(), StartedAt: now, EndedAt: now.Add(-time.Second)},
		"no start":         {InstanceID: uuid.NewString()},
		"bad instance":     {InstanceID: "vm-a", StartedAt: now},
	} {
		if err := store.RecordQuietPauses(ctx, uuid.NewString(), now, []state.QuietPause{p}); err == nil {
			t.Errorf("%s: RecordQuietPauses accepted %+v", name, p)
		}
	}
}

func TestPgDeleteQuietPausesBefore(t *testing.T) {
	ctx := context.Background()
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	node := uuid.NewString()
	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var pauses []state.QuietPause
	for i := range 3 {
		start := old.Add(time.Duration(i) * time.Minute)
		pauses = append(pauses, state.QuietPause{InstanceID: uuid.NewString(), StartedAt: start, EndedAt: start.Add(time.Second)})
	}
	// A floor-held VM with no traffic stays paused on a node that still
	// reports; a pause that began before the cutoff may end after it.
	openLive := state.QuietPause{InstanceID: uuid.NewString(), StartedAt: old}
	straddling := state.QuietPause{InstanceID: uuid.NewString(), StartedAt: old, EndedAt: old.AddDate(0, 2, 0)}
	recent := state.QuietPause{InstanceID: uuid.NewString(), StartedAt: old.AddDate(0, 2, 0), EndedAt: old.AddDate(0, 2, 0).Add(time.Second)}
	if err := store.RecordQuietPauses(ctx, node, old.AddDate(0, 2, 0), append(pauses, openLive, straddling, recent)); err != nil {
		t.Fatal(err)
	}
	// A VM long gone: its node stopped reporting before the cutoff.
	goneNode := uuid.NewString()
	openGone := state.QuietPause{InstanceID: uuid.NewString(), StartedAt: old}
	if err := store.RecordQuietPauses(ctx, goneNode, old.Add(time.Hour), []state.QuietPause{openGone}); err != nil {
		t.Fatal(err)
	}
	cutoff := old.AddDate(0, 1, 0)
	if n, err := store.DeleteQuietPausesBefore(ctx, cutoff, 3); err != nil || n != 3 {
		t.Fatalf("first batch deleted %d, %v; want 3", n, err)
	}
	if n, err := store.DeleteQuietPausesBefore(ctx, cutoff, 3); err != nil || n != 1 {
		t.Fatalf("second batch deleted %d, %v; want 1, the gone node's open pause", n, err)
	}
	if n, err := store.DeleteQuietPausesBefore(ctx, cutoff, 3); err != nil || n != 0 {
		t.Fatalf("third batch deleted %d, %v; want 0", n, err)
	}
	if open, err := store.OpenQuietPauses(ctx, goneNode); err != nil || len(open) != 0 {
		t.Fatalf("gone node's open pauses after retention = %+v, %v; want none", open, err)
	}
	if open, err := store.OpenQuietPauses(ctx, node); err != nil || len(open) != 1 || open[0].InstanceID != openLive.InstanceID {
		t.Fatalf("reporting node's open pauses after retention = %+v, %v; want %s", open, err, openLive.InstanceID)
	}
	window := old.AddDate(0, 2, 0)
	got, err := store.QuietPausedSeconds(ctx, window.Add(-time.Minute), window.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int64{openLive.InstanceID: 60, straddling.InstanceID: 60, recent.InstanceID: 1} {
		if got[id].Seconds != want {
			t.Errorf("paused seconds of %s after retention = %+v; want %d", id, got[id], want)
		}
	}
}

// queryCapture runs queries on a connection and keeps the last one, so a
// test can EXPLAIN exactly what a generated query sends.
type queryCapture struct {
	*pgxpool.Conn
	sql  string
	args []any
}

func (c *queryCapture) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.sql, c.args = sql, args
	return c.Conn.Query(ctx, sql, args...)
}

// meterd reads the pauses overlapping one closed minute every minute it
// rolls. A pause can stay open arbitrarily long, so the read bounds
// started_at only from above, which the started_at index answers with the
// whole retained ledger. Over 45 days of pauses (the retention before rollout
// step 3, or a retention loop stalled that long) the planner must reach the
// minute through the ended_at index instead.
func TestPgQuietPausedSecondsReadsOnlyNearbyPauses(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	node := uuid.NewString()
	minute := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	// 50,000 closed pauses of 716 instances, one every 77 s back from the
	// minute (44.6 days), and one pause still open.
	if _, err := pool.Exec(ctx, `
INSERT INTO instance_quiet_pauses (instance_id, node_id, started_at, ended_at)
SELECT md5((g % 716)::text)::uuid, $2::uuid,
       $1::timestamptz - g * interval '77 seconds',
       $1::timestamptz - g * interval '77 seconds' + (2 + g % 30) * interval '1 second'
  FROM generate_series(1, 50000) g`, minute, node); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	open := state.QuietPause{InstanceID: uuid.NewString(), StartedAt: minute.Add(-10 * time.Second)}
	if err := store.RecordQuietPauses(ctx, node, minute.Add(2*time.Minute), []state.QuietPause{open}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE instance_quiet_pauses`); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	q := &queryCapture{Conn: conn}
	rows, err := sqlc.New().QuietPausedSeconds(ctx, q, sqlc.QuietPausedSecondsParams{
		WindowStart: pgtype.Timestamptz{Time: minute, Valid: true},
		WindowEnd:   pgtype.Timestamptz{Time: minute.Add(time.Minute), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].InstanceID != open.InstanceID || rows[0].PausedSeconds != 60 {
		t.Fatalf("QuietPausedSeconds = %+v, want the open pause's 60 s", rows)
	}
	planRows, err := conn.Query(ctx, "EXPLAIN "+q.sql, q.args...)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(planRows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n")
	if strings.Contains(plan, "Seq Scan on instance_quiet_pauses") || !strings.Contains(plan, "instance_quiet_pauses_ended_at") {
		t.Fatalf("QuietPausedSeconds does not reach the minute through the ended_at index:\n%s", plan)
	}
}

// The customer-visible record of unbilled quiet pause (rollout step 3) sits
// beside billed usage on the same row and never changes it. Like mb_seconds,
// the first positive value of a minute wins.
func TestPgQuietPausedUsage(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatal(err)
	}
	october := time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)
	november := october.Add(time.Minute)
	steps := []func() error{
		func() error { return s.RecordQuietPausedUsage(ctx, acctID, appID, ins.ID, october, 1000) },
		func() error { return s.AppendUsage(ctx, acctID, appID, ins.ID, october, 500, 1, 0, 0, 0, 0, 0, 0) },
		func() error { return s.RecordQuietPausedUsage(ctx, acctID, appID, ins.ID, october, 2000) },
		func() error { return s.AppendUsage(ctx, acctID, appID, ins.ID, november, 300, 0, 0, 0, 0, 0, 0, 0) },
		func() error {
			return s.RecordQuietPausedUsage(ctx, acctID, appID, ins.ID, november.Add(10*time.Second), 400)
		},
		func() error { return s.RecordQuietPausedUsage(ctx, acctID, appID, ins.ID, november.Add(time.Minute), 0) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	for month, want := range map[time.Time]int64{october: 1000, november: 400} {
		got, err := s.QuietPausedMBSecondsByMonth(ctx, acctID, month)
		if err != nil || got != want {
			t.Errorf("paused MB-seconds in %s = %d, %v; want %d", month.Format("2006-01"), got, err, want)
		}
	}
	if got, err := s.QuietPausedMBSecondsByMonth(ctx, uuid.NewString(), october); err != nil || got != 0 {
		t.Errorf("another account's paused MB-seconds = %d, %v; want 0", got, err)
	}
	rows, err := s.UsageByMonth(ctx, acctID, october)
	if err != nil || len(rows) != 1 || rows[0].MBSeconds != 500 {
		t.Fatalf("billed usage in October = %+v, %v; want one row of 500 MB-seconds", rows, err)
	}
	if err := s.RecordQuietPausedUsage(ctx, "not-a-uuid", appID, ins.ID, october, 1); err == nil {
		t.Fatal("recorded paused usage for an invalid account id")
	}
}
