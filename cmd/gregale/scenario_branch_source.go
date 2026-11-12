package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261031193635 — slice 6b, rehearsing a simulated environment branch.

// testBranchSource seeds a rehearsal workload from a simulated environment
// branch instead of deploying source: the twin reuses the branch's build and
// its non-managed values; managed data comes from the recipe's fixtures.
type testBranchSource struct {
	Project     string `yaml:"project" json:"project"`
	Environment string `yaml:"environment" json:"environment"`
	Workload    string `yaml:"workload" json:"workload"`
}

// validateTestBranchSources checks from_branch on the primary workload and
// on services. A twin only rehearses under contained actions.
func validateTestBranchSources(scenario testScenario, engine string) error {
	sources := map[string]*testBranchSource{}
	if scenario.FromBranch != nil {
		sources[scenario.Project] = scenario.FromBranch
	}
	for name, service := range scenario.Services {
		if service.FromBranch == nil {
			continue
		}
		if service.Source != "" || service.Fixture != "" {
			return fmt.Errorf("service %q sets from_branch with source or fixture; use one", name)
		}
		sources[name] = service.FromBranch
	}
	if len(sources) == 0 {
		return nil
	}
	if engine != "real-vm" {
		return fmt.Errorf("from_branch requires the real-vm engine")
	}
	if scenario.Actions == nil || scenario.Actions.Network != "contained" {
		return fmt.Errorf("from_branch requires actions.network: contained; a simulated branch rehearses only under containment")
	}
	for name, source := range sources {
		if !api.ValidProjectSlug(source.Project) || !api.ValidProjectEnvironmentSlug(source.Environment) || !api.ValidAppSlug(source.Workload) {
			return fmt.Errorf("workload %q: from_branch needs a valid project, environment and workload", name)
		}
	}
	return nil
}

// seedTestWorkloadFromBranch deploys the branch workload into the workload's
// developer session and waits for the twin to go live.
func seedTestWorkloadFromBranch(ctx context.Context, client *api.Client, project, runID string, source testBranchSource) (string, error) {
	seeded, err := client.SeedDevSessionFromEnvironmentBranch(ctx, project, api.SeedDevSessionFromEnvironmentBranchRequest{
		WorkspaceID: runID, Project: source.Project, Environment: source.Environment, Workload: source.Workload,
	})
	if err != nil {
		return "", fmt.Errorf("seed from branch %s/%s: %w", source.Project, source.Environment, err)
	}
	for {
		d, err := client.GetDeployment(ctx, seeded.DeploymentID)
		if err != nil {
			return seeded.DeploymentID, fmt.Errorf("read seeded deployment %s: %w", seeded.DeploymentID, err)
		}
		switch d.Status {
		case "live":
			return seeded.DeploymentID, nil
		case "failed", "cancelled", "superseded":
			return seeded.DeploymentID, fmt.Errorf("seeded deployment %s ended %s", seeded.DeploymentID, d.Status)
		}
		select {
		case <-ctx.Done():
			return seeded.DeploymentID, fmt.Errorf("seeded deployment %s did not go live: %w", seeded.DeploymentID, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
