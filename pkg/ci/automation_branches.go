package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AutomationHeadVerifier resolves a branch through account-owned installation
// authority. No token or source archive is returned to the CI controller.
type AutomationHeadVerifier func(context.Context, GitHubBinding, string) (string, bool, error)

type automationHead struct {
	Fence int64
	SHA   string
}

type automationBranchStore interface {
	beginAutomationHead(context.Context, AutomationEvent) (int64, error)
}

// A committed queue/cancellation/supersession decision is not an admission
// failure. Its claim is settled inside the admission transaction.
var errAutomationDeferred = errors.New("CI automation branch decision committed")

func prepareAutomationHead(ctx context.Context, store AutomationStore, verify GitHubVerifier, event AutomationEvent, heads []AutomationHeadVerifier) (*automationHead, error) {
	if !event.Policy.LatestOnly {
		return nil, nil
	}
	branchStore, ok := store.(automationBranchStore)
	if !ok || len(heads) != 1 || heads[0] == nil {
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
	fence, err := branchStore.beginAutomationHead(ctx, event)
	if err != nil {
		return nil, err
	}
	sha, found, err := heads[0](ctx, b, event.Source.Branch)
	if err != nil || found && (!ValidCommitSHA(sha) || sha == "0000000000000000000000000000000000000000") || !found && sha != "" {
		return nil, ErrGitHubUnavailable
	}
	return &automationHead{Fence: fence, SHA: sha}, nil
}
