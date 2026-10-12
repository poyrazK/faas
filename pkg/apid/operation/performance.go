package operation

import (
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func OperationWorkflowPerformanceOptions(r *http.Request, operator bool, appID string) (api.OperationWorkflowPerformanceOptions, error) {
	opts := api.OperationWorkflowPerformanceOptions{AppID: appID}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "scope", "workflow":
		case "baseline_token":
			opts.BaselineToken = values[0]
		case "tenant_id":
			if !operator {
				return opts, state.ErrInvalidArgument
			}
			opts.TenantID = values[0]
		case "app_id":
			if operator {
				return opts, state.ErrInvalidArgument
			}
			opts.AppID = values[0]
		default:
			return opts, state.ErrInvalidArgument
		}
	}
	opts.Scope, opts.Workflow = q.Get("scope"), q.Get("workflow")
	return opts, nil
}
func (s *Service) SummarizeAccountWorkflowPerformance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowPerformanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := OperationWorkflowPerformanceOptions(r, true, app.ID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	out, err := store.SummarizeAccountWorkflowPerformance(r.Context(), acct.ID, opts)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (s *Service) SummarizePlatformTenantSelfWorkflowPerformance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowPerformanceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow performance unavailable"))
		return
	}
	opts, err := OperationWorkflowPerformanceOptions(r, false, "")
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	out, err := store.SummarizePlatformTenantWorkflowPerformance(r.Context(), acct.ID, tenant, opts)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
