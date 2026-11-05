// adr: 20261027090010
package api

import (
	"errors"
	"strings"
	"testing"
)

func validGitHubPullRequestEvent() GitHubPullRequestEvent {
	return GitHubPullRequestEvent{Action: "opened", InstallationID: 7, Sender: "octocat",
		Repository: GitHubEventRepository{FullName: "octo/api", DefaultBranch: "main"},
		PullRequest: GitHubEventPullRequest{Number: 42, Title: "Fix the thing", State: "open",
			URL: "https://github.com/octo/api/pull/42", Author: "octocat", Labels: []string{"bug"},
			Head: GitHubEventRef{Ref: "fix", SHA: strings.Repeat("a", 40), Repository: "octo/api"},
			Base: GitHubEventRef{Ref: "main", SHA: strings.Repeat("b", 40)}}}
}

func TestGitHubPullRequestEventValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*GitHubPullRequestEvent)
		ok     bool
	}{
		{"valid", func(*GitHubPullRequestEvent) {}, true},
		{"ready for review", func(e *GitHubPullRequestEvent) { e.Action = "ready_for_review" }, true},
		{"unsupported action", func(e *GitHubPullRequestEvent) { e.Action = "labeled" }, false},
		{"no installation", func(e *GitHubPullRequestEvent) { e.InstallationID = 0 }, false},
		{"bad repository", func(e *GitHubPullRequestEvent) { e.Repository.FullName = "octo" }, false},
		{"dot repository", func(e *GitHubPullRequestEvent) { e.Repository.FullName = "../api" }, false},
		{"no number", func(e *GitHubPullRequestEvent) { e.PullRequest.Number = 0 }, false},
		{"bad state", func(e *GitHubPullRequestEvent) { e.PullRequest.State = "merged" }, false},
		{"short head", func(e *GitHubPullRequestEvent) { e.PullRequest.Head.SHA = "abc" }, false},
		{"upper head", func(e *GitHubPullRequestEvent) { e.PullRequest.Head.SHA = strings.Repeat("A", 40) }, false},
		{"no base", func(e *GitHubPullRequestEvent) { e.PullRequest.Base.SHA = "" }, false},
		{"bad head repository", func(e *GitHubPullRequestEvent) { e.PullRequest.Head.Repository = "x" }, false},
		{"http url", func(e *GitHubPullRequestEvent) { e.PullRequest.URL = "http://github.com/x" }, false},
		{"long title", func(e *GitHubPullRequestEvent) {
			e.PullRequest.Title = strings.Repeat("x", GitHubEventTitleMaxBytes+1)
		}, false},
		{"invalid title", func(e *GitHubPullRequestEvent) { e.PullRequest.Title = "\xff" }, false},
		{"nul title", func(e *GitHubPullRequestEvent) { e.PullRequest.Title = "a\x00b" }, false},
		{"too many labels", func(e *GitHubPullRequestEvent) {
			e.PullRequest.Labels = make([]string, GitHubEventLabelsMax+1)
			for i := range e.PullRequest.Labels {
				e.PullRequest.Labels[i] = "l"
			}
		}, false},
		{"empty label", func(e *GitHubPullRequestEvent) { e.PullRequest.Labels = []string{""} }, false},
		{"long ref", func(e *GitHubPullRequestEvent) { e.PullRequest.Head.Ref = strings.Repeat("r", 257) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := validGitHubPullRequestEvent()
			tc.mutate(&event)
			err := event.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrGitHubEventInvalid) {
				t.Fatalf("Validate() = %v, want ErrGitHubEventInvalid", err)
			}
		})
	}
	if got := validGitHubPullRequestEvent().Type(); got != "pull_request.opened" {
		t.Fatalf("Type() = %q", got)
	}
}
