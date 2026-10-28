package routejobs_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routejobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHoldPastDeadline(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	holds := []state.RouteHealthHoldAlert{
		{DeploymentID: "aborted", Status: "aborted", UpdatedAt: now.Add(-48 * time.Hour)},
		{DeploymentID: "fresh", Status: "blocked_unknown", UpdatedAt: now.Add(-time.Hour)},
		{DeploymentID: "stale", Status: "blocked_regressed", UpdatedAt: now.Add(-api.RouteHealthHoldDeadline)},
	}
	for _, tc := range []struct {
		deployment string
		held       time.Duration
		past       bool
	}{
		{deployment: "fresh", held: time.Hour},
		{deployment: "stale", held: api.RouteHealthHoldDeadline, past: true},
		{deployment: "aborted"},
		{deployment: "missing"},
	} {
		held, past := routejobs.HoldPastDeadline(holds, tc.deployment, now)
		if held != tc.held || past != tc.past {
			t.Errorf("%s: held %s past %v, want %s %v", tc.deployment, held, past, tc.held, tc.past)
		}
	}
}
