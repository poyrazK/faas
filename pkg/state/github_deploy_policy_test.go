package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitHubDeployPolicyDefaultsAndIgnorePath(t *testing.T) {
	m := state.NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "policy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := m.CreateProject(context.Background(), state.Project{AccountID: acct.ID, Slug: "policy"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := m.GetGitHubDeployPolicy(context.Background(), project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(default): %v", err)
	}
	if !got.PreviewEnabled || got.PreviewTTLHours != state.GitHubDeployPolicyDefaultPreviewTTLHours || got.PreviewServicePolicy != state.PreviewServicePolicyDeny || got.ProductionTrigger != state.ProductionTriggerWebhook {
		t.Fatalf("defaults = %+v", got)
	}
	got.RootDir = "apps/web"
	got.IgnoredPaths = []string{"docs/**", "README.md", "*.md"}
	if _, err := m.UpsertGitHubDeployPolicy(context.Background(), got); err != nil {
		t.Fatalf("UpsertGitHubDeployPolicy: %v", err)
	}
	stored, err := m.GetGitHubDeployPolicy(context.Background(), project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(stored): %v", err)
	}
	if stored.RootDir != "apps/web" || !stored.IgnorePath("docs/guide.md") || !stored.IgnorePath("README.md") || !stored.IgnorePath("notes.md") {
		t.Fatalf("stored policy did not match expected paths: %+v", stored)
	}
	if stored.IgnorePath("src/app.ts") {
		t.Fatal("src/app.ts unexpectedly ignored")
	}
}

func TestGitHubDeployPolicyRejectsUnsafeValues(t *testing.T) {
	cases := []state.GitHubDeployPolicy{
		{ProjectID: "p", AccountID: "a", RootDir: "../escape", PreviewEnabled: true, PreviewTTLHours: 168, PreviewServicePolicy: state.PreviewServicePolicyDeny},
		{ProjectID: "p", AccountID: "a", IgnoredPaths: []string{"["}, PreviewEnabled: true, PreviewTTLHours: 168, PreviewServicePolicy: state.PreviewServicePolicyDeny},
		{ProjectID: "p", AccountID: "a", PreviewEnabled: true, PreviewTTLHours: 0, PreviewServicePolicy: state.PreviewServicePolicyDeny},
	}
	for _, policy := range cases {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded", policy)
		}
	}
}

func TestGitHubDeployPolicyValidationCoverage(t *testing.T) {
	base := state.GitHubDeployPolicy{ProjectID: "p", AccountID: "a", PreviewEnabled: true, PreviewTTLHours: 168, PreviewServicePolicy: state.PreviewServicePolicyDeny}
	cases := []struct {
		name string
		edit func(*state.GitHubDeployPolicy)
	}{
		{name: "missing project", edit: func(p *state.GitHubDeployPolicy) { p.ProjectID = "" }},
		{name: "missing account", edit: func(p *state.GitHubDeployPolicy) { p.AccountID = "" }},
		{name: "root too long", edit: func(p *state.GitHubDeployPolicy) {
			p.RootDir = strings.Repeat("a", state.GitHubDeployPolicyMaxRootDirLength+1)
		}},
		{name: "root absolute", edit: func(p *state.GitHubDeployPolicy) { p.RootDir = "/workspace" }},
		{name: "root dot", edit: func(p *state.GitHubDeployPolicy) { p.RootDir = "." }},
		{name: "root parent", edit: func(p *state.GitHubDeployPolicy) { p.RootDir = "../workspace" }},
		{name: "root control", edit: func(p *state.GitHubDeployPolicy) { p.RootDir = "app\x00s" }},
		{name: "too many ignored paths", edit: func(p *state.GitHubDeployPolicy) {
			p.IgnoredPaths = make([]string, state.GitHubDeployPolicyMaxIgnoredPaths+1)
		}},
		{name: "empty ignored path", edit: func(p *state.GitHubDeployPolicy) { p.IgnoredPaths = []string{""} }},
		{name: "long ignored path", edit: func(p *state.GitHubDeployPolicy) {
			p.IgnoredPaths = []string{strings.Repeat("a", state.GitHubDeployPolicyMaxPathLength+1)}
		}},
		{name: "trimmed ignored path", edit: func(p *state.GitHubDeployPolicy) { p.IgnoredPaths = []string{" docs"} }},
		{name: "control ignored path", edit: func(p *state.GitHubDeployPolicy) { p.IgnoredPaths = []string{"docs\x00"} }},
		{name: "invalid glob", edit: func(p *state.GitHubDeployPolicy) { p.IgnoredPaths = []string{"["} }},
		{name: "ttl too low", edit: func(p *state.GitHubDeployPolicy) { p.PreviewTTLHours = state.GitHubDeployPolicyMinPreviewTTLHours - 1 }},
		{name: "ttl too high", edit: func(p *state.GitHubDeployPolicy) { p.PreviewTTLHours = state.GitHubDeployPolicyMaxPreviewTTLHours + 1 }},
		{name: "invalid preview service policy", edit: func(p *state.GitHubDeployPolicy) { p.PreviewServicePolicy = "allow" }},
		{name: "invalid production trigger", edit: func(p *state.GitHubDeployPolicy) { p.ProductionTrigger = "both" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := base
			tc.edit(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatalf("Validate(%+v) succeeded", policy)
			}
		})
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	if got, err := state.MarshalIgnoredPaths(nil); err != nil || string(got) != "[]" {
		t.Fatalf("MarshalIgnoredPaths(nil) = %q, %v", got, err)
	}
	if got, err := state.MarshalIgnoredPaths([]string{"docs/**"}); err != nil || string(got) != `["docs/**"]` {
		t.Fatalf("MarshalIgnoredPaths = %q, %v", got, err)
	}
}

func TestGitHubDeployPolicyMemStoreErrorsAndCopy(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	if _, err := m.GetGitHubDeployPolicy(ctx, "", ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty lookup error = %v", err)
	}
	if _, err := m.GetGitHubDeployPolicy(ctx, "missing", "account"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing project lookup error = %v", err)
	}
	acct, err := m.CreateAccount(ctx, "policy-copy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := m.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "policy-copy"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := m.GetGitHubDeployPolicy(ctx, project.ID, "wrong-account"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("wrong account lookup error = %v", err)
	}
	policy := state.GitHubDeployPolicy{ProjectID: project.ID, AccountID: acct.ID, IgnoredPaths: []string{"docs/**"}, PreviewEnabled: true, PreviewTTLHours: 168, PreviewServicePolicy: state.PreviewServicePolicyAllowMarked}
	if _, err := m.UpsertGitHubDeployPolicy(ctx, state.GitHubDeployPolicy{ProjectID: "missing", AccountID: acct.ID, PreviewEnabled: true, PreviewTTLHours: 168, PreviewServicePolicy: state.PreviewServicePolicyDeny}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing project upsert error = %v", err)
	}
	if _, err := m.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatalf("UpsertGitHubDeployPolicy: %v", err)
	}
	stored, err := m.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy: %v", err)
	}
	stored.IgnoredPaths[0] = "changed/**"
	again, err := m.GetGitHubDeployPolicy(ctx, project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(copy): %v", err)
	}
	if again.IgnoredPaths[0] != "docs/**" {
		t.Fatalf("stored ignored paths were not copied: %+v", again.IgnoredPaths)
	}
}
