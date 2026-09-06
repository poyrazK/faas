package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// enqueueRetry is shared by the API and dashboard. Creating a deployment row
// alone does not start builderd; source retries need the same durable build
// and source publication ordering as an initial upload.
func (s *server) enqueueRetry(ctx context.Context, app state.App, dep state.Deployment, from state.StageName) (state.Deployment, error) {
	if dep.Status != state.DeployFailed {
		return state.Deployment{}, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Deployment cannot be retried",
			fmt.Sprintf("Deployment %s has status %s; only failed deployments can be retried.", dep.ID, dep.Status))
	}
	if app.Status != state.AppActive {
		return state.Deployment{}, api.ErrSourceInvalid("Retry requires an active app.")
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return state.Deployment{}, err
	}
	limits := api.MustLimitsFor(acct.Plan)
	if dep.SourceBytes > int64(limits.SourceTarballMaxMB)*1024*1024 {
		return state.Deployment{}, api.ErrSourceTooLarge(limits, dep.SourceBytes)
	}
	if dep.Kind == state.DeploymentKindImage || dep.Kind == "" {
		return s.enqueueImageRetry(ctx, dep, from)
	}
	build, err := s.store.BuildByDeployment(ctx, dep.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.Deployment{}, err
	}
	result, err := apidsource.Enqueue(ctx, s.store, s.notif, apidsource.EnqueueParams{
		RetryOf: dep.ID, RetryFrom: from, AppID: app.ID, Kind: dep.Kind,
		SourcePath: dep.SourcePath, SourceBytes: dep.SourceBytes,
		SourceBuildID: build.ID,
		LogSpool:      spoolRoot(), Log: s.log,
	})
	if err != nil {
		if errors.Is(err, apidsource.ErrRetrySourceUnavailable) {
			return state.Deployment{}, api.NewProblem(http.StatusConflict, api.CodeSourceInvalid,
				"Retry source unavailable", "The original source archive is no longer available. Upload the source again to deploy.")
		}
		return state.Deployment{}, err
	}
	created, err := s.store.DeploymentByID(ctx, result.DeploymentID)
	created.BuildID = result.BuildID
	return created, err
}

func (s *server) enqueueImageRetry(ctx context.Context, dep state.Deployment, from state.StageName) (state.Deployment, error) {
	created, err := s.store.RetryDeploymentFromStage(ctx, dep.ID, from)
	if err != nil {
		return state.Deployment{}, err
	}
	payload, _ := json.Marshal(map[string]string{
		"app_id": created.AppID, "to": created.ID,
		"kind": string(state.DeploymentKindImage), "image_digest": created.ImageDigest,
	})
	if err := s.notif.Notify(ctx, db.NotifyDeploymentChanged, string(payload)); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.store.FailSourceDeployment(cleanupCtx, created.ID, "retry notification failed")
		return state.Deployment{}, fmt.Errorf("enqueue image retry: %w", err)
	}
	return created, nil
}
