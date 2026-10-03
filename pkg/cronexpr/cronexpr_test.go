package cronexpr

import (
	"errors"
	"testing"
	"time"
)

// TestParseRejectsSchedulesThatNeverFire: robfig accepts a day that never
// occurs and answers Next with the zero time, which callers read as an
// instant long past — the cron dispatcher fired it every tick and a
// scaling-schedule window never closed.
func TestParseRejectsSchedulesThatNeverFire(t *testing.T) {
	cases := []struct {
		expr string
		ok   bool
	}{
		{"0 0 30 2 *", false},
		{"0 0 31 2 *", false},
		{"0 0 31 4,6,9,11 *", false},
		{"0 0 29 2 *", true},  // leap day: fires within four years
		{"0 0 31 * *", true},  // months that have a 31st
		{"0 0 30 2 1", true},  // dom OR dow: every Monday
		{"*/5 * * * *", true}, // ordinary
	}
	for _, tc := range cases {
		_, err := Parse(tc.expr, "Europe/Istanbul")
		if tc.ok && err != nil {
			t.Errorf("Parse(%q) = %v, want ok", tc.expr, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("Parse(%q) = %v, want ErrInvalidSchedule", tc.expr, err)
		}
	}
}

func fires(t *testing.T, expr, tz string, from time.Time, n int) []string {
	t.Helper()
	s, err := Parse(expr, tz)
	if err != nil {
		t.Fatalf("Parse(%q, %q): %v", expr, tz, err)
	}
	out := make([]string, 0, n)
	cur := from
	for i := 0; i < n; i++ {
		cur = s.Next(cur)
		out = append(out, cur.UTC().Format(time.RFC3339))
	}
	return out
}

// TestParseFixedTimeSchedulesSurviveDaylightSaving — robfig matches wall
// clock fields literally: in Europe/Berlin a daily "30 2 * * *" never ran on
// the spring-forward day and ran twice on the fall-back day. Fixed-time jobs
// now follow Vixie cron; interval jobs keep following the clock.
func TestParseFixedTimeSchedulesSurviveDaylightSaving(t *testing.T) {
	cases := []struct {
		name string
		expr string
		tz   string
		from time.Time
		want []string
	}{
		{
			// 29 Mar 2026: 02:00 CET → 03:00 CEST. 02:30 runs at 03:00 CEST.
			name: "berlin spring forward runs the skipped time after the gap",
			expr: "30 2 * * *", tz: "Europe/Berlin",
			from: time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-03-29T01:00:00Z", "2026-03-30T00:30:00Z"},
		},
		{
			// 25 Oct 2026: 03:00 CEST → 02:00 CET. 02:30 runs once.
			name: "berlin fall back runs a repeated time once",
			expr: "30 2 * * *", tz: "Europe/Berlin",
			from: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"},
		},
		{
			// 8 Mar 2026: 02:00 EST → 03:00 EDT.
			name: "new york spring forward",
			expr: "15 2 * * *", tz: "America/New_York",
			from: time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-03-08T07:00:00Z", "2026-03-09T06:15:00Z"},
		},
		{
			name: "times outside the transition are untouched",
			expr: "30 4 * * *", tz: "Europe/Berlin",
			from: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-10-25T03:30:00Z", "2026-10-26T03:30:00Z"},
		},
		{
			name: "a day the schedule does not match is not caught up",
			expr: "30 2 * * 1", tz: "Europe/Berlin", // Mondays; 29 Mar is a Sunday
			from: time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-03-30T00:30:00Z"},
		},
		{
			// Interval jobs follow the clock through both passes of the hour.
			name: "interval job keeps both passes of the repeated hour",
			expr: "*/30 * * * *", tz: "Europe/Berlin",
			from: time.Date(2026, 10, 24, 23, 45, 0, 0, time.UTC),
			want: []string{"2026-10-25T00:00:00Z", "2026-10-25T00:30:00Z", "2026-10-25T01:00:00Z", "2026-10-25T01:30:00Z"},
		},
		{
			name: "utc is unaffected",
			expr: "30 2 * * *", tz: "UTC",
			from: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			want: []string{"2026-10-25T02:30:00Z", "2026-10-26T02:30:00Z"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fires(t, tc.expr, tc.tz, tc.from, len(tc.want))
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("fires = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
