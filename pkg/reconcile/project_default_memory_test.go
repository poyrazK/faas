package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestNewProjectServiceRAM pins the project default from pkg/api/limits.go
// (Hunt 14 H14-36): a service without Compose memory starts at
// ProjectWorkloadDefaultRAMMB, and declared memory wins.
func TestNewProjectServiceRAM(t *testing.T) {
	if got := workloadToDraftApp(state.Project{}, reposcan.Workload{Name: "web"}, "", api.PlanScale).RAMMB; got != api.ProjectWorkloadDefaultRAMMB {
		t.Fatalf("undeclared memory: ram_mb = %d, want %d", got, api.ProjectWorkloadDefaultRAMMB)
	}
	declared := reposcan.Workload{Name: "api", Resources: &api.ComposeResources{MemoryMB: 768}}
	if got := workloadToDraftApp(state.Project{}, declared, "", api.PlanScale).RAMMB; got != 768 {
		t.Fatalf("declared memory: ram_mb = %d, want 768", got)
	}
}
