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
	fresh, failure := prepareApplicationStandardDeployment(parent, s.store, s.log, app, "project-apply")
	if failure != nil {
		return fresh, failure.Detail
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
