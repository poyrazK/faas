package state

// adr: 602

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRuntimeUpgradeAcceptanceFreshnessStartsAtDispatch(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name               string
		startedAt, readyAt time.Time
		want               bool
	}{
		{"fresh", now.Add(-time.Minute), now, true},
		{"exact age bound", now.Add(-api.RuntimeUpgradeAcceptanceMaxAge), now, true},
		{"old boot with fresh acknowledgment", now.Add(-api.RuntimeUpgradeAcceptanceMaxAge - time.Microsecond), now, false},
		{"old acknowledgment", now.Add(-api.RuntimeUpgradeAcceptanceMaxAge - time.Minute), now.Add(-time.Minute), false},
		{"future acknowledgment", now, now.Add(time.Microsecond), false},
		{"reversed interval", now, now.Add(-time.Microsecond), false},
		{"missing dispatch", time.Time{}, now, false},
		{"missing acknowledgment", now, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validRuntimeUpgradeAcceptanceTime(tc.startedAt, tc.readyAt, now); got != tc.want {
				t.Fatalf("validity=%v, want %v", got, tc.want)
			}
		})
	}
}
