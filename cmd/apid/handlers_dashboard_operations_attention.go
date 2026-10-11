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

func (s *server) renderAppWorkflowAttention(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowAttentionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow attention unavailable"))
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	for _, key := range []string{"tenant_id", "workflow", "target_operation", "reason", "blocker_code", "dependency_status", "required_outcome_code", "owner", "priority", "sort"} {
		if values := query[key]; len(values) == 1 && values[0] == "" {
			query.Del(key)
		}
	}
	if !query.Has("scope") {
		query.Set("scope", api.DefaultEnvScope)
	}
	groupBy := "workflow"
	if values, ok := query["group_by"]; ok {
		if len(values) != 1 || values[0] == "" {
			writeOperationError(w, state.ErrInvalidArgument)
			return
		}
		groupBy = values[0]
	}
	query.Del("group_by")
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
	opts, err := operationWorkflowAttentionOptions(clone, true, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	result, err := store.ListAccountWorkflowAttention(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if opts.Limit == 0 {
		opts.Limit = api.OperationHistoryPageDefault
	}
	data := dashboard.WorkflowAttentionData{Priority: opts.Priority, Sort: opts.Sort, Owner: opts.Owner, Unassigned: opts.Unassigned, AppSlug: app.Slug, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, TargetOperation: opts.TargetOperation, Reason: opts.Reason, Limit: opts.Limit, EvaluatedAt: dashboardJobsTime(result.EvaluatedAt), ListURL: dashboardCustomerOperationsURL(app.Slug), QueueURL: dashboardCustomerOperationsURL(app.Slug) + "/attention"}
	summaryStore, ok := s.store.(state.OperationWorkflowAttentionSummaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow summaries unavailable"))
		return
	}
	summaryOpts := opts
	summaryOpts.Cursor = summaryCursor
	summary, err := summaryStore.SummarizeAccountWorkflowAttention(r.Context(), acct.ID, api.OperationWorkflowAttentionSummaryOptions{OperationWorkflowAttentionOptions: summaryOpts, GroupBy: groupBy})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	data.Summary = summary
	data.GroupBy = groupBy
	data.BlockerCode = opts.BlockerCode
	data.DependencyStatus, data.RequiredOutcomeCode = opts.DependencyStatus, opts.RequiredOutcomeCode
	for _, group := range summary.Groups {
		link := url.Values{}
		for key, values := range query {
			link[key] = append([]string(nil), values...)
		}
		link.Del("cursor")
		link.Set("group_by", groupBy)
		selector := map[string]string{"owner": "owner", "workflow": "workflow", "customer": "tenant_id", "blocker_code": "blocker_code", "target_operation": "target_operation", "dependency_status": "dependency_status", "required_outcome_code": "required_outcome_code"}[groupBy]
		if groupBy == "owner" {
			link.Del("owner")
			link.Del("unassigned")
			if group.Value == "" {
				link.Set("unassigned", "true")
			} else {
				link.Set("owner", group.Value)
			}
		} else {
			link.Set(selector, group.Value)
		}
		data.SummaryGroups = append(data.SummaryGroups, dashboard.WorkflowAttentionSummaryGroup{Group: group, URL: data.QueueURL + "?" + link.Encode()})
	}
	query.Set("group_by", groupBy)
	if summary.NextCursor != "" {
		link := url.Values{}
		for key, values := range query {
			link[key] = append([]string(nil), values...)
		}
		link.Set("summary_cursor", summary.NextCursor)
		data.SummaryNextURL = data.QueueURL + "?" + link.Encode()
	}
	if summaryCursor != "" {
		query.Set("summary_cursor", summaryCursor)
	}
	for _, entry := range result.Items {
		history := url.Values{"scope": {opts.Scope}, "tenant_id": {entry.PlatformTenantID}, "subject_type": {entry.Subject.Type}, "subject_id": {entry.Subject.ID}, "workflow": {entry.State.Workflow}, "workflow_instance_id": {entry.State.InstanceID}}
		item := dashboard.WorkflowAttentionItem{Entry: entry, HistoryURL: data.ListURL + "?" + history.Encode() + "#workflows", OperationURL: data.ListURL + "/" + url.PathEscape(entry.OperationID), UpdatedAt: dashboardJobsTime(entry.State.UpdatedAt)}
		for _, related := range entry.DependencyAttention {
			q := url.Values{"scope": {opts.Scope}, "tenant_id": {entry.PlatformTenantID}, "subject_type": {related.Dependency.SubjectType}, "subject_id": {related.Dependency.SubjectID}, "workflow": {related.Dependency.Workflow}, "workflow_instance_id": {related.Dependency.InstanceID}}
			item.Dependencies = append(item.Dependencies, dashboard.CustomerOperationWorkflowRelation{OperationWorkflowRelatedInstance: related, URL: data.ListURL + "?" + q.Encode() + "#workflows"})
		}
		data.Items = append(data.Items, item)
	}
	if result.NextCursor != "" {
		query.Set("cursor", result.NextCursor)
		query.Set("limit", strconv.Itoa(opts.Limit))
		data.NextURL = data.QueueURL + "?" + query.Encode()
	}
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard workflow attention: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " workflow attention", Body: "workflow_attention", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
