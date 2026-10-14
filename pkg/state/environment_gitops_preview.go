package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

// Candidate previews require neither an approved revision nor a worker lease.
// The verified binding is compared again inside the observation transaction.
type EnvironmentGitOpsPreviewStore interface {
	PreviewEnvironmentGitOpsCommit(context.Context, EnvironmentGitSource, string, environmentsync.DesiredState) (api.PreviewEnvironmentGitPlanResponse, error)
}

func validateEnvironmentGitOpsPreview(source EnvironmentGitSource, commit string, desired environmentsync.DesiredState) error {
	compiled, err := environmentsync.Compile(desired.Definition)
	if source.ID == "" || source.AccountID == "" || source.Generation < 0 || !environmentCommitRE.MatchString(commit) || err != nil || compiled.Digest != desired.Digest {
		return ErrInvalidArgument
	}
	return nil
}

func environmentGitOpsCommitPreview(source EnvironmentGitSource, commit string, desired environmentsync.DesiredState, observation EnvironmentGitOpsObservation) (api.PreviewEnvironmentGitPlanResponse, error) {
	// A separate revision namespace keeps a candidate plan from becoming an
	// adoption receipt, even if these bytes are subsequently approved.
	plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{
		Manager: source.ID, Revision: "candidate:" + commit, CommitSHA: commit, Generation: source.Generation,
		Preview: true, Prune: source.Spec.Prune, Overrides: observation.Overrides, Now: time.Now().UTC()})
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	plan.Changes = slices.Clone(plan.Changes)
	for i := range plan.Changes {
		plan.Changes[i].Before, plan.Changes[i].After = nil, nil
	}
	provider := source.Spec.Provider
	if provider == "" {
		provider = "github"
	}
	return api.PreviewEnvironmentGitPlanResponse{SourceID: source.ID, Provider: provider,
		Project: desired.Definition.Project, Environment: desired.Definition.Environment,
		ObservedAt: time.Now().UTC(), ReadOnly: true, ValuesRedacted: true, Plan: plan}, nil
}
