package ci

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// ChangedFiles is an exact-SHA, bounded comparison. Complete is affirmative
// evidence; absent, invalid or truncated results cannot justify skipping CI.
type ChangedFiles struct {
	BaseSHA, HeadSHA, MergeBaseSHA string
	Complete                       bool
	Files                          []ChangedFile
}

type ChangedFile struct {
	Path         string
	PreviousPath string
	Status       string
}

type AutomationChangesResolver func(context.Context, GitHubBinding, string, string) (ChangedFiles, error)

type AutomationPathDecision struct {
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason"`
	BaseSHA      string `json:"base_sha,omitempty"`
	HeadSHA      string `json:"head_sha"`
	MergeBaseSHA string `json:"merge_base_sha,omitempty"`
	FileCount    int    `json:"file_count"`
}

func normalizeAutomationPaths(req AutomationRequest) (AutomationRequest, error) {
	req.IncludePaths, req.ExcludePaths = slices.Clone(req.IncludePaths), slices.Clone(req.ExcludePaths)
	for _, patterns := range [][]string{req.IncludePaths, req.ExcludePaths} {
		if len(patterns) > api.CIAutomationMaxPathFilters {
			return req, ErrConflict
		}
		slices.Sort(patterns)
		for i, pattern := range patterns {
			if !validAutomationPath(pattern, true) || i > 0 && pattern == patterns[i-1] {
				return req, ErrConflict
			}
		}
	}
	return req, nil
}

func validAutomationPath(value string, pattern bool) bool {
	limit := api.CIAutomationChangedPathMaxBytes
	if pattern {
		limit = api.CIAutomationPathPatternMaxBytes
	}
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, '\\') {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return false
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		if pattern && (strings.ContainsAny(part, "[]{}!") || strings.Contains(part, "**") && part != "**") {
			return false
		}
	}
	return true
}

// ** is a whole path component matching zero or more components. * and ?
// match within one component. There are no negations, escapes or character sets.
func automationPathMatches(pattern, filename string) bool {
	parts, names := strings.Split(pattern, "/"), strings.Split(filename, "/")
	matched := make([]bool, len(names)+1)
	matched[0] = true
	for _, part := range parts {
		next := make([]bool, len(names)+1)
		for i := range next {
			if part == "**" {
				next[i] = matched[i] || i > 0 && next[i-1]
			} else if i > 0 && matched[i-1] {
				ok, _ := path.Match(part, names[i-1])
				next[i] = matched[i-1] && ok
			}
		}
		matched = next
	}
	return matched[len(names)]
}

func (changes ChangedFiles) Valid(base, head string) bool {
	if !changes.Complete || !ValidCommitSHA(base) || !ValidCommitSHA(head) ||
		base == strings.Repeat("0", 40) || head == strings.Repeat("0", 40) ||
		changes.BaseSHA != base || changes.HeadSHA != head || !ValidCommitSHA(changes.MergeBaseSHA) ||
		changes.MergeBaseSHA == strings.Repeat("0", 40) || len(changes.Files) >= api.CIAutomationCompareMaxFiles {
		return false
	}
	seen := map[string]bool{}
	for _, file := range changes.Files {
		if !validAutomationPath(file.Path, false) || seen[file.Path] {
			return false
		}
		seen[file.Path] = true
		switch file.Status {
		case "added", "removed", "modified", "changed", "unchanged":
			if file.PreviousPath != "" {
				return false
			}
		case "renamed", "copied":
			if !validAutomationPath(file.PreviousPath, false) || file.PreviousPath == file.Path {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func prepareAutomationPaths(ctx context.Context, verify GitHubVerifier, event AutomationEvent, pr *automationPullRequest, resolve AutomationChangesResolver) (*AutomationPathDecision, error) {
	if len(event.Policy.IncludePaths)+len(event.Policy.ExcludePaths) == 0 {
		return nil, nil
	}
	d := &AutomationPathDecision{Outcome: "run", Reason: "changes_unavailable", HeadSHA: event.Source.CommitSHA}
	base := event.Source.BeforeSHA
	if event.Source.Kind == "pull_request" {
		if pr == nil || pullRequestIneligible(event, pr.State) != "" || pr.State.HeadSHA != event.Source.CommitSHA {
			return d, nil
		}
		base = pr.State.BaseSHA
	}
	if !ValidCommitSHA(base) || base == strings.Repeat("0", 40) || resolve == nil {
		return d, nil
	}
	d.BaseSHA = base
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	b := event.Binding
	if err := verify(ctx, event.AccountID, b.InstallationID, b.RepositoryID, b.Repository); err != nil {
		if errors.Is(err, ErrGitHubAccess) {
			return nil, ErrGitHubAccess
		}
		return nil, ErrGitHubUnavailable
	}
	changes, err := resolve(ctx, b, base, event.Source.CommitSHA)
	if err != nil || ctx.Err() != nil || !changes.Valid(base, event.Source.CommitSHA) || event.Source.Kind == "push" && changes.MergeBaseSHA != base {
		return d, nil
	}
	d.MergeBaseSHA, d.FileCount = changes.MergeBaseSHA, len(changes.Files)
	d.Outcome, d.Reason = "skip", "paths_filtered"
	for _, file := range changes.Files {
		if ctx.Err() != nil {
			return &AutomationPathDecision{Outcome: "run", Reason: "changes_unavailable", BaseSHA: base, HeadSHA: event.Source.CommitSHA}, nil
		}
		for _, filename := range []string{file.Path, file.PreviousPath} {
			if filename == "" {
				continue
			}
			included := len(event.Policy.IncludePaths) == 0
			for _, pattern := range event.Policy.IncludePaths {
				included = included || automationPathMatches(pattern, filename)
			}
			for _, pattern := range event.Policy.ExcludePaths {
				if automationPathMatches(pattern, filename) {
					included = false
					break
				}
			}
			if included {
				d.Outcome, d.Reason = "run", "paths_matched"
				return d, nil
			}
		}
	}
	if ctx.Err() != nil {
		d.Outcome, d.Reason = "run", "changes_unavailable"
	}
	return d, nil
}
