package dbmaintenance

import (
	"testing"
	"time"
)

func TestActiveOrNext(t *testing.T) {
	window := Window{Weekday: int(time.Saturday), StartMinute: 23*60 + 30, DurationMinutes: 90}
	if !window.Valid() {
		t.Fatal("valid weekly window was rejected")
	}
	start := time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		now  time.Time
		want time.Time
		open bool
	}{
		{"before opening", start.Add(-time.Millisecond), start, false},
		{"exact opening", start, start, true},
		{"after UTC week boundary", start.Add(time.Hour).In(time.FixedZone("UTC+3", 3*60*60)), start, true},
		{"exact close", start.Add(90 * time.Minute), start.AddDate(0, 0, 7), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := window.ActiveOrNext(tc.now)
			if !got.Start.Equal(tc.want) || !got.End.Equal(tc.want.Add(90*time.Minute)) || got.Contains(tc.now) != tc.open {
				t.Fatalf("interval = %+v (open %v), want start %s (open %v)", got, got.Contains(tc.now), tc.want, tc.open)
			}
		})
	}
}
