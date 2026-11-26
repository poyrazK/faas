package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLastCompleteWeek(t *testing.T) {
	monday := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), monday},
		{time.Date(2026, 11, 26, 15, 4, 0, 0, time.UTC), monday},
		{time.Date(2026, 11, 29, 23, 59, 0, 0, time.UTC), monday},
		{time.Date(2026, 11, 22, 23, 59, 0, 0, time.UTC), monday.AddDate(0, 0, -7)},
		// A time in another zone is read in UTC.
		{time.Date(2026, 11, 23, 1, 0, 0, 0, time.FixedZone("UTC+2", 2*3600)), monday.AddDate(0, 0, -7)},
	} {
		if got := lastCompleteWeek(tc.now); !got.Equal(tc.want) {
			t.Errorf("lastCompleteWeek(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}

// awakeTestStore records what the notice pass hands an AwakeNoticeStore.
type awakeTestStore struct {
	*MemoryStore
	weeks   []time.Time
	since   []time.Time
	notices []AwakeNotice
	err     error
}

func (s *awakeTestStore) RecordAwakeNotices(_ context.Context, weekStart time.Time, minAwakeSeconds int64, minCausePercent int) (int64, error) {
	if minAwakeSeconds != int64(api.ManagedPostgresAwakeNoticeHoursPerDay)*7*3600 || minCausePercent != api.ManagedPostgresAwakeNoticeCauseSharePercent {
		return 0, errors.New("unexpected thresholds")
	}
	s.weeks = append(s.weeks, weekStart)
	return 0, s.err
}

func (s *awakeTestStore) UnmailedAwakeNotices(_ context.Context, since time.Time, _ int) ([]AwakeNotice, error) {
	s.since = append(s.since, since)
	return s.notices, nil
}

// The pass records last week's notices and hands each unmailed one to the
// mailer at most hourly; a mailer error moves on to the next notice, and a
// record error mails nothing.
func TestUsageCollectorNoticesAwakeHourly(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	store := &awakeTestStore{MemoryStore: NewMemoryStore(), notices: []AwakeNotice{{DatabaseID: "a"}, {DatabaseID: "b"}}}
	var mailed []string
	c := &UsageCollector{store: store, now: func() time.Time { return now }, logger: slog.New(slog.DiscardHandler),
		notifyAwake: func(_ context.Context, n AwakeNotice) error {
			mailed = append(mailed, n.DatabaseID)
			return errors.New("outbox full")
		}}
	ctx := context.Background()
	c.noticeAwake(ctx)
	now = now.Add(59 * time.Minute)
	c.noticeAwake(ctx)
	if len(store.weeks) != 1 || !store.weeks[0].Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || len(mailed) != 2 {
		t.Fatalf("weeks %v, mailed %v; want one pass for both notices", store.weeks, mailed)
	}
	if want := now.Add(-59 * time.Minute).Add(-api.InternalMailManagedPostgresAwakeTTL); !store.since[0].Equal(want) {
		t.Fatalf("listed since %s, want %s", store.since[0], want)
	}
	store.err = errors.New("down")
	now = now.Add(time.Minute)
	c.noticeAwake(ctx)
	if len(store.weeks) != 2 || len(mailed) != 2 {
		t.Fatalf("weeks %v, mailed %v; want a second pass that mails nothing", store.weeks, mailed)
	}

	// Without a mailer nothing is recorded.
	idle := &awakeTestStore{MemoryStore: NewMemoryStore()}
	(&UsageCollector{store: idle, now: func() time.Time { return now }, logger: slog.New(slog.DiscardHandler)}).noticeAwake(ctx)
	if len(idle.weeks) != 0 {
		t.Fatalf("recorded %v with no mailer", idle.weeks)
	}
}

// A database is noticed when it was awake long enough over the week and one
// cause, other than requests, held enough of its starts; once a week, listed
// until mailed.
func TestAwakeNoticesPGRecordOncePerWeek(t *testing.T) {
	s, pool, ctx, accountID := postgresStoreFixture(t)
	week := lastCompleteWeek(time.Now())
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: accountID, Slug: "aw-" + uuid.NewString()[:8], MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	type start struct {
		cause      string
		from, hrs  float64
		detail     string
		withoutApp bool
	}
	database := func(scaleToZero bool, starts ...start) string {
		t.Helper()
		db := postgresReadyDatabase(t, s, accountID, "aw-"+uuid.NewString()[:8], week.Add(-48*time.Hour))
		if _, err := pool.Exec(ctx, "UPDATE managed_postgres_databases SET scale_to_zero=$2 WHERE id=$1", db.ID, scaleToZero); err != nil {
			t.Fatal(err)
		}
		for _, st := range starts {
			at := week.Add(time.Duration(st.from * float64(time.Hour)))
			var appID any = app.ID
			if st.withoutApp {
				appID = nil
			}
			if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_compute_starts
				(database_id,account_id,provider_operation_id,started_at,suspended_at,cause,cause_app_id,cause_detail) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				db.ID, accountID, "op-"+uuid.NewString(), at, at.Add(time.Duration(st.hrs*float64(time.Hour))), st.cause, appID, st.detail); err != nil {
				t.Fatal(err)
			}
		}
		return db.ID
	}
	cron := func(from float64) start { return start{cause: CauseCron, from: from, hrs: 15, detail: "/cron/sync"} }
	// 45 h of cron and the hour of a start that began before the week: 46 h.
	noticed := database(true, cron(0), cron(24), cron(48), start{cause: CauseExternal, from: -0.5, hrs: 1.5, withoutApp: true})
	// No cause holds half the starts.
	database(true, cron(0), start{cause: CauseExternal, from: 24, hrs: 15, withoutApp: true}, start{cause: CauseAppWake, from: 48, hrs: 15, withoutApp: true})
	// Requests are the app's own traffic.
	database(true, start{cause: CauseRequest, from: 0, hrs: 50})
	// Not a scale-to-zero database.
	database(false, cron(0), cron(24), cron(48))
	// Awake too little.
	database(true, cron(0), start{cause: CauseCron, from: 24, hrs: 1, detail: "/cron/sync"})

	minAwake := int64(api.ManagedPostgresAwakeNoticeHoursPerDay) * 7 * 3600
	if n, err := s.RecordAwakeNotices(ctx, week, minAwake, api.ManagedPostgresAwakeNoticeCauseSharePercent); err != nil || n != 1 {
		t.Fatalf("recorded %d, %v; want only the cron database", n, err)
	}
	if n, err := s.RecordAwakeNotices(ctx, week, minAwake, api.ManagedPostgresAwakeNoticeCauseSharePercent); err != nil || n != 0 {
		t.Fatalf("second pass recorded %d, %v; want none", n, err)
	}
	notices, err := s.UnmailedAwakeNotices(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	var mine []AwakeNotice
	for _, n := range notices {
		if n.AccountID == accountID {
			mine = append(mine, n)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("notices = %+v", mine)
	}
	got := mine[0]
	if got.DatabaseID != noticed || !got.WeekStart.Equal(week) || got.AwakeSeconds != 46*3600 || got.AwakeHoursPerDay() != 6 || got.Starts != 4 ||
		got.Cause != CauseCron || got.CauseStarts != 3 || got.AppSlug != app.Slug || got.Detail != "/cron/sync" || got.DatabaseName == "" {
		t.Fatalf("notice = %+v", got)
	}
	if _, err := pool.Exec(ctx, "UPDATE managed_postgres_awake_notices SET mailed_at=now() WHERE database_id=$1", noticed); err != nil {
		t.Fatal(err)
	}
	if notices, err := s.UnmailedAwakeNotices(ctx, time.Now().Add(-time.Hour), 10); err != nil {
		t.Fatal(err)
	} else {
		for _, n := range notices {
			if n.DatabaseID == noticed {
				t.Fatalf("mailed notice listed: %+v", n)
			}
		}
	}
	if _, err := s.UnmailedAwakeNotices(ctx, time.Now(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit 0: %v", err)
	}
}
