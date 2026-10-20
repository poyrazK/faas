package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestGitHubTarballURL_PreservesRefPath(t *testing.T) {
	got, err := main.GithubTarballURL("poyrazK/example", "release/2026-q3")
	if err != nil {
		t.Fatalf("githubTarballURL: %v", err)
	}
	want := "https://api.github.com/repos/poyrazK/example/tarball/release/2026-q3"
	if got != want {
		t.Errorf("githubTarballURL = %q, want %q", got, want)
	}
}

func TestGitHubTarballURL_EscapesRefSegments(t *testing.T) {
	got, err := main.GithubTarballURL("owner/repo", "release/100%25")
	if err != nil {
		t.Fatalf("githubTarballURL: %v", err)
	}
	want := "https://api.github.com/repos/owner/repo/tarball/release/100%2525"
	if got != want {
		t.Errorf("githubTarballURL = %q, want %q", got, want)
	}
}

func TestGitHubTarballURL_RejectsURLDelimiters(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo string
		ref  string
	}{
		{name: "repo query", repo: "owner/repo?next=evil", ref: "main"},
		{name: "ref query", repo: "owner/repo", ref: "main?next=evil"},
		{name: "ref fragment", repo: "owner/repo", ref: "main#fragment"},
		{name: "ref traversal", repo: "owner/repo", ref: "../main"},
		{name: "ref empty segment", repo: "owner/repo", ref: "feature//main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := main.GithubTarballURL(tc.repo, tc.ref); err == nil {
				t.Fatalf("githubTarballURL = %q, want validation error", got)
			}
		})
	}
}

func TestDocsURLForTopic_UsesLiveRoutes(t *testing.T) {
	for _, tc := range []struct {
		topic string
		want  string
	}{
		{topic: "", want: main.DocsSiteURL},
		{topic: "storage", want: main.StorageDocsURL},
		{topic: "runtime-node", want: main.DocsSiteURL + "/runtime-node"},
		{topic: "apps", want: main.CliDocsURL},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			if got := main.DocsURLForTopic(tc.topic); got != tc.want {
				t.Errorf("docsURLForTopic(%q) = %q, want %q", tc.topic, got, tc.want)
			}
		})
	}
}

func TestNormalizeDocsURL_RepairsLegacyServerLinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "legacy build", raw: "https://docs.gregale.dev/build/limits#timeout", want: main.DeployFromSourceDocsURL},
		{name: "legacy storage", raw: "https://docs.gregale.dev/storage", want: main.StorageDocsURL},
		{name: "legacy error", raw: "https://docs.gregale.dev/errors/app-not-listening", want: main.CliDocsURL},
		{name: "canonical url", raw: "https://gregale.dev/docs/cli", want: "https://gregale.dev/docs/cli"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.NormalizeDocsURL(tc.raw); got != tc.want {
				t.Errorf("normalizeDocsURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
