package apidserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardMCPAppPageSize = 25

func dashboardMCPOffset(values url.Values, key string) (int, error) {
	v, ok := values[key]
	if !ok {
		return 0, nil
	}
	if len(v) != 1 || v[0] == "" {
		return 0, state.ErrInvalidArgument
	}
	n, err := strconv.Atoi(v[0])
	if err != nil || n < 0 || n > api.MCPConnectionHealthMaxOffset {
		return 0, state.ErrInvalidArgument
	}
	return n, nil
}
func dashboardMCPURL(query url.Values, key, value string) string {
	copy := url.Values{}
	for k, v := range query {
		copy[k] = append([]string(nil), v...)
	}
	copy.Set(key, value)
	return "/dashboard/mcp?" + copy.Encode()
}
func dashboardMCPTime(at *time.Time) string {
	if at == nil || at.IsZero() {
		return "Not observed"
	}
	return at.UTC().Format(time.RFC3339)
}
func dashboardMCPAdmission(e api.MCPAdmissionEvidence) dashboard.MCPAdmissionView {
	return dashboard.MCPAdmissionView{ID: e.ID, DeploymentID: e.DeploymentID, Outcome: e.Outcome, Reason: e.Reason, CapturedAt: e.CapturedAt.UTC().Format(time.RFC3339), TrafficPercent: e.TrafficPercent, URL: dashboardMCPURL(url.Values{}, "evidence", e.ID)}
}

func (s *server) renderMCPOperations(w http.ResponseWriter, r *http.Request, log *slog.Logger, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid MCP dashboard filters"))
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "app" && key != "offset" && key != "health_offset" && key != "evidence" && key != "retirement_offset" && key != "notice" && key != "scope" && key != "incident_before" && key != "notification_after" && key != "attention_after") {
			api.WriteProblem(w, api.ErrValidation("Invalid MCP dashboard filters"))
			return
		}
	}
	if _, _, err := api.ParseMCPReadinessIncidentCursor(query.Get("incident_before")); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident timeline cursor"))
		return
	}
	if cursor := query.Get("notification_after"); cursor != "" && uuid.Validate(cursor) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid notification queue cursor"))
		return
	}
	if cursor := query.Get("attention_after"); cursor != "" && uuid.Validate(cursor) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid incident attention cursor"))
		return
	}
	scope := api.MCPConnectionScope(query.Get("scope"))
	if api.ValidateScope(scope) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid readiness environment"))
		return
	}
	offset, err := dashboardMCPOffset(query, "offset")
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid application offset"))
		return
	}
	healthOffset, err := dashboardMCPOffset(query, "health_offset")
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid connection-health offset"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.MCPPolicyConvergenceReadTimeout)
	defer cancel()
	data := dashboard.MCPOperationsData{ReadinessAttentionAfter: query.Get("attention_after"), ReadinessNotificationsAfter: query.Get("notification_after"), ReadinessIncidentsBefore: query.Get("incident_before"), ReadinessScope: scope, AppFilter: query.Get("app"), StatusMessage: dashboardMCPOAuthNotice(query.Get("notice"))}
	appID := ""
	var apps []state.App
	if data.AppFilter != "" {
		app, e := s.store.AppBySlug(ctx, data.AppFilter)
		if e != nil || app.AccountID != account.ID || !accountViewAppVisible(ctx, s.store, log, account, app) {
			http.NotFound(w, r)
			return
		}
		apps = []state.App{app}
		appID = app.ID
	} else {
		apps, err = s.store.ListApps(ctx, account.ID)
		if err == nil {
			apps, _, err = s.visibleAccountApps(r, account, apps)
		}
		if err != nil {
			data.Error = "Application inventory is unavailable. Try again shortly."
		}
	}
	// Read configuration before performance queries can exhaust the shared deadline.
	s.dashboardMCPAlerts(w, r, ctx, account, appID, data.AppFilter, &data)
	s.dashboardMCPReadiness(ctx, account.ID, appID, &data)
	if appID != "" {
		view := dashboard.MCPPerformanceView{Coverage: "Unavailable", Error: "MCP performance telemetry is unavailable."}
		inventory, inventoryErr := s.store.ListInstancesForApp(ctx, appID)
		var executing []string
		for _, instance := range inventory {
			switch state.State(instance.State) {
			case state.StateRunning, state.StateDraining, state.StateWaking, state.StateColdBooting:
				executing = append(executing, instance.ID)
			}
		}
		if s.promqlClient != nil && inventoryErr == nil {
			now := time.Now().UTC()
			coverage, coverageErr := mcpprotocol.FetchCoverage(ctx, s.promqlClient, appID, now, executing)
			snapshot, err := mcpprotocol.Fetch(ctx, s.promqlClient, appID, now)
			if err == nil {
				view = dashboard.MCPPerformance(snapshot, now)
				if coverageErr == nil && coverage.State == "Reporting" && now.Sub(snapshot.LastEvent) > 5*time.Minute {
					view.Freshness = "Telemetry reporting; no recent RPC completions"
				}
			}
			view.Coverage = "Unavailable"
			if coverageErr == nil {
				view.Coverage = fmt.Sprintf("%s · %d/%d instances reporting · %d starting · %d stale · %d deployments", coverage.State, coverage.Reporting, coverage.Expected, coverage.Starting, coverage.Stale, coverage.Deployments)
			}
		}
		data.Performance = &view
	}
	// A direct receipt read uses the same account ownership check as the API.
	if id := query.Get("evidence"); id != "" {
		reader, ok := s.store.(state.MCPAdmissionReader)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("MCP admission evidence unavailable"))
			return
		}
		receipt, e := reader.GetMCPAdmissionReceipt(ctx, account.ID, id)
		if e != nil {
			if errors.Is(e, state.ErrNotFound) {
				http.NotFound(w, r)
			} else {
				api.WriteProblem(w, api.ErrCapacity("MCP admission evidence unavailable"))
			}
			return
		}
		deployment, e := s.store.DeploymentByID(ctx, receipt.DeploymentID)
		if e != nil || (appID != "" && deployment.AppID != appID) {
			http.NotFound(w, r)
			return
		}
		app, e := s.store.AppByID(ctx, deployment.AppID)
		if e != nil || app.AccountID != account.ID || !accountViewAppVisible(ctx, s.store, log, account, app) {
			http.NotFound(w, r)
			return
		}
		evidence := dashboardMCPAdmission(receipt)
		data.Evidence = &evidence
	}
	if source, ok := s.store.(state.MCPConnectionHealthStore); ok {
		snapshot, e := source.MCPConnectionHealth(ctx, account.ID, appID, healthOffset, time.Now().UTC())
		if e != nil {
			data.HealthError = "Connection health is unavailable. No healthy state can be inferred."
		} else {
			data.ObservedAt = dashboardMCPTime(&snapshot.ObservedAt)
			data.HealthComplete = snapshot.Complete
			data.TotalIssues = snapshot.TotalIssues
			data.Reauthorization = snapshot.Counts["reauthorization_required"]
			data.RenewalFailed = snapshot.Counts["renewal_failed"]
			data.RetirementExpiring = snapshot.Counts["retirement_expiring"]
			data.IssuanceUnknown = snapshot.Counts["issuance_unknown"]
			for _, issue := range snapshot.Items {
				row := dashboard.MCPConnectionIssueView{App: issue.AppSlug, Role: issue.Role, Kind: issue.Kind, State: issue.State, Deleted: issue.AppDeleted, ExpiresAt: dashboardMCPTime(issue.ExpiresAt), Guidance: dashboardMCPRecoveryGuidance(issue.Kind)}
				if !issue.AppDeleted {
					row.AppURL = dashboardMCPURL(url.Values{}, "app", issue.AppSlug)
				}
				data.Issues = append(data.Issues, row)
			}
			if snapshot.NextOffset > 0 {
				data.NextHealthURL = dashboardMCPURL(query, "health_offset", strconv.Itoa(snapshot.NextOffset)) + "#connections"
			}
		}
	} else {
		data.HealthError = "Connection health is unavailable. No healthy state can be inferred."
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	if offset < len(apps) {
		end := min(offset+dashboardMCPAppPageSize, len(apps))
		for _, app := range apps[offset:end] {
			if app.AccountID != account.ID {
				continue
			}
			data.Apps = append(data.Apps, s.dashboardMCPApp(ctx, account.ID, app))
		}
		if end < len(apps) && end <= api.MCPConnectionHealthMaxOffset {
			data.NextAppsURL = dashboardMCPURL(query, "offset", strconv.Itoa(end)) + "#applications"
		}
	}
	if offset > 0 {
		previous := offset - dashboardMCPAppPageSize
		if previous < 0 {
			previous = 0
		}
		data.PreviousAppsURL = dashboardMCPURL(query, "offset", strconv.Itoa(previous)) + "#applications"
	}
	s.dashboardMCPRetirements(w, r, ctx, account, query, &data)
	page := dashboard.Page{Title: "MCP operations", Body: "mcp_operations", Account: dashboardAccountView(account, len(apps)), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) dashboardMCPApp(ctx context.Context, account string, app state.App) dashboard.MCPOperationsAppView {
	base := "/dashboard/apps/" + url.PathEscape(app.Slug)
	row := dashboard.MCPOperationsAppView{PerformanceURL: dashboardMCPURL(url.Values{}, "app", app.Slug), Slug: app.Slug, URL: base, DebugURL: base + "/debug#requests", LogsURL: base + "/logs", EdgeRulesURL: base + "/edge-rules", DeploymentsURL: base + "#deployments", CatalogState: "Unavailable", PolicyState: "Unavailable"}
	if source, ok := s.store.(state.MCPCatalogMonitorStore); ok {
		snapshot, err := source.GetMCPCatalogMonitor(ctx, account, app.ID)
		switch {
		case errors.Is(err, state.ErrNotFound):
			row.CatalogState = "Not configured"
		case err != nil:
			row.CatalogError = "Catalog evidence unavailable"
		default:
			summary := api.CatalogMonitorSummary(snapshot, app.Slug, time.Now().UTC())
			row.CatalogState = summary.State
			row.CatalogCheckedAt = dashboardMCPTime(summary.Signal.CheckedAt)
			roles := make([]string, 0, len(summary.Roles))
			for role := range summary.Roles {
				roles = append(roles, role)
			}
			sort.Strings(roles)
			for _, role := range roles {
				view := summary.Roles[role]
				signal := summary.Signal.Roles[role]
				item := dashboard.MCPCatalogRoleView{Scope: api.MCPConnectionScope(view.Config.Scope), Role: role, State: signal.State, CheckedAt: dashboardMCPTime(signal.CheckedAt), LastSuccessAt: "Not observed"}
				if status := view.ObserverStatus; status != nil {
					item.ObserverState = status.State
					item.LastSuccessAt = dashboardMCPTime(status.LastSuccessAt)
					item.Failures = status.ConsecutiveFailures
				}
				if private, ok := api.CatalogMonitorRoleSnapshot(snapshot, role); ok && private.Config.Observer != nil && private.Config.Observer.OAuth != nil {
					item.OAuthRevision = private.Config.Revision
					item.OAuthURL = "/dashboard/mcp/apps/" + url.PathEscape(app.Slug) + "/oauth"
					item.BrowserRedirectURI = private.Config.Observer.OAuth.BrowserRedirectURI
					item.BrowserEnabled = private.Config.Observer.OAuth.Guided && item.BrowserRedirectURI != "" && item.BrowserRedirectURI == s.dashboardMCPOAuthRedirect()
					item.RenewalRetryAllowed = api.MCPOAuthRenewalRetryAllowed(private, time.Now().UTC())
					item.ProviderConfirmationRequired = api.MCPOAuthReauthorizationNeedsConfirmation(private)
				}
				row.CatalogRoles = append(row.CatalogRoles, item)
			}
		}
	} else {
		row.CatalogError = "Catalog evidence unavailable"
	}
	if source, ok := s.store.(state.MCPPolicyConvergenceStore); ok {
		status, err := source.MCPPolicyConvergenceSnapshot(ctx, account, app.ID)
		if err != nil {
			row.PolicyError = "Policy evidence unavailable"
		} else {
			row.PolicyState = status.State
			row.PolicyCapturedAt = dashboardMCPTime(&status.CapturedAt)
			row.Serving = status.ServingGateways
			row.Pending = status.PendingGateways
			row.Stale = status.StaleGateways
		}
	} else {
		row.PolicyError = "Policy evidence unavailable"
	}
	deployments, err := s.store.ListDeploymentsForApp(ctx, app.ID, 1, 0)
	if err != nil {
		row.AdmissionError = "Admission evidence unavailable"
		return row
	}
	if len(deployments) == 0 {
		row.AdmissionScope = "No deployments"
		return row
	}
	row.AdmissionScope = deployments[0].ID
	reader, ok := s.store.(state.MCPAdmissionReader)
	if !ok {
		row.AdmissionError = "Admission evidence unavailable"
		return row
	}
	for _, outcome := range []string{"accepted", "rejected"} {
		history, e := reader.ListMCPAdmissionHistory(ctx, account, deployments[0].ID, 0, outcome, 1)
		if e != nil {
			row.AdmissionError = "Admission evidence unavailable"
			continue
		}
		if len(history.Items) > 0 {
			view := dashboardMCPAdmission(history.Items[0])
			if outcome == "accepted" {
				row.Accepted = &view
			} else {
				row.Rejected = &view
			}
		}
	}
	return row
}
func dashboardMCPRecoveryGuidance(kind string) string {
	switch kind {
	case "reauthorization_required":
		return "Reconnect this role using the CLI OAuth flow."
	case "renewal_failed":
		return "Inspect provider credentials and renewal history before retrying."
	case "retirement_expiring":
		return "Review pending grant retirement before its sealed retention expires."
	case "issuance_unknown":
		return "Confirm issuance at the provider before reconnecting."
	default:
		return "Inspect this connection using the CLI."
	}
}

func dashboardMCPOAuthNotice(notice string) string {
	switch notice {
	case "connected":
		return "OAuth connection established. The observer will verify catalog access."
	case "authorization_denied":
		return "Authorization was denied. No token exchange was attempted."
	case "authorization_expired":
		return "Authorization expired. Start a new connection flow."
	case "authorization_uncertain":
		return "The authorization outcome is uncertain. Resolve the retained grant at the provider before reconnecting; the code exchange will not be retried."
	case "authorization_failed":
		return "Authorization could not be completed. Reload connection health and inspect grant retirement before starting again."
	case "renewal_scheduled":
		return "Renewal recovery was queued for the observer. This is not confirmation that renewal succeeded."
	default:
		return ""
	}
}
