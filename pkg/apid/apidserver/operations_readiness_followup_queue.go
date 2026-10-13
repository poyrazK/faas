package apidserver

import (
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountWorkflowReadinessFollowUps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.workflowReadinessFollowUps(w, r, acct, "", app.ID, true)
}
func (s *server) customerWorkflowReadinessFollowUps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	s.workflowReadinessFollowUps(w, r, acct, tenant, "", false)
}
func (s *server) workflowReadinessFollowUps(w http.ResponseWriter, r *http.Request, acct state.Account, tenant, app string, operator bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowReadinessFollowUpStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness follow-ups unavailable"))
		return
	}
	opts, err := workflowReadinessFollowUpOptions(r, app, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	out, err := store.ListWorkflowReadinessFollowUps(r.Context(), acct.ID, tenant, opts, operator)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func workflowReadinessFollowUpOptions(r *http.Request, app string, operator bool) (api.OperationWorkflowReadinessFollowUpOptions, error) {
	out := api.OperationWorkflowReadinessFollowUpOptions{AppID: app}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return out, state.ErrInvalidArgument
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" {
			return out, state.ErrInvalidArgument
		}
		if k != "view" && k != "action_kind" && k != "action_trigger" && k != "impact_priority" && k != "impact_kind" && k != "impact_code" && k != "sort" && k != "reminder_only" && k != "scope" && k != "owner" && k != "workflow" && k != "due" && k != "offset" && k != "limit" && (!operator || k != "tenant_id") && (operator || k != "app_id") {
			return out, state.ErrInvalidArgument
		}
	}
	if operator {
		out.TenantID = q.Get("tenant_id")
	} else {
		out.AppID = q.Get("app_id")
	}
	if q.Has("reminder_only") {
		if q.Get("reminder_only") != "true" && q.Get("reminder_only") != "false" {
			return out, state.ErrInvalidArgument
		}
		out.ReminderOnly = q.Get("reminder_only") == "true"
	}
	out.View = q.Get("view")
	out.ActionKind = q.Get("action_kind")
	out.ActionTrigger = q.Get("action_trigger")
	out.Scope = q.Get("scope")
	out.Owner = q.Get("owner")
	out.Workflow = q.Get("workflow")
	out.Due = q.Get("due")
	out.ImpactPriority = q.Get("impact_priority")
	out.ImpactKind = q.Get("impact_kind")
	out.ImpactCode = q.Get("impact_code")
	out.Sort = q.Get("sort")
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
