// spec: §4.3
package cronexpr

import (
	"testing"
	"time"
)

func TestFallBackSkipsEveryRepeatedFixedMinute(t *testing.T) {
	for _, tc := range []struct {
		name, expression, zone, from, want string
	}{
		{"six fixed minutes", "0,10,20,30,40,50 1 * * *", "America/New_York", "2026-11-01T05:50:00Z", "2026-11-02T06:00:00Z"},
		{"minute range", "0-59 1 * * *", "America/New_York", "2026-11-01T05:59:00Z", "2026-11-02T06:00:00Z"},
		{"next unrepeated hour", "0-59 1,2 * * *", "America/New_York", "2026-11-01T05:59:00Z", "2026-11-01T07:00:00Z"},
		{"Berlin minute range", "0-59 2 * * *", "Europe/Berlin", "2026-10-25T00:59:00Z", "2026-10-26T01:00:00Z"},
		{"interval keeps repeated occurrences", "*/10 1 * * *", "America/New_York", "2026-11-01T05:50:00Z", "2026-11-01T06:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schedule, err := Parse(tc.expression, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			from, err := time.Parse(time.RFC3339, tc.from)
			if err != nil {
				t.Fatal(err)
			}
			if next := schedule.Next(from).UTC().Format(time.RFC3339); next != tc.want {
				t.Errorf("next occurrence = %s, want %s", next, tc.want)
			}
		})
	}
}
