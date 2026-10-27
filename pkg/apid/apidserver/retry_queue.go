package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/apid/trafficproblem"
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
	// A retry is a new release: an image retry needs no builder, so only
	// this check holds it during the break-glass pause (ADR 20261022140000).
	if p := s.platformReleasePauseProblem(); p != nil {
		return state.Deployment{}, p
	}
	if p := s.retryReleaseProblem(ctx, app, dep); p != nil {
		return state.Deployment{}, p
	}
	acct, err := state.AppResourceAccount(ctx, s.store, app)
	if err != nil {
		return state.Deployment{}, err
	}
	limits := api.MustLimitsFor(acct.Plan)
	if dep.SourceBytes > int64(limits.SourceTarballMaxMB)*1024*1024 {
		return state.Deployment{}, api.ErrSourceTooLarge(limits, dep.SourceBytes)
	}
	specs, err := s.retryOperationDefinitions(ctx, app, dep)
	if err != nil {
		return state.Deployment{}, err
	}
	if dep.Kind == state.DeploymentKindImage || dep.Kind == "" {
		return s.enqueueImageRetry(ctx, dep, from, specs)
	}
	build, err := s.store.BuildByDeployment(ctx, dep.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.Deployment{}, err
	}
	result, err := apidsource.Enqueue(ctx, s.store, s.notif, apidsource.EnqueueParams{
		OperationDefinitions: specs, OperationAdmissionEnabled: s.operation.OperationDefinitionsAdmission(app.AccountID, app.ID, dep.Scope, specs),
		RetryOf: dep.ID, RetryFrom: from, AppID: app.ID, Kind: dep.Kind,
		SourcePath: dep.SourcePath, SourceBytes: dep.SourceBytes,
		SourceBuildID: build.ID,
		LogSpool:      spoolRoot(), Log: s.log,
		ServiceRollout: app.Manifest.ExecutionMode == api.ExecutionModeService,
	})
	if err != nil {
		if errors.Is(err, apidsource.ErrRetrySourceUnavailable) {
			return state.Deployment{}, api.NewProblem(http.StatusConflict, api.CodeSourceInvalid,
				"Retry source unavailable", "The original source archive is no longer available. Upload the source again to deploy.")
		}
		return state.Deployment{}, retryTrafficProblem(err)
	}
	created, err := s.store.DeploymentByID(ctx, result.DeploymentID)
	created.BuildID = result.BuildID
	return created, err
}

// A retry's new revision URL is a new binding (ADR-570); a refusal is the
// customer's policy problem, not an internal error.
func retryTrafficProblem(err error) error {
	if problem := trafficproblem.Policy(err, nil); problem != nil {
		return problem
	}
	return err
}

func (s *server) enqueueImageRetry(ctx context.Context, dep state.Deployment, from state.StageName, specs []api.OperationDefinitionSpec) (state.Deployment, error) {
	created, err := s.store.RetryDeploymentFromStage(ctx, dep.ID, from)
	if err != nil {
		return state.Deployment{}, retryTrafficProblem(err)
	}
	if err := s.installRetryOperations(ctx, created, specs); err != nil {
		_ = s.store.FailSourceDeployment(context.WithoutCancel(ctx), created.ID, "operation contract retry failed")
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
