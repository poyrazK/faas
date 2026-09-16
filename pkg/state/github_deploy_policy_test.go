package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitHubDeployPolicyDefaultsAndIgnorePath(t *testing.T) {
	m := NewMemStore()
	acct, err := m.CreateAccount(context.Background(), "policy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := m.CreateProject(context.Background(), Project{AccountID: acct.ID, Slug: "policy"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := m.GetGitHubDeployPolicy(context.Background(), project.ID, acct.ID)
	if err != nil {
		t.Fatalf("GetGitHubDeployPolicy(default): %v", err)
	}
	if !got.PreviewEnabled || got.PreviewTTLHours != GitHubDeployPolicyDefaultPreviewTTLHours {
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
	cases := []GitHubDeployPolicy{
		{ProjectID: "p", AccountID: "a", RootDir: "../escape", PreviewEnabled: true, PreviewTTLHours: 168},
		{ProjectID: "p", AccountID: "a", IgnoredPaths: []string{"["}, PreviewEnabled: true, PreviewTTLHours: 168},
		{ProjectID: "p", AccountID: "a", PreviewEnabled: true, PreviewTTLHours: 0},
	}
	for _, policy := range cases {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded", policy)
		}
	}
}

func TestGitHubDeployPolicyValidationCoverage(t *testing.T) {
	base := GitHubDeployPolicy{ProjectID: "p", AccountID: "a", PreviewEnabled: true, PreviewTTLHours: 168}
	cases := []struct {
		name string
		edit func(*GitHubDeployPolicy)
	}{
		{name: "missing project", edit: func(p *GitHubDeployPolicy) { p.ProjectID = "" }},
		{name: "missing account", edit: func(p *GitHubDeployPolicy) { p.AccountID = "" }},
		{name: "root too long", edit: func(p *GitHubDeployPolicy) { p.RootDir = strings.Repeat("a", GitHubDeployPolicyMaxRootDirLength+1) }},
		{name: "root absolute", edit: func(p *GitHubDeployPolicy) { p.RootDir = "/workspace" }},
		{name: "root dot", edit: func(p *GitHubDeployPolicy) { p.RootDir = "." }},
		{name: "root parent", edit: func(p *GitHubDeployPolicy) { p.RootDir = "../workspace" }},
		{name: "root control", edit: func(p *GitHubDeployPolicy) { p.RootDir = "app\x00s" }},
		{name: "too many ignored paths", edit: func(p *GitHubDeployPolicy) { p.IgnoredPaths = make([]string, GitHubDeployPolicyMaxIgnoredPaths+1) }},
		{name: "empty ignored path", edit: func(p *GitHubDeployPolicy) { p.IgnoredPaths = []string{""} }},
		{name: "long ignored path", edit: func(p *GitHubDeployPolicy) {
			p.IgnoredPaths = []string{strings.Repeat("a", GitHubDeployPolicyMaxPathLength+1)}
		}},
		{name: "trimmed ignored path", edit: func(p *GitHubDeployPolicy) { p.IgnoredPaths = []string{" docs"} }},
		{name: "control ignored path", edit: func(p *GitHubDeployPolicy) { p.IgnoredPaths = []string{"docs\x00"} }},
		{name: "invalid glob", edit: func(p *GitHubDeployPolicy) { p.IgnoredPaths = []string{"["} }},
		{name: "ttl too low", edit: func(p *GitHubDeployPolicy) { p.PreviewTTLHours = GitHubDeployPolicyMinPreviewTTLHours - 1 }},
		{name: "ttl too high", edit: func(p *GitHubDeployPolicy) { p.PreviewTTLHours = GitHubDeployPolicyMaxPreviewTTLHours + 1 }},
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
	if got, err := MarshalIgnoredPaths(nil); err != nil || string(got) != "[]" {
		t.Fatalf("MarshalIgnoredPaths(nil) = %q, %v", got, err)
	}
	if got, err := MarshalIgnoredPaths([]string{"docs/**"}); err != nil || string(got) != `["docs/**"]` {
		t.Fatalf("MarshalIgnoredPaths = %q, %v", got, err)
	}
}

func TestGitHubDeployPolicyMemStoreErrorsAndCopy(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.GetGitHubDeployPolicy(ctx, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty lookup error = %v", err)
	}
	if _, err := m.GetGitHubDeployPolicy(ctx, "missing", "account"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project lookup error = %v", err)
	}
	acct, err := m.CreateAccount(ctx, "policy-copy@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	project, err := m.CreateProject(ctx, Project{AccountID: acct.ID, Slug: "policy-copy"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := m.GetGitHubDeployPolicy(ctx, project.ID, "wrong-account"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong account lookup error = %v", err)
	}
	policy := GitHubDeployPolicy{ProjectID: project.ID, AccountID: acct.ID, IgnoredPaths: []string{"docs/**"}, PreviewEnabled: true, PreviewTTLHours: 168}
	if _, err := m.UpsertGitHubDeployPolicy(ctx, GitHubDeployPolicy{ProjectID: "missing", AccountID: acct.ID, PreviewEnabled: true, PreviewTTLHours: 168}); !errors.Is(err, ErrNotFound) {
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
