package apidserver

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) explainAppServiceBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid service explanation query encoding"))
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "deployment_id" && key != "scope" && key != "method" && key != "path") {
			api.WriteProblem(w, api.ErrValidation("service explanation requires unique supported query parameters"))
			return
		}
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), bindingInventoryReadTimeout)
	defer cancel()
	explanation, problem := bindings.ExplainServiceBinding(ctx, s.store, app, r.PathValue("service"), api.ExplainServiceBindingOptions{
		DeploymentID: query.Get("deployment_id"), Scope: query.Get("scope"), Method: query.Get("method"), Path: query.Get("path"),
	})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, explanation)
}
