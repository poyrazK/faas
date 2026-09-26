package cronexpr

import (
	"errors"
	"testing"
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
