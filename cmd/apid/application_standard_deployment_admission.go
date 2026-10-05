// adr: 595. apid installs captured standards before source deployment enqueue.
package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type applicationStandardDeploymentStore interface {
	AppByID(context.Context, string) (state.App, error)
}

type applicationStandardDeploymentFailure struct {
	Detail    string
	Retryable bool
}

func prepareApplicationStandardDeployment(parent context.Context, store applicationStandardDeploymentStore, log *slog.Logger, app state.App, ownerPrefix string) (state.App, *applicationStandardDeploymentFailure) {
	if app.OrgID == "" {
		return app, nil
	}
	unavailable := &applicationStandardDeploymentFailure{Detail: "Application standards admission is unavailable; retry the deployment.", Retryable: true}
	changed := &applicationStandardDeploymentFailure{Detail: "Application ownership changed during deployment admission; reload the application before retrying."}
	reader, ok := store.(state.ApplicationStandardEnrollmentStore)
	if !ok {
		return app, unavailable
	}
	ctx, cancel := context.WithTimeout(parent, api.ApplicationStandardWorkerPassTimeout)
	defer cancel()
	e, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err == nil && (!sameProjectStandardID(app.OrgID, e.OrgID) || !sameProjectStandardID(app.ProjectID, e.ProjectID)) {
		return app, changed
	}
	if err == nil && e.State == "pending" {
		materializeApplicationStandardDeployment(ctx, store, log, e, ownerPrefix)
		e, err = reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	}
	if err != nil {
		return app, unavailable
	}
	if e.State == "blocked" {
		return app, &applicationStandardDeploymentFailure{Detail: "Application standards blocked this build; review the application's effective standard and exceptions."}
	}
	fresh, err := store.AppByID(ctx, app.ID)
	if err == nil && !sameProjectStandardOwner(app, fresh) {
		return app, changed
	}
	if err != nil || !state.ApplicationStandardEnrollmentPermitsRuntime(fresh, e) {
		return app, &applicationStandardDeploymentFailure{Detail: "Application standards are still being installed; retry the deployment when installation completes.", Retryable: true}
	}
	return fresh, nil
}

func materializeApplicationStandardDeployment(ctx context.Context, store applicationStandardDeploymentStore, log *slog.Logger, e state.ApplicationStandardEnrollment, ownerPrefix string) {
	worker, ok := store.(state.ApplicationStandardImmediateMaterializationStore)
	if !ok {
		return
	}
	claim, err := worker.ClaimApplicationStandardEnrollmentForApp(ctx, state.ApplicationStandardEnrollmentClaimRequest{OrgID: e.OrgID, AppID: e.AppID, DesiredRevision: e.DesiredRevision, Owner: ownerPrefix + "-" + uuid.NewString()})
	if err != nil {
		logApplicationStandardWorkerError(ctx, log, "deployment_enrollment_claim", e.AppID, err)
		return
	}
	_, err = worker.MaterializeApplicationStandardEnrollment(ctx, claim)
	if err == nil || errors.Is(err, state.ErrApplicationStandardOperationInProgress) {
		return
	}
	logApplicationStandardWorkerError(ctx, log, "deployment_enrollment", e.AppID, err)
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardWorkerReleaseTimeout)
	defer cancel()
	_ = worker.ReleaseApplicationStandardEnrollmentWorker(release, claim)
}
