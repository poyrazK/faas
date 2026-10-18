package apidserver

import (
	"net/http"

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
	writeJSON(w, http.StatusOK, projects)
}
