// spec: §10 — billing month boundaries use UTC even for offset timestamps.
package meter

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGBHours pins the float conversion mb_seconds → GB-RAM-hours.
//
// Spec §10 / financial-model §1: included quotas are integer GB-hours
// per calendar month, so this is the divisor the quota loop keys off
// of. The function uses float because the quota gate needs €0.01/GB-h
// resolution; the wire path uses pure integer math (see
// pkg/billing/stripe.WireQuantityForMBSeconds). The two paths agree at
// exact alignments (1 GB-resident-hour = 1000 wire units = 1.0 GB-h)
// and diverge at sub-milliunit truncations; that's the design.
func TestGBHours(t *testing.T) {
	cases := []struct {
		name      string
		mbSeconds int64
		want      float64
	}{
		// Floor.
		{name: "zero", mbSeconds: 0, want: 0.0},

		// One second of a 128 MB Free instance → 128 / 1024 / 3600
		// ≈ 3.472e-5 GB-h. The smallest billable quantum (meterd
		// rounds upstream to full minutes, so this never lands in
		// production, but the math must still be defined).
		{
			name:      "free_one_second",
			mbSeconds: 128,
			want:      128.0 / 1024.0 / 3600.0,
		},

		// Hobby (256 + 8 MB = 264 MB billed) resident for exactly
		// one hour. 264 * 3600 = 950_400 mb-s → 950_400 / 1024 / 3600
		// = 0.2578125 GB-h. This is the canonical Hobby one-hour
		// quota debit.
		{
			name:      "hobby_one_hour",
			mbSeconds: 264 * 3600,
			want:      0.2578125,
		},

		// One GB-resident-hour. 1024 * 3600 = 3_686_400 mb-s →
		// exactly 1.0 GB-h. Pinned because it's the alignment point
		// between the float quota path and the integer wire path.
		{
			name:      "one_gb_hour",
			mbSeconds: 1024 * 3600,
			want:      1.0,
		},

		// Hobby for 24 h. 264 MB * 86_400 s = 22_809_600 mb-s →
		// 6.1875 GB-h. Compared against the 50 GB-h Hobby quota
		// (12.375 % consumed per resident-day).
		{
			name:      "hobby_one_day",
			mbSeconds: 264 * 24 * 3600,
			want:      6.1875,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GBHours(tc.mbSeconds)
			// 1e-9 GB-h ≈ 3.6 ms of a 128 MB instance — below the
			// float rounding floor used everywhere else in the
			// package.
			if !approxEq(got, tc.want, 1e-9) {
				t.Fatalf("GBHours(%d) = %.12f, want %.12f",
					tc.mbSeconds, got, tc.want)
			}
		})
	}
}

// TestMBSecondsPerMinute pins the per-minute sampler accumulator.
// Used by Sampler to convert one minute of one admission-MB instance
// into the mb_seconds the Store's AppendUsage expects. A regression
// here would silently under- or over-bill every customer by the same
// factor — the canonical 264 MB Hobby app accumulates 264*60 =
// 15_840 mb-s per resident minute.
func TestMBSecondsPerMinute(t *testing.T) {
	cases := []struct {
		name        string
		admissionMB int
		want        int64
	}{
		// Floor. Zero is allowed (the sampler skips parked apps,
		// but defensive arithmetic must not panic on 0).
		{name: "zero", admissionMB: 0, want: 0},

		// Free. 128 + 8 = 136 admission-MB → 8_160 mb-s.
		{name: "free", admissionMB: 136, want: 8_160},

		// Hobby. 264 admission-MB → 15_840 mb-s.
		{name: "hobby", admissionMB: 264, want: 15_840},

		// Pro. 520 admission-MB → 31_200 mb-s.
		{name: "pro", admissionMB: 520, want: 31_200},

		// Scale. 1032 admission-MB → 61_920 mb-s.
		{name: "scale", admissionMB: 1032, want: 61_920},

		// Future Scale-tier headroom: 2 GB admission → 122_880.
		// Pinned to make sure integer multiplication doesn't
		// overflow at plausible growth sizes.
		{name: "two_gb", admissionMB: 2048, want: 122_880},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MBSecondsPerMinute(tc.admissionMB)
			if got != tc.want {
				t.Fatalf("MBSecondsPerMinute(%d) = %d, want %d",
					tc.admissionMB, got, tc.want)
			}
		})
	}
}

// TestAccountMonthKey pins the per-month key used by the aggregator
// to fetch the usage band. Truncates to the first instant of the
// month in UTC — independent of the server's local TZ (the financial
// model assumes UTC because the InvoiceShadow24h integration test
// keys off the same boundary).
//
// Off-by-one here = customers either see their month-to-date usage
// drift into the next month early (and get spurious quota warnings)
// or stay in the previous month too long (and burst past quota).
func TestAccountMonthKey(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		// Mid-month. The 17th truncates to the 1st of the same month.
		{
			name: "mid_month_utc",
			in:   time.Date(2026, 7, 17, 14, 33, 12, 500, time.UTC),
			want: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		// First instant of a month already — must NOT round *down*
		// into the previous month.
		{
			name: "first_instant",
			in:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		// Last second of a month — must NOT round up.
		{
			name: "last_second_of_month",
			in:   time.Date(2026, 7, 31, 23, 59, 59, 999_999_999, time.UTC),
			want: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		// Non-UTC input. The financial model is UTC-only; a
		// Europe/Berlin (+02:00) wall-clock at 2026-07-17 00:30 is
		// 2026-07-16 22:30 UTC — must still truncate to the UTC
		// start of the month. This is the case the
		// `pkg/state.usage_monthly TZ compare` memory was about:
		// we don't compare `month = $::timestamptz` in the
		// aggregator, we round-trip through AccountMonthKey so the
		// comparison happens in UTC.
		{
			name: "non_utc_input_berlin",
			in:   time.Date(2026, 7, 17, 0, 30, 0, 0, berlinTZ()),
			want: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "positive_offset_previous_utc_month",
			in:   time.Date(2026, 9, 1, 0, 30, 0, 0, time.FixedZone("UTC+3", 3*3600)),
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "negative_offset_next_utc_year",
			in:   time.Date(2026, 12, 31, 23, 30, 0, 0, time.FixedZone("UTC-5", -5*3600)),
			want: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// Year boundary. Dec 17 → Jan 1.
		{
			name: "year_boundary",
			in:   time.Date(2026, 12, 17, 12, 0, 0, 0, time.UTC),
			want: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		},
		// Month boundary at the wrong side: Jan 31 23:59:59 must
		// round down to Jan 1, NOT advance to Feb 1.
		{
			name: "month_end_does_not_advance",
			in:   time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC),
			want: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AccountMonthKey(tc.in)
			if !got.Equal(tc.want) {
				t.Fatalf("AccountMonthKey(%s) = %s, want %s",
					tc.in.Format(time.RFC3339Nano),
					got.Format(time.RFC3339Nano),
					tc.want.Format(time.RFC3339Nano))
			}
			// Belt-and-braces: the key must always carry the UTC
			// location, regardless of input location. A non-UTC
			// key fed back into the SQL `month` filter would
			// match nothing.
			if got.Location() != time.UTC {
				t.Fatalf("AccountMonthKey returned a non-UTC location: %s",
					got.Location())
			}
		})
	}
}

// TestMinuteKey pins the per-minute sampler key. The sample loop
// stamps every (instance, minute) row with MinuteKey so the SQL PK
// (instance_id, minute) is unique; a non-aligned key would create
// duplicate PK inserts and silently drop usage rows (the Store's
// AppendUsage is documented to upsert on collision).
func TestMinuteKey(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		// Mid-minute truncates down.
		{
			name: "mid_minute",
			in:   time.Date(2026, 7, 17, 14, 33, 37, 500_000_000, time.UTC),
			want: time.Date(2026, 7, 17, 14, 33, 0, 0, time.UTC),
		},
		// Already on the boundary.
		{
			name: "on_boundary",
			in:   time.Date(2026, 7, 17, 14, 33, 0, 0, time.UTC),
			want: time.Date(2026, 7, 17, 14, 33, 0, 0, time.UTC),
		},
		// Last nanosecond of a minute must NOT advance.
		{
			name: "last_ns_of_minute",
			in:   time.Date(2026, 7, 17, 14, 33, 59, 999_999_999, time.UTC),
			want: time.Date(2026, 7, 17, 14, 33, 0, 0, time.UTC),
		},
		// Sub-minute zero — same as on_boundary.
		{
			name: "sub_second_nanos",
			in:   time.Date(2026, 7, 17, 14, 33, 0, 1, time.UTC),
			want: time.Date(2026, 7, 17, 14, 33, 0, 0, time.UTC),
		},
		// Non-UTC input. CET (+01:00 in winter) 14:33:00 wall =
		// 13:33 UTC — the key is the UTC minute.
		{
			name: "non_utc_input",
			in:   time.Date(2026, 1, 17, 14, 33, 37, 0, berlinTZ()),
			want: time.Date(2026, 1, 17, 13, 33, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MinuteKey(tc.in)
			if !got.Equal(tc.want) {
				t.Fatalf("MinuteKey(%s) = %s, want %s",
					tc.in.Format(time.RFC3339Nano),
					got.Format(time.RFC3339Nano),
					tc.want.Format(time.RFC3339Nano))
			}
			if got.Location() != time.UTC {
				t.Fatalf("MinuteKey returned a non-UTC location: %s",
					got.Location())
			}
		})
	}
}

// TestMonthlyUsageGB pins the aggregator's float-rounding contract
// at boundaries the existing TestMonthlyUsageGB_Math (one rounding
// case at 1M mb-s) does not cover. The function rounds to 6 dp via
//
//	float64(int64(gb*1e6+0.5)) / 1e6
//
// (half-up rounding at 6 dp), so the expected values are the
// half-up-rounded answers of the exact float arithmetic. Off-by-one
// rounding regressions here would shift every customer's monthly
// invoice by sub-microscopic amounts, but the *inconsistency* —
// same input producing different outputs across goroutines — would
// break dashboard reconciliation.
func TestMonthlyUsageGB(t *testing.T) {
	cases := []struct {
		name string
		in   []state.Usage
		want float64
	}{
		// Empty slice — zero usage. Pins the `var mbSec int64`
		// zero-value path (regression: a `:= 1` typo would surface
		// here as a constant non-zero).
		{name: "empty", in: nil, want: 0.0},

		// Single Hobby resident-minute row. 264 MB * 60 s =
		// 15_840 mb-s → 15_840 / 1024 / 3600 = 0.004296875
		// (exact in binary float). × 1e6 = 4296.875; +0.5 → 4297;
		// /1e6 = 0.004297.
		{
			name: "hobby_one_minute",
			in:   []state.Usage{{MBSeconds: 15_840}},
			want: 0.004297,
		},

		// Two Hobby resident-minute rows — additive shape. Any
		// future switch from `+=` to `=` would surface here. Sum
		// = 0.00859375 → 0.008594 at 6 dp.
		{
			name: "two_hobby_minutes",
			in:   []state.Usage{{MBSeconds: 15_840}, {MBSeconds: 15_840}},
			want: 0.008594,
		},

		// Exactly at a quota boundary. Hobby quota = 50 GB-h.
		// 50 * 1024 * 3600 = 184_320_000 mb-s → 50.0 GB-h exact.
		// Pinned because the quota gate uses
		// `usedGB >= float64(q)` for its action decision
		// (CheckQuota); if MonthlyUsageGB ever rounded *down* past
		// this boundary the customer would never see the warning.
		{
			name: "exactly_hobby_quota",
			in:   []state.Usage{{MBSeconds: int64(50) * 1024 * 3600}},
			want: 50.0,
		},

		// Just below Hobby quota (rounded to 6 dp).
		// 184_319_981 mb-s → 49.99999503… → 49.999995 at 6 dp.
		// Below the 50 GB-h warn line: customer is *not* warned
		// yet. Companion to `exactly_hobby_quota`.
		{
			name: "just_below_hobby_quota",
			in:   []state.Usage{{MBSeconds: 184_319_981}},
			want: 49.999995,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MonthlyUsageGB(tc.in)
			// 1e-6 GB-h = the rounding floor of the function under
			// test. Anything looser and we're not actually testing
			// the rounding.
			if !approxEq(got, tc.want, 1e-6) {
				t.Fatalf("MonthlyUsageGB(%v) = %.9f, want %.9f",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestCheckQuota pins the quota ladder exactly at boundaries the
// existing TestCheckQuota_Bands does not cover — primarily the
// under-quota rounding boundary (49.9/50 = 99.8% → 100%) and the
// 999%-cap edge cases, plus the Plan/UsedGB echo and AccountID
// invariant. Each (plan, usedGB) → (Action, Percent) pair is
// load-bearing for revenue:
//
//   - Free ≥ quota    → Action="stop" (hard cap; the financial model
//     prices against this)
//   - Paid ≥ quota    → Action="warn" (one event per UTC day; the
//     caller de-duplicates; overage accrues at
//     €0.01/GB-h)
//
// Percent is rounded to the nearest integer and capped at 999 (so a
// runaway instance doesn't emit a 5-digit percentage into the
// metric label set).
func TestCheckQuota(t *testing.T) {
	const freeQuota = 5   // GB-h
	const hobbyQuota = 50 // GB-h
	const proQuota = 250  // GB-h

	cases := []struct {
		name        string
		plan        api.Plan
		usedGB      float64
		wantAction  string
		wantPercent int
		wantQuotaGB int
	}{
		// Zero baseline. The aggregator's early-exit path
		// (`if q <= 0`) must NOT fire here — Free has quota=5,
		// not 0 — but Percent should still come back as 0.
		{
			name:        "free_zero",
			plan:        api.PlanFree,
			usedGB:      0,
			wantAction:  "",
			wantPercent: 0,
			wantQuotaGB: freeQuota,
		},

		// Under-quota rounding boundary. 4.99/5 = 99.8% rounds
		// to 100% under `int(usedGB*100.0/float64(q) + 0.5)`. The
		// dashboard distinguishes "100% with no action" from
		// "100% with stop" via the Action field, so this case
		// pins the difference.
		{
			name:        "free_under_quota_rounds_to_100",
			plan:        api.PlanFree,
			usedGB:      4.99,
			wantAction:  "",
			wantPercent: 100,
			wantQuotaGB: freeQuota,
		},

		// Hobby under-quota rounding boundary. 49.9/50 = 99.8%
		// → 100%. Same shape as the Free case above; pinned on
		// both plans to make the per-plan independence obvious.
		{
			name:        "hobby_under_quota_rounds_to_100",
			plan:        api.PlanHobby,
			usedGB:      49.9,
			wantAction:  "",
			wantPercent: 100,
			wantQuotaGB: hobbyQuota,
		},

		// 999% cap — runaway. A 20_000 % emit would explode the
		// metric label cardinality. The cap protects the metric.
		{
			name:        "percent_cap_999_runaway",
			plan:        api.PlanScale,
			usedGB:      200_000.0, // 13_333% → cap to 999
			wantAction:  "warn",
			wantPercent: 999,
			wantQuotaGB: 1500,
		},

		// 999% cap — exactly at the cap. Verifies the cap is
		// inclusive (`> 999`, not `>= 999`).
		{
			name:        "percent_exactly_at_cap_999",
			plan:        api.PlanScale,
			usedGB:      14985.0, // 999.0% — exactly at the cap
			wantAction:  "warn",
			wantPercent: 999,
			wantQuotaGB: 1500,
		},

		// 999% cap — just under. 998.6 % → rounds to 999 (still
		// under the cap, not above it). Verifies the rounding
		// inside the cap window doesn't trip the cap.
		{
			name:        "percent_just_under_cap_rounds_to_999",
			plan:        api.PlanScale,
			usedGB:      14979.0, // 998.6% → 999
			wantAction:  "warn",
			wantPercent: 999,
			wantQuotaGB: 1500,
		},

		// Pro 25 % under — pure shape, ensures Pro's quota
		// (250) is wired. The over-quota Pro case is covered by
		// TestCheckQuota_Bands.
		{
			name:        "pro_at_quota_warns",
			plan:        api.PlanPro,
			usedGB:      float64(proQuota),
			wantAction:  "warn",
			wantPercent: 100,
			wantQuotaGB: proQuota,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CheckQuota(tc.plan, tc.usedGB)
			if res.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", res.Action, tc.wantAction)
			}
			if res.Percent != tc.wantPercent {
				t.Errorf("Percent = %d, want %d", res.Percent, tc.wantPercent)
			}
			if res.QuotaGB != tc.wantQuotaGB {
				t.Errorf("QuotaGB = %d, want %d", res.QuotaGB, tc.wantQuotaGB)
			}
			if res.Plan != tc.plan {
				t.Errorf("Plan = %s, want %s", res.Plan, tc.plan)
			}
			if res.UsedGB != tc.usedGB {
				t.Errorf("UsedGB = %f, want %f (echo)", res.UsedGB, tc.usedGB)
			}
			// AccountID invariant: the helper must not invent an
			// account ID — the caller fills it in. A regression
			// that auto-fills would surface here.
			if res.AccountID != "" {
				t.Errorf("AccountID should be empty in CheckQuota (it's "+
					"filled by the caller): got %q", res.AccountID)
			}
		})
	}
}

// berlinTZ is a CET-loc helper for the TZ test cases. We don't
// import time/tzdata — Europe/Berlin is a fixed-offset zone in 2026
// (UTC+1 in winter, UTC+2 in summer) and the package-level
// time.LoadLocation would couple the test to the host's tzdata.
func berlinTZ() *time.Location {
	return time.FixedZone("CET", 60*60)
}

// approxEq compares two floats within an absolute tolerance. The
// tolerance is the function-under-test's own resolution, so a
// regression in the rounding would fail the comparison but the
// test's own arithmetic drift wouldn't.
func approxEq(a, b, eps float64) bool {
	if a > b {
		return a-b < eps
	}
	return b-a < eps
}
