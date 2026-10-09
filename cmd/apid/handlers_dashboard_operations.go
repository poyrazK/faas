// adr: 521
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppCustomerOperationsPath(rest string) (string, string, bool) {
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || !validSlug(parts[0]) || parts[1] != "customer-operations" {
		return "", "", false
	}
	id := ""
	if len(parts) == 3 {
		id = parts[2]
		if id == "" {
			return "", "", false
		}
	}
	return parts[0], id, true
}

func (s *server) renderAppCustomerOperations(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug, id string) {
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	if id == "outcomes" {
		s.renderAppWorkflowOutcomes(w, r, log, acct, app)
		return
	}
	if id == "attention" {
		s.renderAppWorkflowAttention(w, r, log, acct, app)
		return
	}
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	data := dashboard.CustomerOperationsData{AppSlug: slug, ListURL: dashboardCustomerOperationsURL(slug), Scope: api.DefaultEnvScope}
	if id == "" {
		if !populateDashboardOperationList(w, r, acct, app, store, &data) {
			return
		}
	} else if !populateDashboardOperationDetail(w, r, log, acct, app, id, store, &data) {
		return
	}
	populateDashboardOperationMilestones(r, log, acct, app, s.store, &data)
	count, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard operations: count apps", "account_id", acct.ID, "err", err)
	}
	page := dashboard.Page{Title: slug + " customer operations", Body: "customer_operations", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func dashboardCustomerOperationsURL(slug string) string {
	return "/dashboard/apps/" + url.PathEscape(slug) + "/customer-operations"
}

// Empty optional form controls are omitted before sharing the API's strict
// selector parser. Duplicate or unknown controls still fail validation.
func dashboardOperationListOptions(r *http.Request, appID string) (api.OperationListOptions, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	for _, key := range []string{"tenant_id", "name", "state", "subject_type", "subject_id", "workflow", "workflow_instance_id", "workflow_state_cursor"} {
		if values := query[key]; len(values) == 1 && values[0] == "" {
			query.Del(key)
		}
	}
	if values := query["stale_only"]; len(values) > 1 || len(values) == 1 && values[0] != "true" && values[0] != "false" {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	staleOnly := query.Get("stale_only") == "true"
	if staleOnly && (query.Get("subject_type") == "" || query.Get("subject_id") == "") {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	query.Del("stale_only")
	for _, key := range []string{"workflow", "workflow_instance_id", "workflow_state_cursor"} {
		if values := query[key]; len(values) > 1 || len(values) == 1 && len(values[0]) > api.OperationHistoryCursorMaxBytes {
			return api.OperationListOptions{}, state.ErrInvalidArgument
		}
	}
	if (query.Get("workflow") == "") != (query.Get("workflow_instance_id") == "") || query.Get("workflow_state_cursor") != "" && query.Get("workflow") == "" {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	if query.Get("workflow") != "" && (query.Get("subject_type") == "" || query.Get("subject_id") == "" ||
		api.ValidateOperationWorkflowName(query.Get("workflow")) != nil || api.ValidateOperationWorkflowInstanceID(query.Get("workflow_instance_id")) != nil) {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	if !query.Has("scope") {
		query.Set("scope", api.DefaultEnvScope)
	}
	if values := query["milestone_cursor"]; len(values) > 1 || len(values) == 1 && (values[0] == "" || len(values[0]) > api.OperationHistoryCursorMaxBytes) {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	query.Del("milestone_cursor")
	query.Del("workflow")
	query.Del("workflow_instance_id")
	query.Del("workflow_state_cursor")
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = query.Encode()
	opts, err := accountOperationHistoryOptions(clone, appID)
	if opts.Limit == 0 {
		opts.Limit = api.OperationHistoryPageDefault
	}
	return opts, err
}

func populateDashboardOperationList(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.OperationStore, data *dashboard.CustomerOperationsData) bool {
	opts, err := dashboardOperationListOptions(r, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return false
	}
	page, err := store.ListAccountOperations(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return false
	}
	data.SubjectType, data.SubjectID = opts.SubjectType, opts.SubjectID
	data.StaleOnly = r.URL.Query().Get("stale_only") == "true"
	data.Scope, data.TenantID, data.Name, data.State, data.Limit = opts.Scope, opts.TenantID, opts.Name, string(opts.State), opts.Limit
	for _, op := range page.Operations {
		item := projectDashboardOperation(op, app.Slug)
		item.SubjectURL = dashboardOperationSubjectURL(app.Slug, opts.Scope, opts.TenantID, op.Subject)
		data.Items = append(data.Items, item)
	}
	if page.NextCursor != "" {
		query := url.Values{"scope": {opts.Scope}, "limit": {strconv.Itoa(opts.Limit)}, "cursor": {page.NextCursor}}
		for key, value := range map[string]string{"subject_type": opts.SubjectType, "subject_id": opts.SubjectID, "tenant_id": opts.TenantID, "name": opts.Name, "state": string(opts.State)} {
			if value != "" {
				query.Set(key, value)
			}
		}
		if data.StaleOnly {
			query.Set("stale_only", "true")
		}
		data.NextURL = data.ListURL + "?" + query.Encode()
	}
	return true
}

func projectDashboardOperation(op api.OperationSummary, slug string) dashboard.CustomerOperationItem {
	label, class := dashboardOperationState(op.State)
	return dashboard.CustomerOperationItem{Subject: op.Subject, ID: op.ID, Name: op.Name, TenantID: op.PlatformTenantID,
		State: label, StateClass: class, DetailURL: dashboardCustomerOperationsURL(slug) + "/" + url.PathEscape(op.ID),
		Progress: op.Progress, DeliveryState: dashboardOperationDeliveryState(op.CompletionDelivery.State), DeliveryAttempts: op.CompletionDelivery.Attempts,
		CancellationRequested: op.CancellationRequested, CreatedAt: dashboardJobsTime(op.CreatedAt), UpdatedAt: dashboardJobsTime(op.UpdatedAt)}
}

func dashboardOperationState(value api.OperationState) (string, string) {
	switch value {
	case api.OperationAccepted:
		return "Accepted", "pending"
	case api.OperationRunning:
		return "Running", "pending"
	case api.OperationSucceeded:
		return "Succeeded", "good"
	case api.OperationFailed:
		return "Failed", "bad"
	case api.OperationCancelled:
		return "Cancelled", "neutral"
	case api.OperationRequiresReconciliation:
		return "Requires reconciliation", "bad"
	default:
		return "Unknown", "neutral"
	}
}

func dashboardOperationDeliveryState(value string) string {
	switch value {
	case "not_requested":
		return "Not requested"
	case "awaiting_outcome":
		return "Awaiting business outcome"
	case "pending":
		return "Pending"
	case "in_flight":
		return "Sending"
	case "succeeded":
		return "Delivered"
	case "failed":
		return "Retrying"
	case "dead":
		return "Dead letter"
	case "configuration_failed":
		return "Destination unavailable"
	case "delivery_expired":
		return "Delivery history expired"
	default:
		return "Unknown"
	}
}

func dashboardOperationDetailCursors(r *http.Request) (int64, int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, 0, state.ErrInvalidArgument
	}
	for key, values := range query {
		if (key != "event_after" && key != "execution_after" && key != "milestone_cursor") || len(values) != 1 || values[0] == "" {
			return 0, 0, state.ErrInvalidArgument
		}
	}
	if len(query.Get("milestone_cursor")) > api.OperationHistoryCursorMaxBytes {
		return 0, 0, state.ErrInvalidArgument
	}
	parse := func(key string) (int64, error) {
		if !query.Has(key) {
			return 0, nil
		}
		n, err := strconv.ParseInt(query.Get(key), 10, 64)
		if err != nil || n < 0 {
			return 0, state.ErrInvalidArgument
		}
		return n, nil
	}
	eventAfter, err := parse("event_after")
	if err != nil {
		return 0, 0, err
	}
	executionAfter, err := parse("execution_after")
	if err != nil || executionAfter > math.MaxInt32 {
		return 0, 0, state.ErrInvalidArgument
	}
	return eventAfter, int(executionAfter), nil
}

func populateDashboardOperationDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, app state.App, id string, store state.OperationStore, data *dashboard.CustomerOperationsData) bool {
	op, err := store.OperationByID(r.Context(), acct.ID, "", id)
	if err != nil {
		writeOperationError(w, err)
		return false
	}
	if op.AppID != app.ID {
		http.NotFound(w, r)
		return false
	}
	eventAfter, executionAfter, err := dashboardOperationDetailCursors(r)
	if err != nil {
		writeOperationError(w, err)
		return false
	}
	data.Scope = op.Scope
	data.ListURL += "?" + url.Values{"scope": {op.Scope}}.Encode()
	detail := projectDashboardOperationDetail(op, app.Slug)
	populateDashboardOperationDefinition(r, log, acct, op, store, &detail)
	populateDashboardOperationExecutions(r, log, acct, op, store, executionAfter, eventAfter, &detail)
	populateDashboardOperationEvents(r, log, acct, op, store, eventAfter, executionAfter, &detail)
	data.Detail = &detail
	return true
}

func projectDashboardOperationDetail(op state.Operation, slug string) dashboard.CustomerOperationDetail {
	summary := api.OperationSummary{Subject: op.Subject, ID: op.ID, Name: op.Name, PlatformTenantID: op.PlatformTenantID, State: op.State,
		Progress: op.Progress, CancellationRequested: op.CancellationRequested, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
		CompletionDelivery: api.OperationDeliverySummary{State: op.CompletionDelivery.State, Attempts: op.CompletionDelivery.Attempts}}
	base := "/v1/apps/" + url.PathEscape(slug) + "/operations/" + url.PathEscape(op.ID)
	detail := dashboard.CustomerOperationDetail{CustomerOperationItem: projectDashboardOperation(summary, slug),
		Generation: op.Generation, DefinitionRevision: op.DefinitionRevision, DeploymentID: op.DeploymentID,
		DeploymentURL: "/dashboard/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(op.DeploymentID),
		ReleaseID:     op.ReleaseID, FailureCode: op.FailureCode, ExpiresAt: dashboardJobsTime(op.ExpiresAt),
		ResultAvailable: len(op.Result) > 0, RecordURL: base, DeliveryID: op.CompletionDelivery.DeliveryID}
	detail.SubjectURL = dashboardOperationSubjectURL(slug, op.Scope, op.PlatformTenantID, op.Subject)
	if op.CompletionDelivery.NextAttemptAt != nil {
		detail.NextDeliveryAt = dashboardJobsTime(*op.CompletionDelivery.NextAttemptAt)
	}
	for _, artifact := range op.Artifacts {
		item := dashboard.CustomerOperationArtifact{Name: artifact.Name, SizeBytes: artifact.SizeBytes,
			DownloadURL: base + "/artifacts/" + url.PathEscape(artifact.ID)}
		if artifact.ExpiresAt != nil {
			item.ExpiresAt = dashboardJobsTime(*artifact.ExpiresAt)
		}
		detail.Artifacts = append(detail.Artifacts, item)
	}
	return detail
}

func populateDashboardOperationDefinition(r *http.Request, log *slog.Logger, acct state.Account, op state.Operation, store state.OperationStore, detail *dashboard.CustomerOperationDetail) {
	def, err := store.OperationDefinitionByID(r.Context(), acct.ID, op.DefinitionID)
	if err != nil || def.AppID != op.AppID || def.DeploymentID != op.DeploymentID || def.Revision != op.DefinitionRevision {
		detail.DefinitionError = "The pinned definition is unavailable. Reported progress remains visible below."
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			log.Warn("dashboard operations: read definition", "operation_id", op.ID, "err", err)
		}
		return
	}
	detail.Stages = def.Spec.ProgressStages
}

func populateDashboardOperationExecutions(r *http.Request, log *slog.Logger, acct state.Account, op state.Operation, store state.OperationStore, after int, eventAfter int64, detail *dashboard.CustomerOperationDetail) {
	page, err := store.OperationExecutions(r.Context(), acct.ID, op.ID, after, api.OperationHistoryPageDefault)
	if err != nil {
		log.Warn("dashboard operations: read executions", "operation_id", op.ID, "err", err)
		detail.ExecutionsError = "Execution history is temporarily unavailable. Refresh to try again."
		return
	}
	for _, execution := range page.Executions {
		item := dashboard.CustomerOperationExecution{Generation: execution.Generation, InvocationID: execution.InvocationID,
			URL: dashboardAsyncInvocationPath + url.PathEscape(execution.InvocationID), State: execution.State, Attempts: execution.Attempts,
			CreatedAt: dashboardJobsTime(execution.CreatedAt)}
		if execution.CompletedAt != nil {
			item.CompletedAt = dashboardJobsTime(*execution.CompletedAt)
		}
		detail.Executions = append(detail.Executions, item)
	}
	if page.NextGeneration > 0 {
		detail.NextExecutionsURL = detail.DetailURL + "?" + url.Values{"execution_after": {strconv.Itoa(page.NextGeneration)}, "event_after": {strconv.FormatInt(eventAfter, 10)}}.Encode() + "#executions"
	}
}

func populateDashboardOperationEvents(r *http.Request, log *slog.Logger, acct state.Account, op state.Operation, store state.OperationStore, after int64, executionAfter int, detail *dashboard.CustomerOperationDetail) {
	page, err := store.OperationEvents(r.Context(), acct.ID, "", op.ID, after, api.OperationEventsPageMax)
	if err != nil {
		log.Warn("dashboard operations: read events", "operation_id", op.ID, "err", err)
		detail.EventsError = "Progress history is temporarily unavailable. Refresh to try again."
		return
	}
	detail.EventsResync = page.ResyncRequired
	for _, event := range page.Events {
		item := projectDashboardOperationEvent(event)
		detail.Events = append(detail.Events, item)
	}
	if n := len(page.Events); n > 0 && page.Events[n-1].Sequence < page.LatestSequence && !page.ResyncRequired {
		detail.NextEventsURL = detail.DetailURL + "?" + url.Values{"event_after": {strconv.FormatInt(page.Events[n-1].Sequence, 10)}, "execution_after": {strconv.Itoa(executionAfter)}}.Encode() + "#timeline"
	}
}

// Render only recognized event metadata. Recovery evidence, artifact URIs,
// and future event payloads must never become an accidental HTML data dump.
func projectDashboardOperationEvent(event api.OperationEvent) dashboard.CustomerOperationEvent {
	labels := map[string]string{"accepted": "Accepted", "running": "Execution started", "progress": "Progress reported",
		"succeeded": "Business work succeeded", "failed": "Business work failed", "cancelled": "Cancelled",
		"cancellation_requested": "Cancellation requested", "reconciliation_required": "Reconciliation required",
		"recovery_requested": "Recovery requested", "artifact_attached": "Result artifact attached", "milestone": "Business milestone published"}
	label := labels[event.Type]
	if label == "" {
		label = "Unrecognized event"
	}
	item := dashboard.CustomerOperationEvent{Sequence: event.Sequence, Label: label, CreatedAt: dashboardJobsTime(event.CreatedAt),
		InvocationID: event.ExecutionID, Attempt: event.Attempt}
	if event.ExecutionID != "" {
		item.ExecutionURL = dashboardAsyncInvocationPath + url.PathEscape(event.ExecutionID)
	}
	if event.Type == "progress" {
		var progress api.OperationProgress
		if json.Unmarshal(event.Data, &progress) == nil {
			item.Progress = &progress
		}
	}
	return item
}

func dashboardOperationSubjectURL(slug, scope, tenant string, subject *api.OperationSubject) string {
	if subject == nil {
		return ""
	}
	query := url.Values{"scope": {scope}, "subject_type": {subject.Type}, "subject_id": {subject.ID}}
	if tenant != "" {
		query.Set("tenant_id", tenant)
	}
	return dashboardCustomerOperationsURL(slug) + "?" + query.Encode()
}
