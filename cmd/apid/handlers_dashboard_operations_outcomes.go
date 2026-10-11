package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

func (s *server) renderAppWorkflowOutcomes(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowOutcomeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow outcomes unavailable"))
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	for _, key := range []string{"tenant_id", "workflow", "code"} {
		if values := query[key]; len(values) == 1 && values[0] == "" {
			query.Del(key)
		}
	}
	if !query.Has("scope") {
		query.Set("scope", api.DefaultEnvScope)
	}
	summaryCursor := ""
	if values, ok := query["summary_cursor"]; ok {
		if len(values) != 1 || values[0] == "" {
			writeOperationError(w, state.ErrInvalidArgument)
			return
		}
		summaryCursor = values[0]
	}
	query.Del("summary_cursor")
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = query.Encode()
	options, err := operationWorkflowOutcomeOptions(clone, true, app.ID, true)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if options.GroupBy == "" {
		options.GroupBy = "outcome"
	}
	if options.Limit == 0 {
		options.Limit = 20
	}
	result, err := store.ListAccountWorkflowOutcomes(r.Context(), acct.ID, options.OperationWorkflowOutcomeOptions)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	summaryOpts := options
	summaryOpts.Cursor = summaryCursor
	summary, err := store.SummarizeAccountWorkflowOutcomes(r.Context(), acct.ID, summaryOpts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	data := dashboard.WorkflowOutcomesData{AppSlug: app.Slug, Scope: options.Scope, TenantID: options.TenantID, Workflow: options.Workflow, Code: options.Code, GroupBy: options.GroupBy, Limit: options.Limit, EvaluatedAt: dashboardJobsTime(result.EvaluatedAt), Summary: summary, ListURL: dashboardCustomerOperationsURL(app.Slug), QueueURL: dashboardCustomerOperationsURL(app.Slug) + "/outcomes"}
	query.Set("group_by", options.GroupBy)
	copyQuery := func() url.Values {
		q := url.Values{}
		for key, values := range query {
			q[key] = append([]string(nil), values...)
		}
		return q
	}
	for _, g := range summary.Groups {
		link := copyQuery()
		link.Del("cursor")
		key := map[string]string{"outcome": "code", "workflow": "workflow", "customer": "tenant_id"}[options.GroupBy]
		link.Set(key, g.Value)
		data.Groups = append(data.Groups, dashboard.WorkflowOutcomeSummaryGroup{Group: g, URL: data.QueueURL + "?" + link.Encode()})
	}
	for _, e := range result.Items {
		history := url.Values{"scope": {options.Scope}, "tenant_id": {e.PlatformTenantID}, "subject_type": {e.Subject.Type}, "subject_id": {e.Subject.ID}, "workflow": {e.State.Workflow}, "workflow_instance_id": {e.State.InstanceID}}
		data.Items = append(data.Items, dashboard.WorkflowOutcomeItem{Entry: e, HistoryURL: data.ListURL + "?" + history.Encode() + "#workflows", OperationURL: data.ListURL + "/" + url.PathEscape(e.OperationID)})
	}
	if summary.NextCursor != "" {
		next := copyQuery()
		next.Set("summary_cursor", summary.NextCursor)
		data.SummaryNextURL = data.QueueURL + "?" + next.Encode()
	}
	if result.NextCursor != "" {
		next := copyQuery()
		next.Set("cursor", result.NextCursor)
		next.Set("limit", strconv.Itoa(options.Limit))
		if summaryCursor != "" {
			next.Set("summary_cursor", summaryCursor)
		}
		data.NextURL = data.QueueURL + "?" + next.Encode()
	}
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard outcomes: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " workflow outcomes", Body: "workflow_outcomes", Account: dashboardAccountView(acct, count), Data: data}
	if err = dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
