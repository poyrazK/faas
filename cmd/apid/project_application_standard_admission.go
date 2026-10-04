package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareProjectApplicationStandard(parent context.Context, app state.App) (state.App, string) {
	if app.OrgID == "" {
		return app, ""
	}
	reader, ok := s.store.(state.ApplicationStandardEnrollmentStore)
	if !ok {
		return app, "Application standards admission is unavailable; retry the deployment."
	}
	ctx, cancel := context.WithTimeout(parent, api.ApplicationStandardWorkerPassTimeout)
	defer cancel()
	e, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err == nil && (!sameProjectStandardID(app.OrgID, e.OrgID) || !sameProjectStandardID(app.ProjectID, e.ProjectID)) {
		return app, "Application ownership changed during apply; review the project and reapply."
	}
	if err == nil && e.State == "pending" {
		s.materializeProjectApplicationStandard(ctx, e)
		e, err = reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	}
	if err != nil {
		return app, "Application standards admission is unavailable; retry the deployment."
	}
	if e.State == "blocked" {
		return app, "Application standards blocked this build; review the application's effective standard and exceptions."
	}
	fresh, err := s.store.AppByID(ctx, app.ID)
	if err == nil && !sameProjectStandardOwner(app, fresh) {
		return app, "Application ownership changed during apply; review the project and reapply."
	}
	if err != nil || !state.ApplicationStandardEnrollmentPermitsRuntime(fresh, e) {
		return app, "Application standards are still being installed; retry the deployment when installation completes."
	}
	return fresh, ""
}

func sameProjectStandardOwner(before, after state.App) bool {
	return after.Status != state.AppDeleted && sameProjectStandardID(before.ID, after.ID) && sameProjectStandardID(before.AccountID, after.AccountID) && sameProjectStandardID(before.OrgID, after.OrgID) && sameProjectStandardID(before.ProjectID, after.ProjectID)
}

func sameProjectStandardID(first, second string) bool {
	if first == "" || second == "" {
		return first == second
	}
	a, err := uuid.Parse(first)
	b, otherErr := uuid.Parse(second)
	return err == nil && otherErr == nil && a != uuid.Nil && a == b
}

func (s *server) writeProjectApplyLoadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "project_apply_stale", "Project changed during apply", "Reload the project before applying it again."))
		return
	}
	writeCustomerInternalProblem(w, r, s.log, "load apps after applying project",
		"Gregale could not finish loading the project after applying the changes.",
		"Reload the project before retrying the operation.", err)
}

func (s *server) materializeProjectApplicationStandard(ctx context.Context, e state.ApplicationStandardEnrollment) {
	worker, ok := s.store.(state.ApplicationStandardImmediateMaterializationStore)
	if !ok {
		return
	}
	claim, err := worker.ClaimApplicationStandardEnrollmentForApp(ctx, state.ApplicationStandardEnrollmentClaimRequest{OrgID: e.OrgID, AppID: e.AppID, DesiredRevision: e.DesiredRevision, Owner: "project-apply-" + uuid.NewString()})
	if err != nil {
		s.applicationStandardWorkerError(ctx, "project_enrollment_claim", e.AppID, err)
		return
	}
	_, err = worker.MaterializeApplicationStandardEnrollment(ctx, claim)
	if err == nil || errors.Is(err, state.ErrApplicationStandardOperationInProgress) {
		return
	}
	s.applicationStandardWorkerError(ctx, "project_enrollment", e.AppID, err)
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardWorkerReleaseTimeout)
	defer cancel()
	_ = worker.ReleaseApplicationStandardEnrollmentWorker(release, claim)
}
