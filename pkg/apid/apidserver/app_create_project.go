package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) projectForAppCreate(ctx context.Context, accountID, orgID, projectID string) (state.Project, *api.Problem) {
	if _, err := uuid.Parse(projectID); err != nil {
		return state.Project{}, api.ErrValidation("invalid project_id")
	}
	notFound := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Project not found", "Choose a project in this workspace.")
	project, err := s.store.ProjectByID(ctx, projectID)
	if errors.Is(err, state.ErrNotFound) {
		return state.Project{}, notFound
	}
	if err != nil {
		return state.Project{}, api.ErrCapacity("could not read project")
	}
	if orgID != "" {
		if project.OrgID != orgID {
			return state.Project{}, notFound
		}
	} else {
		if project.AccountID != accountID {
			return state.Project{}, notFound
		}
		if project.OrgID != "" {
			org, err := s.store.OrgByID(ctx, project.OrgID)
			if errors.Is(err, state.ErrNotFound) {
				return state.Project{}, notFound
			}
			if err != nil {
				return state.Project{}, api.ErrCapacity("could not verify the project workspace")
			}
			if !org.Personal || org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != accountID {
				return state.Project{}, notFound
			}
		}
	}
	return project, nil
}
