// adr: 20261121231500
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The previous month's realtime egress stays reconcilable while a report for
// its last hour can still arrive.
func TestRealtimeEgressPreviousMonthOpen(t *testing.T) {
	start := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	lastHour := start.Add(-time.Hour)
	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"month start", start, true},
		{"last hour report still accepted", lastHour.Add(api.RealtimeUsageReportMaxAge), true},
		{"just before the window closes", start.Add(api.RealtimeUsageReportMaxAge + time.Hour - time.Second), true},
		{"window closed", start.Add(api.RealtimeUsageReportMaxAge + time.Hour), false},
		{"mid month", start.Add(10 * 24 * time.Hour), false},
	} {
		if got := state.RealtimeEgressPreviousMonthOpen(tc.now); got != tc.want {
			t.Errorf("%s: RealtimeEgressPreviousMonthOpen(%s) = %v, want %v", tc.name, tc.now, got, tc.want)
		}
	}
}
