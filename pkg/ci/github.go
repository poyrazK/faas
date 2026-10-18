package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrGitHubIgnored     = errors.New("CI GitHub event ignored")
	ErrGitHubAccess      = errors.New("CI GitHub repository access denied")
	ErrGitHubUnavailable = errors.New("CI GitHub integration unavailable")
	ErrNoGitHubWork      = errors.New("no CI GitHub work due")
)

type GitHubBindingRequest struct {
	RevisionID          string   `json:"revision_id"`
	InstallationID      int64    `json:"installation_id"`
	RepositoryID        int64    `json:"repository_id"`
	PushBranches        []string `json:"push_branches"`
	PullRequestBranches []string `json:"pull_request_branches"`
}

type GitHubBinding struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	GitHubBindingRequest
	Repository string    `json:"repository"`
	Enabled    bool      `json:"enabled"`
	Generation int64     `json:"generation"`
	CreatedAt  time.Time `json:"created_at"`
}

// GitHubEvent contains authenticated source identity only. It carries no
// commands, credentials, webhook body, changed files, or remote pipeline YAML.
type GitHubEvent struct {
	Kind           string `json:"kind"`
	InstallationID int64  `json:"installation_id"`
	RepositoryID   int64  `json:"repository_id"`
	Repository     string `json:"repository"`
	CommitSHA      string `json:"commit_sha"`
	Branch         string `json:"branch"` // push branch or PR base branch
	PRNumber       int    `json:"pr_number,omitempty"`
	BeforeSHA      string `json:"before_sha,omitempty"` // comparison evidence; never part of logical dedup identity
}

type GitHubSource struct {
	GitHubEvent
	BindingID  string `json:"binding_id"`
	Generation int64  `json:"binding_generation"`
	DeliveryID string `json:"delivery_id"`
}

type GitHubTrigger struct {
	ID         string        `json:"id"`
	AccountID  string        `json:"account_id"`
	Binding    GitHubBinding `json:"binding"`
	Source     GitHubSource  `json:"source"`
	Status     string        `json:"status"`
	Attempts   int           `json:"attempts"`
	RunID      string        `json:"run_id,omitempty"`
	LeaseToken string        `json:"-"`
}

type GitHubCheck struct {
	AccountID  string
	RunID      string
	Pipeline   string
	Source     GitHubSource
	Attempts   int
	CheckID    int64
	LeaseToken string
}

func (c GitHubCheck) Name() string       { return "gregale-ci / " + c.Pipeline + " / " + c.Source.BindingID }
func (c GitHubCheck) ExternalID() string { return "gregale-ci:" + c.RunID }

// GitHubStore separates authenticated inbox and delivery bookkeeping (githubd)
// from customer intent (bindings and run admission, written only by apid).
type GitHubStore interface {
	PutGitHubBinding(context.Context, string, string, GitHubBindingRequest) (GitHubBinding, error)
	GitHubBindings(context.Context, string) ([]GitHubBinding, error)
	DisableGitHubBinding(context.Context, string, string) error
	RecordGitHubDelivery(context.Context, string, string, GitHubEvent) error
	ClaimGitHubTrigger(context.Context, string) (GitHubTrigger, error)
	AdmitGitHubTrigger(context.Context, GitHubTrigger) (Run, error)
	FailGitHubTrigger(context.Context, GitHubTrigger, string) error
	ClaimGitHubCheck(context.Context, string) (GitHubCheck, error)
	FinishGitHubCheck(context.Context, GitHubCheck, int64, string) error
}

type GitHubVerifier func(context.Context, string, int64, int64, string) error
type GitHubPublisher func(context.Context, GitHubCheck) (int64, error)

func NormalizeGitHubBinding(req GitHubBindingRequest) (GitHubBindingRequest, error) {
	if !ValidID(req.RevisionID) || req.InstallationID <= 0 || req.RepositoryID <= 0 || len(req.PushBranches)+len(req.PullRequestBranches) == 0 {
		return req, fmt.Errorf("CI binding requires revision, installation, repository IDs and explicit branch filters")
	}
	req.PushBranches, req.PullRequestBranches = slices.Clone(req.PushBranches), slices.Clone(req.PullRequestBranches)
	for _, branches := range [][]string{req.PushBranches, req.PullRequestBranches} {
		if len(branches) > api.CIGitHubMaxBranches {
			return req, fmt.Errorf("too many CI branch filters")
		}
		slices.Sort(branches)
		for i, branch := range branches {
			if !validGitHubBranch(branch) || i > 0 && branches[i-1] == branch {
				return req, fmt.Errorf("invalid or duplicate CI branch filter")
			}
		}
	}
	return req, nil
}

func validGitHubBranch(branch string) bool {
	if branch == "" || len(branch) > api.CIGitHubBranchMaxBytes || !utf8.ValidString(branch) ||
		strings.ContainsAny(branch, " ~^:?*[\\\x00\r\n\t") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") ||
		strings.HasSuffix(branch, ".") || branch == "@" {
		return false
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	for _, r := range branch {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func (e GitHubEvent) Key() string {
	canonical := e
	canonical.Repository = strings.ToLower(canonical.Repository)
	canonical.BeforeSHA = ""
	data, _ := json.Marshal(canonical)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (e GitHubEvent) valid() bool {
	return (e.Kind == "push" && e.PRNumber == 0 || e.Kind == "pull_request" && e.PRNumber > 0) &&
		e.InstallationID > 0 && e.RepositoryID > 0 && repoPattern.MatchString(e.Repository) && len(e.Repository) <= api.CIRepositoryMaxBytes &&
		ValidCommitSHA(e.CommitSHA) && e.CommitSHA != strings.Repeat("0", 40) && validGitHubBranch(e.Branch)
}

func (e GitHubEvent) Valid() bool { return e.valid() }

func bindingMatches(b GitHubBinding, e GitHubEvent) bool {
	branches := b.PushBranches
	if e.Kind == "pull_request" {
		branches = b.PullRequestBranches
	}
	return b.Enabled && e.valid() && b.InstallationID == e.InstallationID && b.RepositoryID == e.RepositoryID &&
		strings.EqualFold(b.Repository, e.Repository) && slices.Contains(branches, e.Branch)
}

func sameAccount(a, b string) bool {
	x, err := uuid.Parse(a)
	if err != nil {
		return false
	}
	y, err := uuid.Parse(b)
	return err == nil && x == y
}

type gitHubWorkRecord[T any] struct {
	Value    T
	Until    time.Time
	Next     time.Time
	Token    string
	Status   string
	Attempts int
}

func githubRetry(attempt int, now time.Time) time.Time {
	return now.Add(time.Duration(min(1<<min(attempt, api.CIGitHubMaxAttempts), api.CIGitHubMaxBackoffSeconds)) * time.Second)
}

func githubWorkDue(status, active string, until, next, now time.Time) bool {
	return status == "pending" && !now.Before(next) || status == active && !now.Before(until)
}

func githubFailureState(attempts int, reason string) string {
	if reason == "access_denied" || reason == "binding_disabled" {
		return "ignored"
	}
	if attempts >= api.CIGitHubMaxAttempts {
		return "dead"
	}
	return "pending"
}

func githubBindingStillAuthorized(current, captured GitHubBinding) bool {
	return current.Enabled && current.ID == captured.ID && sameAccount(current.AccountID, captured.AccountID) &&
		current.InstallationID == captured.InstallationID && current.RepositoryID == captured.RepositoryID && strings.EqualFold(current.Repository, captured.Repository)
}

func sameGitHubBindingRequest(a, b GitHubBindingRequest) bool {
	return a.RevisionID == b.RevisionID && a.InstallationID == b.InstallationID && a.RepositoryID == b.RepositoryID && slices.Equal(a.PushBranches, b.PushBranches) && slices.Equal(a.PullRequestBranches, b.PullRequestBranches)
}
