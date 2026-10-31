// adr: 20261027123814 — Data API contract tasks run without the app-task capability.
package apidserver

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// createDataAPIContractTask admits one fixed schema inspection. The route
// belongs to the Data API capability, so owner tooling works for an account
// the general app-task API is not released to.
func (s *server) createDataAPIContractTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.CreateDataAPIContractTaskRequest
	if err := decodeJSONSized(r, &request, api.AppTaskRequestMaxBytes); err != nil || request.Kind.Command() == nil {
		api.WriteProblem(w, api.ErrValidation(`kind must be one of "types", "validators" or "schema"`))
		return
	}
	resolved, problem := request.Kind.Request().Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	row, problem := s.admitAppTask(r, acct, app, resolved)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusAccepted, appTaskResponse(row))
}

// getDataAPIContractTask reads one contract inspection; any other app task
// is not found here.
func (s *server) getDataAPIContractTask(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireAppTaskAPI(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such Data API contract task")
		return
	}
	row, err := s.store.AppTaskByID(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) || err == nil && api.DataAPIContractTaskKindOf(row.Command, row.CommandShell) == "" {
		s.notFound(w, "no such Data API contract task")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load Data API contract task"))
		return
	}
	writeJSON(w, http.StatusOK, appTaskResponse(row))
}
