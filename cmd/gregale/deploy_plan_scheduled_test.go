package main_test

import (
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale deploy --at +2h` planned "release: standard
// · 100% after readiness" although the rollout waits for its time (Hunt 14
// H14-15).
func TestDeployPlanNamesAScheduledStart(t *testing.T) {
	at := time.Date(2026, 10, 25, 3, 54, 0, 0, time.UTC)
	want := "scheduled · rollout starts " + at.Local().Format("2006-01-02 15:04 MST") + ", then app release policy"
	if got := main.DeployPreflightScheduled(at.Format(time.RFC3339), "app release policy"); got != want {
		t.Fatalf("scheduled plan = %q, want %q", got, want)
	}
	if got := main.DeployPreflightScheduled("", "app release policy"); got != "app release policy" {
		t.Fatalf("unscheduled plan = %q", got)
	}
}
