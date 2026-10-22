package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261022120744 — `gregale deployment timeline` prints the release,
// then one event per line.
func TestReleaseTimelineLines(t *testing.T) {
	traffic := 10
	timeline := api.ReleaseTimeline{DeploymentID: "d42", App: "shop", Revision: 42, Phase: api.ReleasePhaseHeld,
		Summary: "Held at step 2 of 4, 10% of traffic: waiting for 1 more approval(s)", StableDeploymentID: "d41", StableRevision: 41,
		DeployedBy: "ana@example.com", CommitSHA: strings.Repeat("c", 40),
		Approval: &api.ReleaseApprovalStatus{State: api.ReleaseApprovalStateWaiting, RequiredApprovals: 1},
		Gates:    []api.ReleaseGateResult{{Name: "orders", Kind: "custom_metric", Passed: false, Summary: "orders averaged 3 (fails below 5)"}},
		Events: []api.ReleaseTimelineEvent{
			{At: "2026-10-22T09:00:00Z", Kind: api.ReleaseEventCreated, Summary: "Deployed by ana@example.com", Actor: "ana@example.com"},
			{At: "2026-10-22T09:13:00Z", Kind: api.ReleaseEventHeld, Summary: "Held at 10% of traffic: waiting for 1 more approval(s)", Actor: "Gregale",
				TrafficPercent: &traffic, Repeats: 2},
		},
		Truncated: true,
	}
	got := strings.Join(main.ReleaseTimelineLines(timeline), "\n")
	want := strings.Join([]string{
		"v42 of shop · held · Held at step 2 of 4, 10% of traffic: waiting for 1 more approval(s)",
		"replaces v41 · deployed by ana@example.com · commit cccccccccccc",
		"Approvals: waiting, 0 of 1",
		"Gate orders failed: orders averaged 3 (fails below 5)",
		"",
		"2026-10-22 09:00:00  created            Deployed by ana@example.com · ana@example.com",
		"2026-10-22 09:13:00  held               Held at 10% of traffic: waiting for 1 more approval(s) (3 times) · Gregale",
		"(older history is not shown)",
	}, "\n")
	if got != want {
		t.Fatalf("lines:\n%s\nwant:\n%s", got, want)
	}
}
