package meter

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Usage stopped being delivered the moment an account requested deletion,
// so its last hours, and every window an outage had held back, were never
// billed (bug hunt 2026-10-27 H8). Hours that began before the request are
// still delivered.
func TestUsageDeliverableAroundDeletion(t *testing.T) {
	requested := time.Date(2026, 10, 27, 14, 20, 0, 0, time.UTC)
	deleting := state.Account{Status: state.AccountDeletedPending, DeletionRequestedAt: &requested}
	for _, tc := range []struct {
		name string
		acct state.Account
		hour time.Time
		want bool
	}{
		{"active", state.Account{Status: state.AccountActive}, requested, true},
		{"suspended", state.Account{Status: state.AccountSuspended}, requested, false},
		{"deleting, earlier hour", deleting, requested.Add(-3 * time.Hour).Truncate(time.Hour), true},
		{"deleting, hour of the request", deleting, requested.Truncate(time.Hour), true},
		{"deleting, after the request", deleting, requested.Add(time.Hour).Truncate(time.Hour), false},
		{"deleting, no timestamp", state.Account{Status: state.AccountDeletedPending}, requested, false},
	} {
		if got := usageDeliverable(tc.acct, tc.hour); got != tc.want {
			t.Errorf("%s: usageDeliverable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
