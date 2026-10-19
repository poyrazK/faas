package apidserver

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardMCPReadiness(ctx context.Context, account, app string, data *dashboard.MCPOperationsData) {
	if app == "" {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ctx = readCtx
	s.dashboardMCPAttention(ctx, account, app, data)
	data.ReadinessNotificationsError = "Notification queue health is unavailable."
	if source, ok := s.store.(state.MCPReadinessNotificationStore); ok {
		page, err := source.ListMCPReadinessNotificationQueues(ctx, account, app, api.MCPConnectionScope(data.ReadinessScope), data.ReadinessNotificationsAfter, 20, time.Now().UTC())
		if err == nil {
			data.ReadinessNotificationsError = ""
			for _, q := range page.Queues {
				view := dashboard.MCPReadinessNotificationView{MCPReadinessNotificationQueue: q, RetryRequestID: uuid.NewString(), AbandonRequestID: uuid.NewString()}
				for _, destination := range q.Destinations {
					view.DestinationQueues = append(view.DestinationQueues, dashboard.MCPReadinessDestinationQueueView{MCPReadinessDestinationQueue: destination, RetryRequestID: uuid.NewString(), AbandonRequestID: uuid.NewString()})
				}
				data.ReadinessNotifications = append(data.ReadinessNotifications, view)
			}
			if page.NextAfter != "" {
				data.ReadinessNotificationsNextURL = "/dashboard/mcp?app=" + url.QueryEscape(data.AppFilter) + "&scope=" + url.QueryEscape(api.MCPConnectionScope(data.ReadinessScope)) + "&notification_after=" + url.QueryEscape(page.NextAfter) + "#readiness-notifications"
			}
		}
	}
	data.ReadinessMaintenanceError = "Maintenance history is unavailable."
	if windows, ok := s.store.(state.MCPReadinessMaintenanceStore); ok {
		page, err := windows.ListMCPReadinessMaintenance(ctx, account, app, api.MCPConnectionScope(data.ReadinessScope), "", 20)
		if err == nil {
			data.ReadinessMaintenanceError = ""
			now := time.Now().UTC()
			for _, window := range page.Windows {
				view := dashboard.MCPReadinessMaintenanceView{MCPReadinessMaintenanceWindow: window, State: "active", CanCancel: true}
				switch {
				case window.CancelledAt != nil:
					view.State = "cancelled"
					view.CanCancel = false
				case !now.Before(window.EndsAt):
					view.State = "expired"
					view.CanCancel = false
				case now.Before(window.StartsAt):
					view.State = "scheduled"
				}
				data.ReadinessMaintenance = append(data.ReadinessMaintenance, view)
			}
		}
	}
	data.ReadinessIncidentsError = "Incident history is unavailable."
	if incidents, ok := s.store.(state.MCPReadinessIncidentStore); ok {
		page, err := incidents.ListMCPReadinessIncidents(ctx, account, app, api.MCPConnectionScope(data.ReadinessScope), data.ReadinessIncidentsBefore, 20)
		if err == nil {
			data.ReadinessIncidentsError = ""
			data.ReadinessIncidents = page.Events
			if page.NextBefore != "" {
				data.ReadinessIncidentsNextURL = "/dashboard/mcp?app=" + url.QueryEscape(data.AppFilter) + "&scope=" + url.QueryEscape(api.MCPConnectionScope(data.ReadinessScope)) + "&incident_before=" + url.QueryEscape(page.NextBefore) + "#readiness-incidents"
			}
		}
	}
	data.ReadinessState = "Unavailable"
	data.ReadinessHistoryUnavailable = true
	source, ok := s.store.(state.MCPReadinessStore)
	if !ok {
		return
	}
	snapshot, err := source.ListMCPReadiness(ctx, account, app, "", api.MCPConnectionScope(data.ReadinessScope))
	if err != nil {
		return
	}
	data.ReadinessState = "Disabled"
	if snapshot.Policy.Revision == "" && api.MCPConnectionScope(data.ReadinessScope) != api.DefaultEnvScope {
		data.ReadinessState = "Not configured — no readiness policy is saved for this environment"
	}
	if snapshot.Policy.Enabled {
		data.ReadinessState = fmt.Sprintf("Enforced · policy %s · maximum evidence age %ds", snapshot.Policy.Revision, snapshot.Policy.MaxAgeSeconds)
	}
	if history, ok := s.store.(state.MCPReadinessPolicyHistoryStore); ok {
		page, e := history.ListMCPReadinessPolicyHistory(ctx, account, app, api.MCPConnectionScope(data.ReadinessScope), "", 20)
		if e != nil {
			data.ReadinessHistoryUnavailable = true
		} else {
			data.ReadinessPolicyHistory = page.Policies
			data.ReadinessHistoryUnavailable = false
		}
	} else {
		data.ReadinessHistoryUnavailable = true
	}
	data.ReadinessMonitoringState = "Unavailable"
	if monitors, ok := s.store.(state.MCPReadinessMonitorStore); ok {
		statuses, e := monitors.ListMCPReadinessMonitoring(ctx, account, app, "", api.MCPConnectionScope(data.ReadinessScope))
		if e == nil {
			data.ReadinessMonitoringState = "No serving deployment roles are scheduled"
			if len(statuses) > 0 {
				data.ReadinessMonitoringState = "Enabled"
			}
			for _, status := range statuses {
				row := dashboard.MCPReadinessMonitorView{Deployment: status.DeploymentID, Role: status.Role, State: status.State, Reason: status.Reason, NextProbe: status.NextProbeAt.UTC().Format(time.RFC3339), Failures: status.ConsecutiveFailures, Threshold: status.FailureThreshold}
				if status.OAuthConnection != nil {
					row.ConnectionRole = status.OAuthConnection.Role + " (" + api.MCPConnectionScope(status.OAuthConnection.Scope) + ")"
				}
				switch status.RecoveryAction {
				case "reconnect":
					row.RecoveryAction = "Reconnect"
				case "review_binding":
					row.RecoveryAction = "Review connection binding"
				case "review_connection":
					row.RecoveryAction = "Review connection"
				}
				if row.RecoveryAction != "" {
					row.RecoveryURL = "/dashboard/mcp?app=" + url.QueryEscape(data.AppFilter) + "#connections"
				}
				if status.LastSuccessAt != nil {
					row.LastSuccess = status.LastSuccessAt.UTC().Format(time.RFC3339)
				}
				for _, observation := range status.History {
					label := "passed"
					if !observation.Passed {
						label = observation.Reason
					}
					row.History = append(row.History, observation.ObservedAt.UTC().Format(time.RFC3339)+" · "+label)
				}
				data.ReadinessMonitoring = append(data.ReadinessMonitoring, row)
			}
		}
	}
	subjects := map[string]state.MCPReadinessSubject{}
	failed := map[string]bool{}
	for _, e := range snapshot.Evidence {
		stateLabel := "Failed"
		if e.Passed {
			stateLabel = "Passed"
		}
		if e.PolicyRevision != snapshot.Policy.Revision {
			stateLabel = "Previous policy"
		} else if time.Since(e.ObservedAt) > time.Duration(snapshot.Policy.MaxAgeSeconds)*time.Second {
			stateLabel = "Stale"
		}
		// Compare current runtime subject as well as policy freshness. Deleted or
		// non-live deployments cannot be represented as currently ready.
		current, seen := subjects[e.DeploymentID]
		if !seen && !failed[e.DeploymentID] {
			var subjectErr error
			current, subjectErr = source.MCPReadinessSubject(ctx, account, e.DeploymentID)
			if subjectErr != nil {
				failed[e.DeploymentID] = true
			} else {
				subjects[e.DeploymentID] = current
			}
		}
		if failed[e.DeploymentID] {
			stateLabel = "Deployment unavailable"
		} else if current.Policy.Revision != e.PolicyRevision {
			stateLabel = "Previous policy"
		} else if current.ArtifactSHA256 != e.ArtifactSHA256 {
			stateLabel = "Deployment configuration changed"
		}
		row := dashboard.MCPReadinessView{Deployment: e.DeploymentID, Role: e.Role, State: stateLabel, ObservedAt: e.ObservedAt.UTC().Format(time.RFC3339)}
		for _, check := range e.Checks {
			if check.Status == "failed" {
				row.Detail = check.Detail
				break
			}
		}
		data.Readiness = append(data.Readiness, row)
	}
	for _, o := range snapshot.Overrides {
		stateLabel := "Unused"
		if o.UsedAt != nil {
			stateLabel = "Used"
		} else if time.Now().After(o.ExpiresAt) {
			stateLabel = "Expired"
		} else if o.PolicyRevision != snapshot.Policy.Revision {
			stateLabel = "Previous policy"
		}
		data.ReadinessOverrides = append(data.ReadinessOverrides, dashboard.MCPReadinessOverrideView{Deployment: o.DeploymentID, Reason: o.Reason, Account: o.CreatedBy, State: stateLabel, CreatedAt: o.CreatedAt.UTC().Format(time.RFC3339)})
	}
}
