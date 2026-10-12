package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountWorkflowCoordinationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowCoordinationTimeline(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowCoordinationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowCoordinationTimeline(w, r, acct, tenant, "", false)
}
func (s *server) workflowCoordinationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowCoordinationTimelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow coordination timeline unavailable"))
		return
	}
	opts, err := workflowCoordinationTimelineOptions(r, app, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	out, err := store.WorkflowCoordinationTimeline(r.Context(), acct.ID, tenant, opts, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func workflowCoordinationTimelineOptions(r *http.Request, app string, operator bool) (api.OperationWorkflowCoordinationTimelineOptions, error) {
	out := api.OperationWorkflowCoordinationTimelineOptions{AppID: app}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return out, state.ErrInvalidArgument
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" {
			return out, state.ErrInvalidArgument
		}
		if k != "scope" && k != "subject_type" && k != "subject_id" && k != "workflow" && k != "instance_id" && k != "offset" && k != "limit" && (!operator || k != "tenant_id") && (operator || k != "app_id") {
			return out, state.ErrInvalidArgument
		}
	}
	if operator {
		out.TenantID = q.Get("tenant_id")
	} else {
		out.AppID = q.Get("app_id")
	}
	out.Scope = q.Get("scope")
	out.SubjectType = q.Get("subject_type")
	out.SubjectID = q.Get("subject_id")
	out.Workflow = q.Get("workflow")
	out.InstanceID = q.Get("instance_id")
	if q.Has("offset") {
		out.Offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil {
			return out, state.ErrInvalidArgument
		}
	}
	if q.Has("limit") {
		out.Limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || out.Limit == 0 {
			return out, state.ErrInvalidArgument
		}
	}
	return out, nil
}

func (s *server) accountWorkflowCase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowCase(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowCase(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowCase(w, r, acct, tenant, "", false)
}
func (s *server) workflowCase(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowCaseStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow case overview unavailable"))
		return
	}
	opts, err := workflowCoordinationTimelineOptions(r, app, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	out, err := store.WorkflowCase(r.Context(), acct.ID, tenant, opts, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
