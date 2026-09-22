// spec: §10 — provider delivery keys and reconciliation use whole UTC hours.
package billing

import (
	"testing"
	"time"
)

func TestCompletedUsageWindow(t *testing.T) {
	at := time.Date(2026, 9, 1, 2, 15, 0, 0, time.FixedZone("UTC+0545", 5*3600+45*60))
	end := time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)
	for _, lookback := range []time.Duration{-time.Hour, 0, time.Minute, 90 * time.Minute, 24 * time.Hour} {
		start, gotEnd := CompletedUsageWindow(at, lookback)
		wantStart := end.Add(-max(lookback, 0).Truncate(time.Hour))
		if !gotEnd.Equal(end) || !start.Equal(wantStart) || start.Location() != time.UTC || gotEnd.Location() != time.UTC {
			t.Errorf("lookback %s: [%s, %s), want [%s, %s) UTC", lookback, start, gotEnd, wantStart, end)
		}
	}
}
