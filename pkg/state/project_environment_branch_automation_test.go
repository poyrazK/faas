package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 amendment 2026-11-17: a branch copies its parent's
// crons and event subscriptions paused, as a full clone does, and
// resume-automation turns on the ones the parent ran.
func TestMemProjectEnvironmentBranchCopiesAutomationPaused(t *testing.T) {
	projectEnvironmentBranchCopiesAutomationPaused(t, state.NewMemStore())
}

func projectEnvironmentBranchCopiesAutomationPaused(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "branch-automation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:auto"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, parent.ID, "/auto.ext4", "layers/auto.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCron(ctx, app.ID, "0 4 * * *", "/nightly", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCron(ctx, app.ID, "0 5 * * *", "/off", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertEventSubscription(ctx, acct.ID, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-7",
		ExpiresAt: time.Now().Add(time.Hour), EgressMode: state.EnvironmentBranchEgressLive,
		MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
	}, limits); err != nil {
		t.Fatal(err)
	}

	crons, err := s.ListCronsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	branchCrons := map[string]state.Cron{}
	for _, c := range crons {
		if c.DeploymentScope == "pr-7" {
			branchCrons[c.Path] = c
		}
	}
	if len(branchCrons) != 2 {
		t.Fatalf("branch crons = %+v, want copies of both", crons)
	}
	if c := branchCrons["/nightly"]; c.Enabled || !c.ClonePaused {
		t.Fatalf("a cron the parent ran is not paused in the branch: %+v", c)
	}
	if c := branchCrons["/off"]; c.Enabled || c.ClonePaused {
		t.Fatalf("a cron the parent had off is resumable in the branch: %+v", c)
	}
	subs, err := s.ListEventSubscriptionsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var branchSubs []state.EventSubscription
	for _, sub := range subs {
		if sub.DeploymentScope == "pr-7" {
			branchSubs = append(branchSubs, sub)
		}
	}
	if len(branchSubs) != 1 || branchSubs[0].Enabled || !branchSubs[0].ClonePaused {
		t.Fatalf("branch subscriptions = %+v, want one paused copy", branchSubs)
	}

	resumedSubs, err := s.ResumeEnvironmentClonePausedEventSubscriptions(ctx, acct.ID, project.ID, "pr-7", limits)
	if err != nil || len(resumedSubs) != 1 {
		t.Fatalf("resume subscriptions = %+v, %v", resumedSubs, err)
	}
	resumed, err := s.ResumeEnvironmentClonePausedCrons(ctx, acct.ID, project.ID, "pr-7")
	// The PG resume returns each cron's identity, not its whole row.
	if err != nil || len(resumed) != 1 || resumed[0].ID != branchCrons["/nightly"].ID || !resumed[0].Enabled {
		t.Fatalf("resume crons = %+v, %v", resumed, err)
	}
	// The parent's own automation is untouched.
	crons, _ = s.ListCronsForApp(ctx, app.ID)
	for _, c := range crons {
		if c.DeploymentScope != "pr-7" && c.Path == "/nightly" && !c.Enabled {
			t.Fatalf("the parent's cron changed: %+v", c)
		}
	}
}
