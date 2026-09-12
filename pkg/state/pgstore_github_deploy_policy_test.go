package state_test

import (
	"context"
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

func TestPgStoreGitHubDeployPolicyDatabaseErrors(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "github-policy-pg-errors@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "github-policy-pg-errors"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.GetGitHubDeployPolicy(canceled, project.ID, acct.ID); err == nil {
		t.Fatal("GetGitHubDeployPolicy with canceled context succeeded")
	}
	if _, err := store.UpsertGitHubDeployPolicy(canceled, state.GitHubDeployPolicy{
		ProjectID: project.ID, AccountID: acct.ID, PreviewEnabled: true, PreviewTTLHours: 168,
	}); err == nil {
		t.Fatal("UpsertGitHubDeployPolicy with canceled context succeeded")
	}
}

func TestPgStoreGitHubDeployPolicyDecodeAndValidationErrors(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "github-policy-pg-decode@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "github-policy-pg-decode"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.UpsertGitHubDeployPolicy(ctx, state.GitHubDeployPolicy{
		ProjectID: project.ID, AccountID: acct.ID, PreviewEnabled: true, PreviewTTLHours: 168,
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	// Relax the schema checks in this isolated test schema so the read path's
	// defensive JSON and policy validation branches are exercised directly.
	if _, err := pool.Exec(ctx, `alter table github_deploy_policies
		drop constraint github_deploy_policies_ignored_paths_array_chk,
		drop constraint github_deploy_policies_preview_ttl_chk`); err != nil {
		t.Fatalf("drop policy checks: %v", err)
	}
	if _, err := pool.Exec(ctx, `update github_deploy_policies
		set ignored_paths = '{"invalid":true}'::jsonb where project_id = $1`, project.ID); err != nil {
		t.Fatalf("write malformed ignored_paths: %v", err)
	}
	if _, err := store.GetGitHubDeployPolicy(ctx, project.ID, acct.ID); err == nil {
		t.Fatal("GetGitHubDeployPolicy accepted malformed ignored_paths")
	}
	if _, err := pool.Exec(ctx, `update github_deploy_policies
		set ignored_paths = '[]'::jsonb, preview_ttl_hours = 0 where project_id = $1`, project.ID); err != nil {
		t.Fatalf("write invalid preview ttl: %v", err)
	}
	if _, err := store.GetGitHubDeployPolicy(ctx, project.ID, acct.ID); err == nil {
		t.Fatal("GetGitHubDeployPolicy accepted invalid preview ttl")
	}
}
