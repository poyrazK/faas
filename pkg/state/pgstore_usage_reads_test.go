package state_test

// PgStore coverage gap tests for the usage-read surface.
//
// This file covers Store methods that had no PgStore test before slice 6:
//
//   UsageByMonth:    round-trip, empty, TZ boundary across UTC months.
//   UsageByHour:     grouping + sum, half-open range (minute >= start
//                    AND minute < end).
//   UsageByAccount:  all-since-zero, since-filter.
//
// All fixture timestamps use time.Date(..., time.UTC) literals per
// memory pkg-state-usage-monthly-tz-compare.md. Never feed time.Now()
// without .UTC() — the underlying date_trunc('month', $::timestamptz)
// query is anchored in the session timezone, and a non-UTC host
// would silently split a single calendar month across two rows.
//
// Helpers reused: pgStore(t), seedLiveDeploy(t,s,ctx), createApp(t,s,ctx,...),
// CreateInstance (needs a real instance row to satisfy the FK on
// usage_minutes.instance_id).

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// appendUsage inserts a single usage_minutes row via the Store API
// (the only writer path in production). Instance id is taken from
// seedLiveDeploy's instance — see TestPg_AppendUsage_IdempotentSameMinute
// at pgstore_append_usage_test.go:17 for the same shape.
func appendUsage(t *testing.T, s *state.PgStore, ctx context.Context, acctID, appID, insID string, minute time.Time, mbSec, reqs int64) {
	t.Helper()
	if err := s.AppendUsage(ctx, acctID, appID, insID, minute, mbSec, reqs, 0); err != nil {
		t.Fatalf("AppendUsage(%s, %d): %v", minute.Format(time.RFC3339), mbSec, err)
	}
}

// --- UsageByMonth ---------------------------------------------------------

// TestPg_UsageByMonth_RoundTrip pins the happy path: a single appended
// minute in a known UTC month reads back via UsageByMonth with the
// mb_seconds and requests fields populated. The TZ-boundary branch
// (date_trunc('month', $::timestamptz)) is the load-bearing SQL.
func TestPg_UsageByMonth_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	minute := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, minute, 30_720, 5)

	rows, err := s.UsageByMonth(ctx, acctID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].MBSeconds != 30_720 {
		t.Errorf("MBSeconds = %d, want 30720", rows[0].MBSeconds)
	}
	if rows[0].Requests != 5 {
		t.Errorf("Requests = %d, want 5", rows[0].Requests)
	}
	if rows[0].AppID != appID {
		t.Errorf("AppID = %q, want %q", rows[0].AppID, appID)
	}
}

// TestPg_UsageByMonth_NoneForAccountReturnsEmpty pins the
// account-isolation branch: a fresh account with no usage rows must
// return an empty slice, not a SQL error.
func TestPg_UsageByMonth_NoneForAccountReturnsEmpty(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))

	rows, err := s.UsageByMonth(ctx, acctID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 (fresh account)", len(rows))
	}
}

// TestPg_UsageByMonth_RespectsMonthBoundaryAcrossTZ pins the load-bearing
// date_trunc('month', $::timestamptz) branch: two usage rows on
// either side of a UTC month boundary must be returned separately
// when querying each month. A non-UTC session timezone would silently
// fold July 31 23:30 into August's bucket (or vice-versa, depending
// on sign) — see pkg-state-usage-monthly-tz-compare.md.
func TestPg_UsageByMonth_RespectsMonthBoundaryAcrossTZ(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// One row in July (last hour), one in August (first hour).
	julyLate := time.Date(2026, 7, 31, 23, 30, 0, 0, time.UTC)
	augustEarly := time.Date(2026, 8, 1, 0, 30, 0, 0, time.UTC)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, julyLate, 15_360, 1)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, augustEarly, 15_360, 2)

	// Query July — only julyLate must come back.
	july, err := s.UsageByMonth(ctx, acctID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("UsageByMonth(July): %v", err)
	}
	if len(july) != 1 {
		t.Fatalf("july rows = %d, want 1", len(july))
	}
	if july[0].Requests != 1 {
		t.Errorf("july Requests = %d, want 1 (julyLate row)", july[0].Requests)
	}
	// Query August — only augustEarly must come back.
	august, err := s.UsageByMonth(ctx, acctID, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("UsageByMonth(August): %v", err)
	}
	if len(august) != 1 {
		t.Fatalf("august rows = %d, want 1", len(august))
	}
	if august[0].Requests != 2 {
		t.Errorf("august Requests = %d, want 2 (augustEarly row)", august[0].Requests)
	}
}

// --- UsageByHour ----------------------------------------------------------

// TestPg_UsageByHour_GroupsAndSums pins the GROUP BY (account_id,
// app_id, hour) + sum(mb_seconds)::bigint aggregation. We append two
// minutes in the same hour and assert UsageByHour returns one row
// with the sums.
func TestPg_UsageByHour_GroupsAndSums(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	hourStart := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, hourStart, 15_360, 1)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, hourStart.Add(30*time.Minute), 15_360, 2)

	rows, err := s.UsageByHour(ctx, acctID,
		hourStart,
		hourStart.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].MBSeconds != 30_720 {
		t.Errorf("MBSeconds = %d, want 30720 (15_360 + 15_360)", rows[0].MBSeconds)
	}
	if rows[0].Requests != 3 {
		t.Errorf("Requests = %d, want 3 (1 + 2)", rows[0].Requests)
	}
}

// TestPg_UsageByHour_HalfOpenRange pins the half-open range
// (minute >= start AND minute < end). A row exactly at `start` must
// be included; a row exactly at `end` must NOT be included.
func TestPg_UsageByHour_HalfOpenRange(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	t0 := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute) // == start, must be included
	t2 := t0.Add(time.Hour)   // == end, must be EXCLUDED
	appendUsage(t, s, ctx, acctID, appID, ins.ID, t1, 1024, 1)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, t2, 2048, 1)

	// Window [t0, t1+H): t1 is included, t2 is not.
	rows, err := s.UsageByHour(ctx, acctID, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (only t1 in [t0, t0+H))", len(rows))
	}
	if rows[0].MBSeconds != 1024 {
		t.Errorf("MBSeconds = %d, want 1024 (t1 only)", rows[0].MBSeconds)
	}
}

// --- UsageByAccount -------------------------------------------------------

// TestPg_UsageByAccount_AllRowsSinceZero pins the since.IsZero()
// branch: UsageByAccount with a zero `since` returns ALL rows for
// the account, regardless of date.
func TestPg_UsageByAccount_AllRowsSinceZero(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Seed rows in two distinct UTC months so we exercise the GROUP BY
	// (account_id, app_id, month) aggregation.
	mJuly := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	mAug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, mJuly, 30_720, 1)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, mAug, 30_720, 1)

	rows, err := s.UsageByAccount(ctx, acctID, time.Time{})
	if err != nil {
		t.Fatalf("UsageByAccount(since=zero): %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (one per UTC month)", len(rows))
	}

	// Verify total across the two rows.
	var total int64
	for _, r := range rows {
		total += r.MBSeconds
	}
	if total != 61_440 {
		t.Errorf("total MBSeconds = %d, want 61440 (30720 + 30720)", total)
	}
}

// TestPg_UsageByAccount_SinceFiltersOlderRows pins the
// `minute >= $2` WHERE branch: a `since` between the two months must
// return only the newer month's row.
func TestPg_UsageByAccount_SinceFiltersOlderRows(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	mJuly := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	mAug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, mJuly, 30_720, 1)
	appendUsage(t, s, ctx, acctID, appID, ins.ID, mAug, 30_720, 1)

	// since = August 1: the July row is filtered out.
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.UsageByAccount(ctx, acctID, since)
	if err != nil {
		t.Fatalf("UsageByAccount(since=Aug1): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (August only)", len(rows))
	}
	if rows[0].Month.Month() != time.August {
		t.Errorf("Month = %s, want August", rows[0].Month.Month())
	}
}
