package meter

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// meterd rolls a missed minute up to api.MeterUsageFinalAfter late, but the
// daily rollup only recomputed yesterday and today, so after an outage of more
// than a day usage_daily stayed short of what was billed. The late windows
// cover every earlier day a catch-up row can still land in, whole and
// contiguous up to yesterday.
// adr: 20261101113742
func TestLateDailyRollupWindowsCoverTheCatchUpWindow(t *testing.T) {
	now := time.Date(2026, 11, 4, 1, 30, 0, 0, time.FixedZone("test", 3*60*60))
	windows := lateDailyRollupWindows(now)
	yesterday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) // now is 2026-11-03 22:30 UTC
	earliest := now.UTC().Add(-api.MeterUsageFinalAfter)
	if len(windows) == 0 {
		t.Fatal("no late windows")
	}
	if windows[0].start.After(earliest) {
		t.Fatalf("late windows start %s, after the earliest writable minute %s", windows[0].start, earliest)
	}
	if !windows[0].start.Equal(windows[0].start.Truncate(24 * time.Hour)) {
		t.Fatalf("late window start %s is not a UTC day", windows[0].start)
	}
	for i, w := range windows {
		if w.end.Sub(w.start) != 24*time.Hour {
			t.Fatalf("window %d = [%s,%s), want one whole day", i, w.start, w.end)
		}
		if i > 0 && !w.start.Equal(windows[i-1].end) {
			t.Fatalf("window %d starts %s, want %s", i, w.start, windows[i-1].end)
		}
	}
	if last := windows[len(windows)-1]; !last.end.Equal(yesterday) {
		t.Fatalf("late windows end %s, want yesterday %s (the regular windows take it from there)", last.end, yesterday)
	}
}
