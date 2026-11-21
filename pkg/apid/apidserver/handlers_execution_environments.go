package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runenvironments"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"strings"
)

func executionEnvironmentBases(getenv func(string) string) map[api.ExecutionRuntime]string {
	out := map[api.ExecutionRuntime]string{}
	for _, runtime := range []api.ExecutionRuntime{api.ExecutionRuntimeNode22, api.ExecutionRuntimeNode24, api.ExecutionRuntimePython312, api.ExecutionRuntimePython313} {
		ref := strings.TrimSpace(getenv("FAAS_EXECUTION_ENVIRONMENT_" + strings.ToUpper(string(runtime)) + "_BASE_REF"))
		if runenv.ValidBaseRef(ref) {
			out[runtime] = ref
		}
	}
	return out
}
func (s *server) executionEnvironmentService() runenvironments.Service {
	st, _ := s.store.(state.ExecutionEnvironmentStore)
	return runenvironments.Service{Store: st, Bases: s.executionEnvironmentBases, Enabled: s.executionAPIEnabled && s.executionV2Enabled && s.executionEnvironmentsEnabled}
}
func (s *server) executionEnvironmentAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (runenvironments.Access, bool) {
	a, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return runenvironments.Access{}, false
	}
	return runenvironments.Access{Account: acct, Principal: a.principalID, Wide: a.broad}, true
}
func (s *server) createExecutionEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionEnvironmentAccess(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateExecutionEnvironmentRequest
	if err := decodeJSONSized(r, &req, api.ExecutionEnvironmentRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid dependency environment request"))
		return
	}
	out, err := s.executionEnvironmentService().Create(r.Context(), a, req)
	if err != nil {
		api.WriteProblem(w, runenvironments.Problem(err))
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *server) getExecutionEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionEnvironmentAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := s.executionEnvironmentService().Get(r.Context(), a, r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, runenvironments.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) listExecutionEnvironments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionEnvironmentAccess(w, r, acct)
	if !ok {
		return
	}
	out, err := s.executionEnvironmentService().List(r.Context(), a)
	if err != nil {
		api.WriteProblem(w, runenvironments.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out})
}
func (s *server) deleteExecutionEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionEnvironmentAccess(w, r, acct)
	if !ok {
		return
	}
	if err := s.executionEnvironmentService().Retire(r.Context(), a, r.PathValue("id")); err != nil {
		api.WriteProblem(w, runenvironments.Problem(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "retiring"})
}

func (s *server) executionEnvironmentEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	a, ok := s.executionEnvironmentAccess(w, r, acct)
	if !ok {
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			api.WriteProblem(w, api.ErrValidation("after must be a non-negative integer"))
			return
		}
	}
	out, err := s.executionEnvironmentService().Events(r.Context(), a, r.PathValue("id"), after)
	if err != nil {
		api.WriteProblem(w, runenvironments.Problem(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
