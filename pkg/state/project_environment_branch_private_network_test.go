package state_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 4: a private-network attachment reaches every
// instance of its app, so environment branches and attached workloads do not
// share a project. Each side refuses the other.
func TestMemProjectEnvironmentBranchPrivateNetworks(t *testing.T) {
	projectEnvironmentBranchPrivateNetworks(t, state.NewMemStore())
}

func projectEnvironmentBranchPrivateNetworks(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, _, deployment := seedProjectEnvironmentBranch(t, s, "branch-network@example.com")
	attach := func(appID string) error {
		_, err := s.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
			AccountID: acct.ID, AppID: appID, NetworkID: "net-1", Region: "eu-1",
			CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")},
		})
		return err
	}
	if err := attach(deployment.AppID); !errors.Is(err, state.ErrProjectEnvironmentBranchPrivateNetwork) {
		t.Fatalf("attached a workload of a branched project: %v", err)
	}
	if _, err := s.GetAppPrivateNetworkAttachment(ctx, acct.ID, deployment.AppID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused attachment was saved: %v", err)
	}
	other, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: other.ID, Slug: "worker", WorkloadName: "worker", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := attach(otherApp.ID); err != nil {
		t.Fatalf("another project's workload: %v", err)
	}

	if err := s.DeleteProjectEnvironmentBranch(ctx, acct.ID, project.ID, "pr-1"); err != nil {
		t.Fatal(err)
	}
	if err := attach(deployment.AppID); err != nil {
		t.Fatalf("attach after the branch is gone: %v", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	branch := state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-2",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, branch, limits); !errors.Is(err, state.ErrProjectEnvironmentBranchPrivateNetwork) {
		t.Fatalf("branched a project with an attached workload: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-2"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused branch left an environment: %v", err)
	}
	if err := s.DeleteAppPrivateNetworkAttachment(ctx, acct.ID, deployment.AppID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, branch, limits); err != nil {
		t.Fatalf("branch after the workload left the network: %v", err)
	}
}
