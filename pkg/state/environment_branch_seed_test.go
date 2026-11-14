package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 6b: a rehearsal member is seeded from a simulated
// branch's workload. It reuses the branch deployment's artifact, gets the
// branch's non-managed values, and is primed with an ordinary boot.
func TestMemEnvironmentBranchSeed(t *testing.T) {
	environmentBranchSeed(t, state.NewMemStore())
}

func environmentBranchSeed(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, _, live := seedProjectEnvironmentBranch(t, s, "branch-seed@example.com")
	limits := api.MustLimitsFor(acct.Plan)
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "sim-1",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressSimulated,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}
	branchDeployment, err := s.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, "sim-1", live.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, branchDeployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, acct.ID, live.AppID, "sim-1", "APP_COLOR", "simulated"); err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dev-web-" + uuid.NewString()[:8], Status: state.AppActive,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, PreviewOfSlug: "web"})
	if err != nil {
		t.Fatal(err)
	}
	seed := state.EnvironmentBranchSeed{AccountID: acct.ID, ProjectID: project.ID, Branch: "sim-1", Workload: "web", MemberAppID: member.ID}

	for _, tc := range []struct {
		name string
		edit func(*state.EnvironmentBranchSeed)
		want error
	}{
		{"live branch", func(s *state.EnvironmentBranchSeed) { s.Branch = "pr-1" }, state.ErrProjectEnvironmentBranchNotSimulated},
		{"unknown workload", func(s *state.EnvironmentBranchSeed) { s.Workload = "api" }, state.ErrNotFound},
		{"not a member app", func(s *state.EnvironmentBranchSeed) { s.MemberAppID = live.AppID }, state.ErrNotFound},
	} {
		bad := seed
		tc.edit(&bad)
		if _, err := s.SeedScenarioMemberFromEnvironmentBranch(ctx, bad); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}

	seeded, err := s.SeedScenarioMemberFromEnvironmentBranch(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.AppID != member.ID || seeded.Status != state.DeploySnapshotting || seeded.Reason != state.EnvironmentBranchSeedReason ||
		seeded.RootfsKey == "" || seeded.RootfsKey != branchDeployment.RootfsKey {
		t.Fatalf("seeded deployment = %+v; branch rootfs %q", seeded, branchDeployment.RootfsKey)
	}
	if lineage, err := s.EnvironmentBranchDeployment(ctx, seeded.ID); err != nil || lineage {
		t.Fatalf("a seeded member has branch lineage = %v, %v; it must prime with a boot", lineage, err)
	}
	envs, err := s.ListAppEnvInScope(ctx, acct.ID, member.ID, "default")
	if err != nil || len(envs) == 0 {
		t.Fatalf("member values = %+v, %v", envs, err)
	}
	var color string
	for _, env := range envs {
		if env.Key == "APP_COLOR" {
			color = env.Value
		}
	}
	if color != "simulated" {
		t.Fatalf("member APP_COLOR = %q, want the branch's value", color)
	}
	again, err := s.SeedScenarioMemberFromEnvironmentBranch(ctx, seed)
	if err != nil || again.ID != seeded.ID {
		t.Fatalf("second seed = %+v, %v; want the same deployment", again, err)
	}
}
