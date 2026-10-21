package state

import (
	"context"
	"slices"
	"sort"
	"testing"
	"time"
)

// TestMemStoreListFloorDeploymentsKeepsOnlyFloorApps pins the candidate set
// the floor reconciler reads: every live deployment of an app with any floor
// source, and nothing else.
func TestMemStoreListFloorDeploymentsKeepsOnlyFloorApps(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	apps := []App{
		{ID: "no-floor", NodeID: "n1", Status: AppActive},
		{ID: "app-min", NodeID: "n1", Status: AppActive, MinInstances: 1},
		{ID: "policy-min", NodeID: "n1", Status: AppActive, ScalingPolicy: &ScalingPolicy{MinInstances: 2}},
		{ID: "policy-schedule", NodeID: "n2", Status: AppActive, ScalingPolicy: &ScalingPolicy{Schedules: []ScalingSchedule{{Cron: "0 9 * * *", DurationS: 60, MinInstances: 1}}}},
		{ID: "deployment-floor", NodeID: "n1", Status: AppActive},
		{ID: "deleted", NodeID: "n1", Status: AppDeleted, MinInstances: 3},
	}
	deployments := []Deployment{
		{ID: "no-floor-live", AppID: "no-floor", Status: DeployLive},
		{ID: "app-min-live", AppID: "app-min", Status: DeployLive},
		{ID: "app-min-old", AppID: "app-min", Status: DeploySuperseded},
		{ID: "policy-min-live", AppID: "policy-min", Status: DeployLive},
		{ID: "policy-schedule-live", AppID: "policy-schedule", Status: DeployLive},
		{ID: "deployment-floor-live", AppID: "deployment-floor", Status: DeployLive, MinInstances: 2},
		// A sibling without its own floor stays in the set so the
		// reconciler's serving-scope check sees the whole app.
		{ID: "deployment-floor-sibling", AppID: "deployment-floor", Status: DeployLive},
		{ID: "deleted-live", AppID: "deleted", Status: DeployLive},
	}
	m.mu.Lock()
	for _, a := range apps {
		m.apps[a.ID] = a
	}
	for i, d := range deployments {
		d.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		m.deployments[d.ID] = d
	}
	m.mu.Unlock()

	ids := func(deps []Deployment) []string {
		out := make([]string, 0, len(deps))
		for _, d := range deps {
			out = append(out, d.ID)
		}
		sort.Strings(out)
		return out
	}
	ctx := context.Background()
	all, err := m.ListFloorDeployments(ctx)
	if err != nil {
		t.Fatalf("ListFloorDeployments: %v", err)
	}
	wantAll := []string{"app-min-live", "deployment-floor-live", "deployment-floor-sibling", "policy-min-live", "policy-schedule-live"}
	if got := ids(all); !slices.Equal(got, wantAll) {
		t.Fatalf("ListFloorDeployments = %v, want %v", got, wantAll)
	}
	for i := 1; i < len(all); i++ {
		if all[i].CreatedAt.After(all[i-1].CreatedAt) {
			t.Fatalf("ListFloorDeployments is not newest first: %v", ids(all))
		}
	}
	owned, err := m.ListFloorDeploymentsByNodeID(ctx, "n1")
	if err != nil {
		t.Fatalf("ListFloorDeploymentsByNodeID: %v", err)
	}
	wantOwned := []string{"app-min-live", "deployment-floor-live", "deployment-floor-sibling", "policy-min-live"}
	if got := ids(owned); !slices.Equal(got, wantOwned) {
		t.Fatalf("ListFloorDeploymentsByNodeID(n1) = %v, want %v", got, wantOwned)
	}
}
