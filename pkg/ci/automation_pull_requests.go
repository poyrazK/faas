package ci

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// PullRequestState is a fresh, credential-free observation from githubd. It
// never grants source access or replaces the captured immutable commit.
type PullRequestState struct {
	Number                             int64
	State                              string
	Draft                              bool
	HeadSHA, BaseBranch, BaseSHA       string
	HeadRepository, BaseRepository     string
	HeadRepositoryID, BaseRepositoryID int64
}

type AutomationPullRequestVerifier func(context.Context, GitHubBinding, int) (PullRequestState, error)

type AutomationResolvers struct {
	Branch      AutomationHeadVerifier
	PullRequest AutomationPullRequestVerifier
	Changes     AutomationChangesResolver
}

func automationBranchResolvers(heads []AutomationHeadVerifier) AutomationResolvers {
	var resolvers AutomationResolvers
	if len(heads) == 1 {
		resolvers.Branch = heads[0]
	}
	return resolvers
}

type automationPullRequest struct {
	Fence int64
	State PullRequestState
}

type automationPullRequestStore interface {
	beginAutomationPullRequest(context.Context, AutomationEvent) (int64, error)
}

func automationPolicyMatches(policy AutomationPolicy, event GitHubEvent) bool {
	return event.Kind == "push" && slices.Contains(policy.PushBranches, event.Branch) ||
		event.Kind == "pull_request" && event.PRNumber > 0 && slices.Contains(policy.PullRequestBranches, event.Branch)
}

// Valid rejects incomplete observations rather than interpreting omitted fields
// as a non-draft same-repository PR.
func (state PullRequestState) Valid(number int64) bool {
	return state.Number == number && number > 0 && (state.State == "open" || state.State == "closed") &&
		state.HeadRepositoryID > 0 && state.BaseRepositoryID > 0 &&
		repoPattern.MatchString(state.HeadRepository) && len(state.HeadRepository) <= api.CIRepositoryMaxBytes &&
		repoPattern.MatchString(state.BaseRepository) && len(state.BaseRepository) <= api.CIRepositoryMaxBytes &&
		ValidCommitSHA(state.HeadSHA) && state.HeadSHA != strings.Repeat("0", 40) && validGitHubBranch(state.BaseBranch)
}

func prepareAutomationPullRequest(ctx context.Context, store AutomationStore, verify GitHubVerifier, event AutomationEvent, resolve AutomationPullRequestVerifier) (*automationPullRequest, error) {
	prStore, ok := store.(automationPullRequestStore)
	if !ok || resolve == nil || event.Source.Kind != "pull_request" {
		return nil, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	b := event.Binding
	if err := verify(ctx, event.AccountID, b.InstallationID, b.RepositoryID, b.Repository); err != nil {
		if errors.Is(err, ErrGitHubAccess) {
			return nil, ErrGitHubAccess
		}
		return nil, ErrGitHubUnavailable
	}
	fence, err := prStore.beginAutomationPullRequest(ctx, event)
	if err != nil {
		return nil, err
	}
	state, err := resolve(ctx, b, event.Source.PRNumber)
	if err != nil || !state.Valid(int64(event.Source.PRNumber)) {
		return nil, ErrGitHubUnavailable
	}
	return &automationPullRequest{Fence: fence, State: state}, nil
}

func pullRequestIneligible(event AutomationEvent, state PullRequestState) string {
	if state.State == "closed" {
		return "pr_closed"
	}
	if state.Draft {
		return "pr_draft"
	}
	b := event.Binding
	if state.HeadRepositoryID != b.RepositoryID || state.BaseRepositoryID != b.RepositoryID ||
		!strings.EqualFold(state.HeadRepository, b.Repository) || !strings.EqualFold(state.BaseRepository, b.Repository) ||
		state.BaseBranch != event.Source.Branch || !slices.Contains(event.Policy.PullRequestBranches, state.BaseBranch) {
		return "pr_changed"
	}
	return ""
}
