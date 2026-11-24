// adr: 20261123062111 — D2: usage reports a limited restore window only while
// the history limit is on.
package apidserver

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresHistoryState(t *testing.T) {
	limits := api.ManagedPostgresPlanLimits{StorageLimitBytes: 1 << 30}
	over := managedpostgres.UsageSnapshot{HistoryByteSeconds: managedpostgres.HistoryLimitByteSeconds(limits.StorageLimitBytes)}
	for _, tc := range []struct {
		name     string
		mode     managedpostgres.CapEnforcementMode
		snapshot managedpostgres.UsageSnapshot
		limits   api.ManagedPostgresPlanLimits
		overage  bool
		want     string
	}{
		{name: "over with overage off", mode: managedpostgres.CapEnforcementOn, snapshot: over, limits: limits, want: "limited"},
		{name: "over with overage on", mode: managedpostgres.CapEnforcementOn, snapshot: over, limits: limits, overage: true, want: "normal"},
		{name: "under", mode: managedpostgres.CapEnforcementOn, limits: limits, want: "normal"},
		{name: "shadow never tells", mode: managedpostgres.CapEnforcementShadow, snapshot: over, limits: limits},
		{name: "off never tells", mode: managedpostgres.CapEnforcementOff, snapshot: over, limits: limits},
		{name: "a plan without managed postgres", mode: managedpostgres.CapEnforcementOn, snapshot: over},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedPostgresHistoryState(tc.snapshot, tc.limits, tc.mode, tc.overage); got != tc.want {
				t.Fatalf("history_state = %q, want %q", got, tc.want)
			}
			view := managedPostgresUsageView(managedpostgres.UsageSummary{Snapshot: tc.snapshot}, tc.limits, managedpostgres.CapEnforcementShadow, tc.mode, tc.overage)
			if view.HistoryState != tc.want {
				t.Fatalf("usage view history_state = %q, want %q", view.HistoryState, tc.want)
			}
		})
	}
}
