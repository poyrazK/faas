package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	apiSandboxWorkerLease   = 45 * time.Second
	apiSandboxWorkerTimeout = 30 * time.Second
	apiSandboxSetupTimeout  = 20 * time.Minute
	apiSandboxRetryInterval = 5 * time.Second
)

func (s *server) apiSandboxWorkerEnabled() bool {
	_, ok := s.store.(state.APISandboxDeploymentStore)
	return ok && s.notif != nil && s.managedPostgres != nil && s.managedPostgresBindings != nil && !s.releasePhaseOff
}

func (s *server) runAPISandboxProvisioning(ctx context.Context) {
	store, ok := s.store.(state.APISandboxOperationStore)
	if !ok || !s.apiSandboxWorkerEnabled() {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.processAPISandboxOperation(ctx, store); err != nil && !errors.Is(err, state.ErrNotFound) && ctx.Err() == nil {
			s.log.WarnContext(ctx, "process API sandbox provisioning", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) processAPISandboxOperation(ctx context.Context, store state.APISandboxOperationStore) error {
	now := time.Now().UTC()
	claimCtx, cancelClaim := context.WithTimeout(ctx, 3*time.Second)
	op, err := store.ClaimAPISandboxOperation(claimCtx, now, apiSandboxWorkerLease)
	cancelClaim()
	if err != nil {
		return err
	}
	workCtx, cancelWork := context.WithDeadline(ctx, now.Add(apiSandboxWorkerTimeout))
	err = s.advanceAPISandboxOperation(workCtx, &op, now)
	cancelWork()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now = time.Now().UTC()
	retry := now.Add(apiSandboxRetryInterval)
	op.NextAttemptAt = &retry
	switch {
	case op.Phase == state.APISandboxLive || op.Phase == state.APISandboxFailed:
		op.NextAttemptAt = nil
	case !now.Before(op.StartedAt.Add(apiSandboxSetupTimeout)):
		if op.Failure == nil {
			op.Failure = apiSandboxTimeoutFailure(op.Phase)
		}
		op.Phase, op.NextAttemptAt = state.APISandboxFailed, nil
	}
	saveCtx, cancelSave := context.WithTimeout(ctx, 3*time.Second)
	defer cancelSave()
	saveErr := store.SaveAPISandboxOperation(saveCtx, op, now)
	return errors.Join(err, saveErr)
}

func apiSandboxTimeoutFailure(phase string) *api.APISandboxFailure {
	detail := "The sandbox release did not start in time. Check the release startup configuration and try a new sandbox."
	switch phase {
	case state.APISandboxPreparingDatabase:
		detail = "PostgreSQL or its bindings did not become ready in time. Check managed PostgreSQL and try a new sandbox."
	case state.APISandboxRunningMigrations:
		detail = "The release command did not finish in time. Check that it can initialize a fresh PostgreSQL database."
	case state.APISandboxVerifyingAPI:
		detail = "The sandbox API checks did not pass in time. Check the routes and create a new sandbox."
	}
	return &api.APISandboxFailure{Code: "setup_timeout", Detail: detail}
}

func failAPISandboxOperation(op *state.APISandboxOperation, code, detail string) {
	op.Phase = state.APISandboxFailed
	op.Failure = &api.APISandboxFailure{Code: code, Detail: detail}
}

func (s *server) advanceAPISandboxOperation(ctx context.Context, op *state.APISandboxOperation, now time.Time) error {
	app, err := s.store.AppByID(ctx, op.AppID)
	if err != nil {
		return err
	}
	if !state.APISandboxAcceptsRequests(app, now) {
		failAPISandboxOperation(op, "sandbox_closed", "The sandbox expired or was ended during setup.")
		return nil
	}
	deployment, err := s.store.LatestDeployment(ctx, app.ID)
	switch {
	case errors.Is(err, state.ErrNotFound) && op.DeploymentID == "":
		return s.stageAPISandboxRelease(ctx, app, op)
	case errors.Is(err, state.ErrNotFound):
		failAPISandboxOperation(op, "release_unavailable", "The sandbox deployment is unavailable. Create a new sandbox.")
		return nil
	case err != nil:
		return err
	case !state.IsAPISandboxDeployment(deployment) || (op.DeploymentID != "" && op.DeploymentID != deployment.ID):
		failAPISandboxOperation(op, "release_changed", "The sandbox deployment changed. Create a new sandbox from the selected release.")
		return nil
	}
	op.DeploymentID = deployment.ID
	if deployment.Status == state.DeployLive {
		// Commit the verification phase before making network calls.
		if op.Phase != state.APISandboxVerifyingAPI {
			op.Phase = state.APISandboxVerifyingAPI
			return nil
		}
		return s.verifyAPISandbox(ctx, app, op)
	}

	task, err := s.store.ReleaseAppTaskByDeployment(ctx, deployment.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	switch {
	case task.Status.Terminal() && task.Status != state.AppTaskSucceeded:
		failAPISandboxOperation(op, "migration_failed", "The release command failed. Check that it initializes a fresh database with MIGRATION_DATABASE_URL.")
	case deployment.Status.IsTerminal():
		failAPISandboxOperation(op, "release_start_failed", "The sandbox release did not start. Check its startup command and health endpoint.")
	case task.Status == state.AppTaskSucceeded:
		op.Phase = state.APISandboxStartingRelease
	default:
		op.Phase = state.APISandboxRunningMigrations
	}
	return nil
}

func (s *server) stageAPISandboxRelease(ctx context.Context, app state.App, op *state.APISandboxOperation) error {
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return err
	}
	if err := s.store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, api.DefaultEnvScope, "GREGALE_API_SANDBOX", "1"); err != nil {
		return err
	}
	if op.Scenario != nil {
		if err := s.store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, api.DefaultEnvScope, apisandbox.SeedEnvironmentKey, string(op.Scenario.Seed)); err != nil {
			return err
		}
	}
	postgres, err := s.ensureDevPostgres(ctx, acct, app, &api.DevPostgresRequest{})
	switch {
	case errors.Is(err, managedpostgres.ErrUsageLimitReached):
		failAPISandboxOperation(op, "database_usage_limit", managedpostgres.UsageLimitHint)
		return nil
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		failAPISandboxOperation(op, "database_quota", "The workspace has no available developer PostgreSQL quota. Free up capacity and create a new sandbox.")
		return nil
	case errors.Is(err, managedpostgres.ErrNotInPlan):
		failAPISandboxOperation(op, "database_plan", "The current plan does not support developer PostgreSQL.")
		return nil
	case err != nil:
		return err
	}
	if postgres.State == string(managedpostgres.StateFailed) || postgres.BindingState == string(managedpostgres.BindingStateFailed) {
		failAPISandboxOperation(op, "database_failed", "PostgreSQL or its credentials could not be provisioned. Check managed PostgreSQL and try a new sandbox.")
		return nil
	}
	if postgres.State != string(managedpostgres.StateReady) || postgres.BindingState != string(managedpostgres.BindingStateReady) {
		return nil
	}
	store := s.store.(state.APISandboxDeploymentStore)
	deployment, err := store.CreateAPISandboxDeployment(ctx, acct.ID, app.ID, app.Manifest.APISandboxSourceDeploymentID)
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrLayerArtifactRetired):
		failAPISandboxOperation(op, "release_unavailable", "The selected release or its retained artifact is unavailable. Choose a retained release.")
		return nil
	case err != nil:
		return err
	}
	op.Phase, op.DeploymentID = state.APISandboxRunningMigrations, deployment.ID
	// imaged runs the release command before boot. Its sweep also recovers
	// an accepted deployment if the wakeup or this worker is interrupted.
	payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": deployment.ID})
	return s.notif.Notify(ctx, db.NotifySnapshotBoot, string(payload))
}

func (s *server) verifyAPISandbox(ctx context.Context, app state.App, op *state.APISandboxOperation) error {
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return err
	}
	consumer, err := s.apiSandboxConsumer(ctx, app, api.APISandboxVerificationConsumerRef, "API sandbox readiness checks")
	if err != nil {
		return err
	}
	expires := time.Now().Add(time.Minute)
	if app.PreviewExpiresAt.Before(expires) {
		expires = *app.PreviewExpiresAt
	}
	key, token, problem := s.issueConsumerKey(ctx, acct, consumer, "verify-"+uuid.NewString(), []string{"read"}, &expires)
	if problem != nil {
		op.Failure = &api.APISandboxFailure{Code: "verification_credentials", Detail: "Gregale could not issue the temporary verification key. Check the workspace's consumer key limits."}
		return nil
	}
	defer func() {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if _, err := s.store.RevokeConsumerKey(revokeCtx, acct.ID, key.ID); err != nil {
			s.log.WarnContext(revokeCtx, "revoke API sandbox verification key", "key_id", key.ID, "err", err)
		}
	}()
	base := appURLForDomain(app.Slug, s.domain)
	receipt, failure, retry := apisandbox.Check(ctx, s.apiSandboxProbeClient, base, token, op.DeploymentID, op.SmokePath)
	if failure != nil {
		if !retry {
			op.Phase = state.APISandboxFailed
		}
		op.Failure = failure
		return nil
	}
	op.Phase, op.Failure = state.APISandboxLive, nil
	op.Verification = receipt
	return nil
}
