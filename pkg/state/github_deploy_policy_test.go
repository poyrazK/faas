package state

import (
	"context"
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
