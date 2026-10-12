package apidserver

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
	"log/slog"
	"net/http"
	"net/url"
)

func readinessQueueDashboardOptions(r *http.Request, app string) (api.OperationWorkflowReadinessQueueOptions, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationWorkflowReadinessQueueOptions{}, state.ErrInvalidArgument
	}
	for _, key := range []string{"tenant_id", "workflow", "target_operation", "owner", "requirement"} {
		if v := q[key]; len(v) == 1 && v[0] == "" {
			q.Del(key)
		}
	}
	if !q.Has("scope") {
		q.Set("scope", api.DefaultEnvScope)
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = q.Encode()
	return operationWorkflowReadinessQueueOptions(clone, true, app)
}
func (s *server) renderAppWorkflowReadinessQueue(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationWorkflowReadinessQueueStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow readiness queue unavailable"))
		return
	}
	opts, err := readinessQueueDashboardOptions(r, app.ID)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	result, err := store.ListAccountWorkflowReadinessQueue(r.Context(), acct.ID, opts)
	if err != nil {
		operation.WriteOperationError(w, err)
		return
	}
	if opts.Limit == 0 {
		opts.Limit = api.OperationHistoryPageDefault
	}
	data := readinessQueueDashboardData(app.Slug, opts, result)
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("readiness queue: count apps", "err", err)
	}
	page := dashboard.Page{Title: app.Slug + " transition readiness", Body: "workflow_readiness_queue", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
func readinessQueueDashboardData(slug string, opts api.OperationWorkflowReadinessQueueOptions, result api.OperationWorkflowReadinessQueueResponse) dashboard.WorkflowReadinessQueueData {
	base := dashboardCustomerOperationsURL(slug)
	snapshotJSON, _ := json.Marshal(result.Snapshot)
	data := dashboard.WorkflowReadinessQueueData{Options: opts, Result: result, ListURL: base, QueueURL: base + "/readiness", SubscriptionsURL: "/v1/apps/" + url.PathEscape(slug) + "/workflow-readiness/subscriptions", CaseURL: "/v1/apps/" + url.PathEscape(slug) + "/workflow-readiness/case", TimelineURL: "/v1/apps/" + url.PathEscape(slug) + "/workflow-readiness/timeline", FollowUpsURL: "/v1/apps/" + url.PathEscape(slug) + "/workflow-readiness/follow-ups", SnapshotJSON: string(snapshotJSON), CompareURL: "/v1/apps/" + url.PathEscape(slug) + "/workflow-readiness/compare"}
	for _, row := range result.Items {
		q := url.Values{"scope": {opts.Scope}, "tenant_id": {row.PlatformTenantID}, "subject_type": {row.Subject.Type}, "subject_id": {row.Subject.ID}, "workflow": {row.Workflow}, "workflow_instance_id": {row.InstanceID}}
		data.Items = append(data.Items, dashboard.WorkflowReadinessQueueItem{Entry: row, HistoryURL: base + "?" + q.Encode() + "#workflows", OperationURL: base + "/" + url.PathEscape(row.OperationID) + "?scope=" + url.QueryEscape(opts.Scope)})
	}
	return data
}
