package apidserver

import (
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listOrgProjects(w http.ResponseWriter, r *http.Request, _ state.Account) {
	projects, err := projectaccess.ListProjects(r.Context(), s.store)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list organization projects"))
		return
	}
	// A key limited to projects sees only those (ADR-20261104085712).
	if key := projectaccess.Credential(r); key != nil && key.ProjectLimited {
		projects = slices.DeleteFunc(projects, func(p api.ProjectSummaryResponse) bool { return !slices.Contains(key.ProjectIDs, p.ID) })
	}
	writeJSON(w, http.StatusOK, projects)
}
