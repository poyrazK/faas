package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectEnvironmentCleanupLeaseDuration = 5 * time.Minute
	projectEnvironmentCleanupInterval      = 30 * time.Second
	projectEnvironmentCleanupBatchSize     = 20
)

func projectEnvironmentCleanupRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	return min(delay, time.Hour)
}

func (s *server) runProjectEnvironmentCleanupReconciler(ctx context.Context) {
	store, ok := s.store.(state.ProjectEnvironmentCleanupStore)
	if !ok {
		if s.log != nil {
			s.log.Warn("project environment cleanup reconciler disabled: state store does not support durable cleanup")
		}
		return
	}
	if s.log != nil {
		s.log.Info("project environment cleanup reconciler started")
	}
	ticker := time.NewTicker(projectEnvironmentCleanupInterval)
	defer ticker.Stop()
	for {
		if err := s.sweepProjectEnvironmentCleanup(ctx, store); err != nil && ctx.Err() == nil && s.log != nil {
			s.log.Warn("project environment cleanup sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) sweepProjectEnvironmentCleanup(ctx context.Context, store state.ProjectEnvironmentCleanupStore) error {
	var sweepErr error
	for i := 0; i < projectEnvironmentCleanupBatchSize; i++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(sweepErr, err)
		}
		leaseToken := uuid.NewString()
		job, err := store.ClaimNextProjectEnvironmentCleanup(ctx, leaseToken, time.Now().UTC(), projectEnvironmentCleanupLeaseDuration)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return errors.Join(sweepErr, err)
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, projectEnvironmentCleanupLeaseDuration-time.Minute)
		cleanupErr := s.cleanupProjectEnvironmentManagedResourcePayload(cleanupCtx, state.Account{ID: job.AccountID}, job.Resources)
		cancel()
		if cleanupErr != nil {
			nextAttempt := time.Now().UTC().Add(projectEnvironmentCleanupRetryDelay(job.AttemptCount))
			retryErr := store.RetryProjectEnvironmentCleanup(context.WithoutCancel(ctx), job.ID, job.LeaseToken, nextAttempt)
			if s.log != nil {
				s.log.Warn("project environment managed resource cleanup will retry", "project_id", job.ProjectID, "environment", job.EnvironmentSlug, "err", cleanupErr)
			}
			sweepErr = errors.Join(sweepErr, cleanupErr, retryErr)
			continue
		}
		if err := store.CompleteProjectEnvironmentCleanup(context.WithoutCancel(ctx), job.ID, job.LeaseToken); err != nil {
			sweepErr = errors.Join(sweepErr, err)
			if s.log != nil {
				s.log.Warn("project environment cleanup completion will retry after lease expiry", "project_id", job.ProjectID, "environment", job.EnvironmentSlug, "err", err)
			}
		}
	}
	return sweepErr
}
