package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreGitHubDeployPolicyParity(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "github-policy-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "github-policy-pg"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.GetGitHubDeployPolicy(ctx, "", acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty project lookup error = %v", err)
	}
	defaults, err := store.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(default): %v", err)
	}
	if !defaults.PreviewEnabled || defaults.PreviewTTLHours != state.GitHubDeployPolicyDefaultPreviewTTLHours {
		t.Fatalf("defaults = %+v", defaults)
	}
	policy := state.GitHubDeployPolicy{
		ProjectID:       project.ID,
		AccountID:       acct.ID,
		RootDir:         "apps/web",
		IgnoredPaths:    []string{"docs/**", "README.md"},
		PreviewEnabled:  false,
		PreviewTTLHours: 72,
	}
	stored, err := store.UpsertGitHubDeployPolicy(ctx, policy)
	if err != nil {
		t.Fatalf("UpsertGitHubDeployPolicy: %v", err)
	}
	if stored.UpdatedAt.IsZero() || stored.RootDir != policy.RootDir || stored.PreviewEnabled {
		t.Fatalf("stored policy = %+v", stored)
	}
	got, err := store.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(stored): %v", err)
	}
	if got.RootDir != policy.RootDir || len(got.IgnoredPaths) != 2 || got.IgnoredPaths[0] != "docs/**" || got.PreviewTTLHours != 72 {
		t.Fatalf("round trip = %+v", got)
	}
	if _, err := store.UpsertGitHubDeployPolicy(ctx, state.GitHubDeployPolicy{
		ProjectID:       project.ID,
		AccountID:       acct.ID,
		PreviewTTLHours: 0,
	}); err == nil {
		t.Fatal("invalid policy upsert succeeded")
	}
}
