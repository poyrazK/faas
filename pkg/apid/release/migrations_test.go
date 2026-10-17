package release

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCrossingProblem pins the ADR-1448 409: every crossed release is listed,
// and each migratable database gets one restore point at the start of the
// earliest crossed release, truncated so it falls before the release ran.
func TestCrossingProblem(t *testing.T) {
	first := time.Date(2026, 10, 15, 10, 2, 13, 900_000_000, time.UTC)
	runs := []state.ReleaseRun{
		{DeploymentID: "d41", Revision: 41, Status: state.AppTaskSucceeded, StartedAt: first},
		{DeploymentID: "d42", Revision: 42, Status: state.AppTaskFailed, StartedAt: first.Add(time.Hour)},
	}
	databases := []state.ReleaseDatabase{{ID: "db-1", Name: "orders"}, {ID: "db-2", Name: strings.Repeat("a", 63)}}
	p := CrossingProblem(state.Deployment{ID: "d40", Revision: 40}, runs, databases)

	if p.Status != 409 || p.Code != api.CodeRollbackCrossesRelease {
		t.Fatalf("problem = %d %s, want 409 %s", p.Status, p.Code, api.CodeRollbackCrossesRelease)
	}
	for _, want := range []string{"after v40 last served", "v41 (succeeded, started 2026-10-15T10:02:13Z)", "v42 (failed"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail %q lacks %q", p.Detail, want)
		}
	}
	if len(p.CrossedReleases) != 2 || p.CrossedReleases[1].Status != "failed" || p.CrossedReleases[1].Revision != 42 {
		t.Fatalf("crossed releases = %+v", p.CrossedReleases)
	}
	if len(p.RestorePoints) != 2 {
		t.Fatalf("restore points = %+v, want one per database", p.RestorePoints)
	}
	orders := p.RestorePoints[0]
	if orders.PointInTime != "2026-10-15T10:02:13Z" ||
		orders.Command != "gregale postgres restore db-1 --name orders-before-v41 --point-in-time 2026-10-15T10:02:13Z" {
		t.Fatalf("orders restore point = %+v", orders)
	}
	long := p.RestorePoints[1].Command
	name := strings.Fields(long)[5]
	if len(name) > 63 || !strings.HasSuffix(name, "-before-v41") {
		t.Fatalf("long database restore name %q (%d chars); want at most 63 ending in -before-v41", name, len(name))
	}
}

func TestCrossingProblemWithoutManagedDatabases(t *testing.T) {
	p := CrossingProblem(state.Deployment{ID: "d40"},
		[]state.ReleaseRun{{DeploymentID: "d41", Status: state.AppTaskSucceeded, StartedAt: time.Now()}}, nil)
	if len(p.RestorePoints) != 0 || len(p.CrossedReleases) != 1 || !strings.Contains(p.Detail, "after d40 last served") {
		t.Fatalf("problem = %+v", p)
	}
}
