package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listProjectRollbacks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectRollbackHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback history is unavailable"))
		return
	}
	items, err := store.ListProjectRollbacks(r.Context(), project.AccountID, project.ID)
	if err != nil {
		s.log.Warn("read project rollback history", "project", project.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read project rollback history"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProjectRollbackHistory{
		Project: project.Slug, ProjectID: project.ID, Operations: items,
	})
}

func (s *server) activeProjectRollbackConflict(ctx context.Context, project state.Project) *api.Problem {
	store, ok := s.store.(state.ProjectRollbackHistoryStore)
	if !ok {
		return nil
	}
	id, err := store.ActiveProjectRollbackID(ctx, project.AccountID, project.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		s.log.Warn("read active project rollback", "project", project.ID, "err", err)
		return api.ErrCapacity("could not read the active project rollback")
	}
	problem := api.NewProblem(http.StatusConflict, api.CodeProjectRollbackConflict, "Project rollback already running",
		fmt.Sprintf("Project rollback %s is already running.", id)).
		WithHint(fmt.Sprintf("Inspect it with gregale project rollback %s --status %s", project.Slug, id))
	problem.ActiveOperationID = id
	return problem
}
