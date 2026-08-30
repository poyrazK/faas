package main

import "testing"

func TestGitHubTarballURL_PreservesRefPath(t *testing.T) {
	got, err := githubTarballURL("poyrazK/example", "release/2026-q3")
	if err != nil {
		t.Fatalf("githubTarballURL: %v", err)
	}
	want := "https://api.github.com/repos/poyrazK/example/tarball/release/2026-q3"
	if got != want {
		t.Errorf("githubTarballURL = %q, want %q", got, want)
	}
}

func TestGitHubTarballURL_EscapesRefSegments(t *testing.T) {
	got, err := githubTarballURL("owner/repo", "release/100%25")
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
			if got, err := githubTarballURL(tc.repo, tc.ref); err == nil {
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
		{topic: "", want: docsSiteURL},
		{topic: "storage", want: storageDocsURL},
		{topic: "runtime-node", want: docsSiteURL + "/runtime-node"},
		{topic: "apps", want: cliDocsURL},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			if got := docsURLForTopic(tc.topic); got != tc.want {
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
		{name: "legacy build", raw: "https://docs.gregale.dev/build/limits#timeout", want: deployFromSourceDocsURL},
		{name: "legacy storage", raw: "https://docs.gregale.dev/storage", want: storageDocsURL},
		{name: "legacy error", raw: "https://docs.gregale.dev/errors/app-not-listening", want: cliDocsURL},
		{name: "canonical url", raw: "https://gregale.dev/docs/cli", want: "https://gregale.dev/docs/cli"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDocsURL(tc.raw); got != tc.want {
				t.Errorf("normalizeDocsURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
