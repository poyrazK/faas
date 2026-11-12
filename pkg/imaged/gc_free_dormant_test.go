package imaged

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The Free-tier disk reaper takes every snapshot of a dormant app, skips
// rows already targeted or pending delete, and leaves other apps alone.
// adr: 20261105120000
func TestDormantFreeTargets(t *testing.T) {
	rows := []state.SnapshotForGC{
		{ID: "s2", AppID: "dormant", DeploymentID: "d2", DeploymentStatus: state.DeployLive},
		{ID: "s1", AppID: "dormant", DeploymentID: "d1", DeploymentStatus: state.DeploySuperseded},
		{ID: "s3", AppID: "dormant", DeploymentID: "d1", DeletePending: true},
		{ID: "s4", AppID: "dormant", DeploymentID: "d0"},
		{ID: "s5", AppID: "active", DeploymentID: "d9"},
	}
	got := dormantFreeTargets(rows, map[string]bool{"dormant": true}, []deleteTarget{{ID: "s4"}})
	if len(got) != 2 || got[0].ID != "s1" || got[1].ID != "s2" {
		t.Fatalf("targets = %+v, want s1 and s2", got)
	}
	if got[1].DeploymentStatus != state.DeployLive {
		t.Fatalf("live deployment status lost: %+v", got[1])
	}
	if got := dormantFreeTargets(rows, nil, nil); got != nil {
		t.Fatalf("no dormant apps = %+v, want none", got)
	}
}
