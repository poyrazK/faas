package cronexpr

import (
	"testing"
	"time"
)

// FuzzParseNextIsStrictlyIncreasing checks the daylight-saving adjustment
// never breaks the schedule contract every caller relies on: Next(t) is
// either the zero time (never) or strictly after t, and walking a schedule
// forward only moves forward — across gaps and repeated hours in any zone.
func FuzzParseNextIsStrictlyIncreasing(f *testing.F) {
	zones := []string{"UTC", "Europe/Berlin", "America/New_York", "Australia/Lord_Howe", "America/Sao_Paulo", "Asia/Kolkata", "Pacific/Chatham"}
	f.Add("30 2 * * *", uint8(1), int64(1774656000)) // 2026-03-28, before Berlin spring forward
	f.Add("0 2 * * 0", uint8(2), int64(1772928000))  // New York DST week
	f.Add("*/15 1-3 * * *", uint8(1), int64(1793000000))
	f.Add("45 1 * 10 *", uint8(3), int64(1790000000))
	f.Fuzz(func(t *testing.T, expr string, zone uint8, unix int64) {
		if unix < 0 || unix > 4102444800 { // up to 2100
			return
		}
		tz := zones[int(zone)%len(zones)]
		s, err := Parse(expr, tz)
		if err != nil {
			return
		}
		loc, _ := time.LoadLocation(tz)
		fixed := fixedWallTime(expr)
		cur := time.Unix(unix, 0).UTC()
		var prev time.Time
		for i := 0; i < 8; i++ {
			next := s.Next(cur)
			if next.IsZero() {
				return
			}
			if !next.After(cur) {
				t.Fatalf("%q in %s: Next(%s) = %s, not after", expr, tz, cur, next)
			}
			// A fixed-time job never runs the same wall-clock minute twice.
			if fixed && !prev.IsZero() && sameWallMinute(prev.In(loc), next.In(loc)) {
				t.Fatalf("%q in %s: fired twice at wall time %s (%s and %s)", expr, tz, next.In(loc).Format("2006-01-02 15:04"), prev, next)
			}
			prev, cur = next, next
		}
	})
}
