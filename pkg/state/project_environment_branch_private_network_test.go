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

// ADR-20261031193635 section 6: branches and private networks share a
// project. A branch stays off its apps' networks unless it joins them, which
// only a live branch may; vmmd reads the mode from the deployment's policy.
func TestMemProjectEnvironmentBranchPrivateNetworks(t *testing.T) {
	projectEnvironmentBranchPrivateNetworks(t, state.NewMemStore())
}

func projectEnvironmentBranchPrivateNetworks(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, off, offDeployment := seedProjectEnvironmentBranch(t, s, "branch-network@example.com")
	if _, err := s.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: acct.ID, AppID: offDeployment.AppID, NetworkID: "net-1", Region: "eu-1",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")},
	}); err != nil {
		t.Fatalf("attach a workload of a branched project: %v", err)
	}
	if off.PrivateNetworks != state.EnvironmentBranchPrivateNetworksOff {
		t.Fatalf("default private networks = %q, want off", off.PrivateNetworks)
	}
	parentID, err := s.EnvironmentBranchLineageParent(ctx, offDeployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.EnvironmentBranchEgress(ctx, parentID); err != nil || !got.JoinsPrivateNetworks() {
		t.Fatalf("production joins its networks: %+v, %v", got, err)
	}
	if got, err := s.EnvironmentBranchEgress(ctx, offDeployment.ID); err != nil || got.JoinsPrivateNetworks() || got.PrivateNetworks != state.EnvironmentBranchPrivateNetworksOff {
		t.Fatalf("a branch stays off its networks by default: %+v, %v", got, err)
	}

	limits := api.MustLimitsFor(acct.Plan)
	create := func(slug, egress, networks string) (state.ProjectEnvironmentBranch, error) {
		_, branch, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: slug,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: egress, PrivateNetworks: networks,
			MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
		}, limits)
		return branch, err
	}
	joined, err := create("pr-2", state.EnvironmentBranchEgressLive, state.EnvironmentBranchPrivateNetworksJoin)
	if err != nil || joined.PrivateNetworks != state.EnvironmentBranchPrivateNetworksJoin {
		t.Fatalf("branch an attached project with join = %+v, %v", joined, err)
	}
	if stored, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-2"); err != nil || stored.PrivateNetworks != state.EnvironmentBranchPrivateNetworksJoin {
		t.Fatalf("stored join branch = %+v, %v", stored, err)
	}
	joinedDeployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "pr-2", offDeployment.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.EnvironmentBranchEgress(ctx, joinedDeployment.ID); err != nil || !got.JoinsPrivateNetworks() {
		t.Fatalf("a join branch joins its networks: %+v, %v", got, err)
	}
	for name, tc := range map[string]struct{ egress, networks string }{
		"contained join": {state.EnvironmentBranchEgressContained, state.EnvironmentBranchPrivateNetworksJoin},
		"simulated join": {state.EnvironmentBranchEgressSimulated, state.EnvironmentBranchPrivateNetworksJoin},
		"unknown mode":   {state.EnvironmentBranchEgressLive, "bridge"},
	} {
		if _, err := create("pr-x", tc.egress, tc.networks); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("%s: %v, want invalid argument", name, err)
		}
	}
	if _, err := create("pr-3", state.EnvironmentBranchEgressContained, state.EnvironmentBranchPrivateNetworksOff); err != nil {
		t.Fatalf("contained branch of an attached project: %v", err)
	}
}
