package apidserver

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runcontrols"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func runControlsAccess(r *http.Request, acct state.Account) (runcontrols.Access, *api.Problem) {
	a, p := executionAccessForRequest(r)
	_, key, _ := authmw.AccountFromContext(r)
	wide := a.broad
	if key != nil && (key.AppID != "" || key.ProjectLimited || key.PlatformTenantID != "" || key.HostingSiteID != "") {
		wide = false
	}
	return runcontrols.Access{AccountID: acct.ID, PrincipalID: a.principalID, Wide: wide}, p
}
func (s *server) runControlsService() runcontrols.Service {
	store, _ := s.store.(state.RunControlStore)
	return runcontrols.Service{Store: store, Enabled: s.executionAPIEnabled && s.executionV2Enabled}
}
func (s *server) runBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := runControlsAccess(r, acct)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	service := s.runControlsService()
	kind, ref := r.PathValue("kind"), r.PathValue("ref")
	var out api.RunBudgetPolicy
	var err error
	if r.Method == http.MethodPut {
		req, decodeErr := decodeRunBudgetBody(w, r)
		if decodeErr != nil {
			api.WriteProblem(w, api.ErrValidation("invalid bounded Run budget"))
			return
		}
		out, err = service.PutBudget(r.Context(), a, kind, ref, req)
	} else {
		out, err = service.GetBudget(r.Context(), a, kind, ref)
	}
	if err != nil {
		api.WriteProblem(w, runcontrols.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) runBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := runControlsAccess(r, acct)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	service := s.runControlsService()
	kind, id := r.PathValue("kind"), r.PathValue("id")
	var out api.RunAttributionBinding
	var err error
	switch r.Method {
	case http.MethodPut:
		var req api.SetRunAttributionBindingRequest
		if decodeExecutionTaskBody(w, r, &req, api.RunControlBodyMaxBytes) != nil {
			api.WriteProblem(w, api.ErrValidation("invalid bounded Run attribution binding"))
			return
		}
		out, err = service.PutBinding(r.Context(), a, kind, id, req)
	case http.MethodDelete:
		revision, e := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
		if e != nil || revision < 1 {
			api.WriteProblem(w, api.ErrValidation("expected_revision must be a positive integer"))
			return
		}
		err = service.DeleteBinding(r.Context(), a, kind, id, revision)
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	default:
		out, err = service.GetBinding(r.Context(), a, kind, id)
	}
	if err != nil {
		api.WriteProblem(w, runcontrols.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) runHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, p := runControlsAccess(r, acct)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	f, err := runcontrols.HistoryFilter(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, runcontrols.Problem(err))
		return
	}
	out, err := s.runControlsService().History(r.Context(), runcontrols.Access{AccountID: acct.ID, PrincipalID: a.PrincipalID, Wide: a.Wide}, f)
	if err != nil {
		api.WriteProblem(w, runcontrols.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func decodeRunBudgetBody(w http.ResponseWriter, r *http.Request) (api.SetRunBudgetRequest, error) {
	var req api.SetRunBudgetRequest
	err := decodeExecutionTaskBody(w, r, &req, api.RunControlBodyMaxBytes)
	return req, err
}
