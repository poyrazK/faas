package state

import "testing"

// Observe audit 2026-11-21 (finding 20): the failing-cron pause counted runs
// the platform failed before the app ran (the wake, the version pin) as the
// app failing, then emailed the customer to fix the app.
func TestPlatformFailedBeforeApp(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"wake: sched: node admission unavailable", true},
		{"version pin: deployment gone", true},
		{"bind operation instance: lease lost", true},
		{"HTTP 500 from /cron/report", false},
		{"", false},
		{"rewake: not a prefix", false},
	} {
		if got := PlatformFailedBeforeApp(tc.err); got != tc.want {
			t.Errorf("PlatformFailedBeforeApp(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
