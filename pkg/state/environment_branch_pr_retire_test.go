package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261113121342 slice 2: a project's pull request branches end when its
// previews do (previews turned off, the app unbound, the installation
// revoked); a branch made by hand keeps its expiry.
func TestMemEnvironmentBranchPRRetirement(t *testing.T) {
	for _, trigger := range []string{"previews off", "app unbound"} {
		t.Run(trigger, func(t *testing.T) { environmentBranchPRRetirement(t, state.NewMemStore(), trigger) })
	}
}

func TestPgEnvironmentBranchPRRetirement(t *testing.T) {
	for _, trigger := range []string{"previews off", "app unbound", "installation revoked", "repository removed"} {
		t.Run(trigger, func(t *testing.T) {
			s, _, _ := pgWithPool(t)
			environmentBranchPRRetirement(t, s, trigger)
		})
	}
}

func environmentBranchPRRetirement(t *testing.T, s contractTestStore, trigger string) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	installation := time.Now().UnixNano()%1_000_000_000 + 1
	repo := "acme/shop-" + suffix
	acct, err := s.CreateAccount(ctx, "branch-retire-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop-" + suffix,
		InstallID: installation, RepoFullName: repo, ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	web, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "web-" + suffix, WorkloadName: "web",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AppID: web.ID, AccountID: acct.ID, BindingID: "bind-" + web.ID,
		InstallID: installation, RepoFullName: repo, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	for _, branch := range []struct {
		slug string
		pr   int
	}{{"pr-7", 7}, {"by-hand", 0}} {
		if _, _, _, err := s.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: branch.slug, PullRequestNumber: branch.pr,
			ExpiresAt: expires, EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
		}, api.MustLimitsFor(acct.Plan)); err != nil {
			t.Fatal(err)
		}
	}

	switch trigger {
	case "previews off":
		_, err = s.(state.ProjectPRPreviewRetirer).RetireProjectPRPreviews(ctx, acct.ID, project.ID)
	case "app unbound":
		err = s.DeleteGithubInstallBinding(ctx, web.ID)
	case "installation revoked":
		err = s.(*state.PgStore).RevokeGitHubInstallation(ctx, installation)
	case "repository removed":
		err = s.(*state.PgStore).RemoveGitHubRepositories(ctx, installation, []string{repo})
	}
	if err != nil {
		t.Fatalf("%s: %v", trigger, err)
	}

	now := time.Now().Add(2 * time.Second)
	retired, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-7")
	if err != nil || retired.ExpiresAt.After(now) {
		t.Fatalf("pull request branch after %s = %+v, %v; want it expired", trigger, retired, err)
	}
	kept, err := s.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "by-hand")
	if err != nil || !kept.ExpiresAt.Equal(expires) {
		t.Fatalf("hand-made branch after %s = %+v, %v; want expiry %s", trigger, kept, err, expires)
	}
	expired, err := s.ExpiredProjectEnvironmentBranches(ctx, now, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, branch := range expired {
		found = found || branch.EnvironmentID == retired.EnvironmentID
	}
	if !found {
		t.Fatalf("the sweeper does not see the retired pull request branch after %s", trigger)
	}
}
