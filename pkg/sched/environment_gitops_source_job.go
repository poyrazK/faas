package sched

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) validateEnvironmentGitOpsSourceJob(ctx context.Context, job state.Job, run state.JobRun, intent state.EnvironmentWorkloadIntent) error {
	if intent.Source != nil && intent.Source.Kind == "image" {
		return nil
	}
	id, ok := state.EnvironmentGitOpsJobSourceDeployment(job)
	if !ok || !environmentGitOpsJobBindingIntentMatches(job, run, job.AccountID, intent) {
		return state.ErrConflict
	}
	dep, err := e.store.DeploymentByID(ctx, id)
	if err != nil {
		return err
	}
	if dep.Status != state.DeployLive || dep.EnvironmentWorkloadHeld() || !state.EnvironmentGitOpsSourceJobArtifactMatches(intent, job, dep) {
		return state.ErrConflict
	}
	releases, ok := e.store.(interface {
		ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
		ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error)
		EnvironmentGitSource(context.Context, string, string, string) (state.EnvironmentGitSource, error)
	})
	if !ok {
		return state.ErrConflict
	}
	env, err := releases.ProjectEnvironmentByID(ctx, intent.EnvironmentID)
	if err != nil {
		return err
	}
	if env.AccountID != job.AccountID || env.ID != intent.EnvironmentID || dep.Scope != env.Slug {
		return state.ErrConflict
	}
	source, err := releases.EnvironmentGitSource(ctx, job.AccountID, env.ProjectID, env.Slug)
	if err != nil {
		return err
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || frozen.SourceID != source.ID || frozen.SourceRepositoryID != source.Spec.RepositoryID ||
		frozen.SourceProvider != source.Spec.Provider || !strings.EqualFold(frozen.SourceRepository, source.Spec.Repository) ||
		source.Spec.Provider == "gitlab" && frozen.SourceRepository != source.Spec.Repository {
		return state.ErrConflict
	}
	if frozen.BuiltArtifact != nil {
		// Authenticate the current producer and publisher again at dispatch and
		// while the guest's start gate is held. Retention is not execution trust.
		producers, ok := e.store.(state.DeploymentRuntimeProducerInputStore)
		if !ok {
			return state.ErrConflict
		}
		if _, err := producers.GetFreshDeploymentRuntimeProducerInputs(ctx, job.AccountID, intent.AppID, dep.ID); err != nil {
			return err
		}
	}
	release, err := releases.ActiveProjectReleaseSet(ctx, job.AccountID, env.ProjectID, env.Slug)
	if err != nil {
		return err
	}
	for _, member := range release.Members {
		if release.Active && member.AppID == intent.AppID && member.DeploymentID == dep.ID {
			return nil
		}
	}
	return state.ErrConflict
}

// Repeat the whole identity check while the native guest's start gate is
// still closed. A build completion or a cached check cannot authorize a
// superseded schedule, different layer, or replacement active release.
func (e *Engine) recheckEnvironmentGitOpsSourceJob(ctx context.Context, original state.Job, run state.JobRun) error {
	job, err := e.store.JobGetByID(ctx, original.ID)
	if err != nil {
		return err
	}
	intents, ok := e.store.(state.EnvironmentWorkloadIntentStore)
	if !ok {
		return state.ErrConflict
	}
	intent, err := intents.EnvironmentWorkloadIntentByJob(ctx, job.AccountID, job.ID)
	if err != nil {
		return err
	}
	return e.validateEnvironmentGitOpsSourceJob(ctx, job, run, intent)
}
