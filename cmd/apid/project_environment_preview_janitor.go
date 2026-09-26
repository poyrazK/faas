package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectEnvironmentPreviewJanitorInterval  = time.Minute
	projectEnvironmentPreviewJanitorBatchSize = 20
)

func (s *server) runProjectEnvironmentPreviewJanitor(ctx context.Context) {
	store, ok := s.store.(state.ProjectEnvironmentPreviewLifecycleStore)
	if !ok {
		if s.log != nil {
			s.log.Warn("project environment preview janitor disabled: state store does not support preview lifecycle")
		}
		return
	}
	if s.log != nil {
		s.log.Info("project environment preview janitor started")
	}
	ticker := time.NewTicker(projectEnvironmentPreviewJanitorInterval)
	defer ticker.Stop()
	for {
		if err := s.sweepProjectEnvironmentPreviews(ctx, store, time.Now().UTC()); err != nil && ctx.Err() == nil && s.log != nil {
			s.log.Warn("project environment preview sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) sweepProjectEnvironmentPreviews(
	ctx context.Context, store state.ProjectEnvironmentPreviewLifecycleStore, now time.Time,
) error {
	environments, err := store.ListProjectEnvironmentPreviewsForTeardown(ctx, now, projectEnvironmentPreviewJanitorBatchSize)
	if err != nil {
		return err
	}
	var sweepErr error
	for _, environment := range environments {
		if err := ctx.Err(); err != nil {
			return errors.Join(sweepErr, err)
		}
		wasTearingDown := environment.PreviewState == state.ProjectEnvironmentPreviewTearingDown
		draining, deployments, beginErr := store.BeginProjectEnvironmentPreviewTeardown(
			ctx, environment.AccountID, environment.ProjectID, environment.Slug,
			now, now.Add(state.ProjectEnvironmentPreviewDrainGrace),
		)
		if errors.Is(beginErr, state.ErrConflict) || errors.Is(beginErr, state.ErrNotFound) {
			continue // A webhook or another janitor won the race.
		}
		if beginErr != nil {
			sweepErr = errors.Join(sweepErr, beginErr)
			continue
		}
		if !wasTearingDown {
			s.audit.Emit(ctx, "project.environment.preview_tearing_down", &draining.AccountID, map[string]any{
				"project_id": draining.ProjectID, "environment_id": draining.ID,
				"environment_slug": draining.Slug, "preview_pr_number": draining.PreviewPRNumber,
			})
		}
		for _, deployment := range deployments {
			payload, marshalErr := json.Marshal(map[string]string{
				"deployment_id": deployment.DeploymentID, "app_id": deployment.AppID, "status": string(state.DeploySuperseded),
			})
			if marshalErr != nil {
				sweepErr = errors.Join(sweepErr, marshalErr)
				continue
			}
			if s.notif != nil {
				if notifyErr := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); notifyErr != nil {
					sweepErr = errors.Join(sweepErr, notifyErr)
					if s.log != nil {
						s.log.Warn("project preview release drain notification failed", "deployment_id", deployment.DeploymentID, "err", notifyErr)
					}
				}
			}
		}
		if draining.PreviewExpiresAt != nil && draining.PreviewExpiresAt.After(now) {
			continue // Preserve a drain grace after the latest live release stops serving.
		}

		project, projectErr := s.store.ProjectByID(ctx, environment.ProjectID)
		if errors.Is(projectErr, state.ErrNotFound) || projectErr == nil && project.AccountID != environment.AccountID {
			// An environment cannot outlive its owning project. Treat a stale row as
			// a repairable condition rather than deleting against another account.
			if s.log != nil {
				s.log.Warn("expired project preview has no matching project; retaining for reconciliation", "project_id", environment.ProjectID, "environment", environment.Slug)
			}
			continue
		}
		if projectErr != nil {
			sweepErr = errors.Join(sweepErr, projectErr)
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, projectEnvironmentCleanupLeaseDuration-time.Minute)
		cleanupPending, deleteErr := s.deleteProjectEnvironmentWithCleanup(cleanupCtx, state.Account{ID: environment.AccountID}, project, environment)
		cancel()
		if errors.Is(deleteErr, state.ErrNotFound) && !cleanupPending {
			continue // Already removed by an operator or another janitor.
		}
		if errors.Is(deleteErr, state.ErrConflict) && !cleanupPending {
			if s.log != nil {
				s.log.Warn("expired project preview retained because it has a bound domain or live release", "project_id", project.ID, "environment", environment.Slug)
			}
			continue
		}
		if deleteErr != nil && !cleanupPending {
			sweepErr = errors.Join(sweepErr, deleteErr)
			continue
		}
		s.audit.Emit(ctx, "project.environment.preview_expired", &environment.AccountID, map[string]any{
			"project_id": project.ID, "environment_id": environment.ID,
			"environment_slug": environment.Slug, "preview_pr_number": environment.PreviewPRNumber,
		})
		if cleanupPending && s.log != nil {
			s.log.Warn("expired project preview was deleted; managed resource cleanup remains queued", "project_id", project.ID, "environment", environment.Slug)
		}
	}
	return sweepErr
}

// deleteProjectEnvironmentWithCleanup is shared by the HTTP delete path and
// the preview janitor so provider resources always use the durable cleanup
// queue before the environment registry row is removed.
func (s *server) deleteProjectEnvironmentWithCleanup(
	ctx context.Context, acct state.Account, project state.Project, environment state.ProjectEnvironment,
) (bool, error) {
	resourceCleanup, err := s.planProjectEnvironmentManagedResourceCleanup(ctx, acct, project, environment.Slug)
	if err != nil {
		return false, err
	}
	cleanupResources := projectEnvironmentCleanupResources(resourceCleanup)
	var cleanupJob state.ProjectEnvironmentCleanupJob
	var cleanupStore state.ProjectEnvironmentCleanupStore
	if !cleanupResources.Empty() {
		var supported bool
		cleanupStore, supported = s.store.(state.ProjectEnvironmentCleanupStore)
		if !supported {
			return false, errors.New("durable managed-resource cleanup is unavailable")
		}
		cleanupJob, err = cleanupStore.DeleteProjectEnvironmentWithCleanup(
			ctx, acct.ID, project.ID, environment.Slug, cleanupResources,
			uuid.NewString(), projectEnvironmentCleanupLeaseDuration,
		)
	} else {
		err = s.store.DeleteProjectEnvironment(ctx, acct.ID, project.ID, environment.Slug)
	}
	if err != nil {
		return false, err
	}
	if cleanupJob.ID == "" {
		return false, nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), projectEnvironmentCleanupLeaseDuration-time.Minute)
	defer cancel()
	cleanupErr := s.cleanupProjectEnvironmentManagedResourcePayload(cleanupCtx, acct, cleanupJob.Resources)
	if cleanupErr == nil {
		cleanupErr = cleanupStore.CompleteProjectEnvironmentCleanup(cleanupCtx, cleanupJob.ID, cleanupJob.LeaseToken)
	} else {
		retryErr := cleanupStore.RetryProjectEnvironmentCleanup(
			cleanupCtx, cleanupJob.ID, cleanupJob.LeaseToken,
			time.Now().UTC().Add(projectEnvironmentCleanupRetryDelay(cleanupJob.AttemptCount+1)),
		)
		cleanupErr = errors.Join(cleanupErr, retryErr)
	}
	return cleanupErr != nil, cleanupErr
}
