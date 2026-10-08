package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type RouteMonitorStore interface {
	GetRouteMonitor(context.Context, string, string) (api.RouteMonitorConfig, error)
	SetRouteMonitor(context.Context, string, string, api.SetRouteMonitorRequest) (api.RouteMonitorConfig, error)
	PreviewRouteMonitor(context.Context, string, string, api.PreviewRouteMonitorRequest) (api.RouteMonitorPreview, error)
	GetRouteMonitorReport(context.Context, string, string) (api.RouteMonitorReport, error)
	ListRouteMonitorIncidents(context.Context, string, string, int, string) (api.RouteMonitorIncidentPage, error)
	GetRouteMonitorIncident(context.Context, string, string, string) (api.RouteMonitorIncident, error)
}
type RouteMonitorTarget struct {
	AccountID, AppID string
	Revision         int64
	DueAt            time.Time
}
type RouteMonitorWorkerStore interface {
	ListDueRouteMonitors(context.Context) ([]RouteMonitorTarget, error)
	EvaluateRouteMonitor(context.Context, string, string) (bool, error)
	DeferRouteMonitor(context.Context, RouteMonitorTarget) error
}
type routeMonitorRecoveryState struct {
	Incomplete bool                        `json:"incomplete"`
	Routes     []routeMonitorRecoveryRoute `json:"routes"`
}
type routeMonitorRecoveryRoute struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	CustomerIDs []string `json:"customer_ids"`
}

func defaultRouteMonitor(appID string) api.RouteMonitorConfig {
	return api.RouteMonitorConfig{AppID: appID, Routes: []api.RouteMonitorRoute{}}
}
func newRouteMonitorIncident(r api.RouteMonitorReport, baseline *api.RouteMonitorDeploymentBaseline) api.RouteMonitorIncident {
	i := api.RouteMonitorIncident{Version: api.RouteMonitorVersion, ID: uuid.NewString(), AppID: r.AppID, DeploymentID: r.DeploymentID, Revision: r.Revision, Status: "open", OpenedAt: r.CheckedAt, OpeningReport: r, Evidence: []api.RouteMonitorEvidence{}, Timeline: []api.RouteMonitorIncidentTimelineEntry{routemonitor.IncidentTimelineEntry(r)}}
	if baseline != nil && baseline.DeploymentID != r.DeploymentID {
		copy := *baseline
		i.Baseline = &copy
	}
	return i
}
func closeRouteMonitorIncident(i *api.RouteMonitorIncident, status string, now time.Time, r *api.RouteMonitorReport) {
	i.Status = status
	i.ClosedAt = &now
	i.RecoveryReport = r
}
func encodeRouteMonitorIncident(i *api.RouteMonitorIncident) ([]byte, error) {
	for {
		body, err := json.Marshal(i)
		if err != nil {
			return nil, fmt.Errorf("encode production route incident: %w", err)
		}
		if len(body) <= api.RouteMonitorIncidentMaxBytes {
			return body, nil
		}
		if i != nil && len(i.Escalations) > 1 {
			i.Escalations = append(i.Escalations[:0], i.Escalations[1:]...)
			i.EscalationsTruncated = true
			continue
		}
		if i != nil && len(i.Escalations) == 1 && len(i.Escalations[0].Evidence) > 0 {
			last := &i.Escalations[0]
			last.Evidence = last.Evidence[:len(last.Evidence)-1]
			last.EvidenceTruncated = true
			continue
		}
		if i == nil || len(i.Timeline) <= 2 {
			return nil, fmt.Errorf("production route incident exceeds %d bytes", api.RouteMonitorIncidentMaxBytes)
		}
		// Preserve the opening baseline and newest state if other saved evidence
		// leaves less room than the normal timeline count cap.
		i.Timeline = append(i.Timeline[:1], i.Timeline[2:]...)
		i.TimelineTruncated = true
	}
}
func routeMonitorNotification(i api.RouteMonitorIncident, slug string) (AppWebhookEvent, []byte, error) {
	event := AppWebhookEventRouteMonitorViolated
	now := i.OpenedAt
	impactReport := i.OpeningReport
	if i.Status == "recovered" {
		event = AppWebhookEventRouteMonitorRecovered
		now = *i.ClosedAt
		if i.RecoveryReport != nil {
			impactReport = *i.RecoveryReport
		}
	}
	payload := routeMonitorWebhookPayload(i, slug, now, impactReport)
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("encode route monitor notification: %w", err)
	}
	return event, body, nil
}

func routeMonitorEscalationNotification(i api.RouteMonitorIncident, slug string, report api.RouteMonitorReport, escalation *api.RouteMonitorWebhookEscalation) (AppWebhookEvent, string, []byte, error) {
	if i.Status != "open" || escalation == nil || escalation.NewlyViolatedSignals < 1 || escalation.NewlyViolatedRoutes < 1 {
		return "", "", nil, fmt.Errorf("invalid route monitor escalation notification")
	}
	transitionID := routeMonitorEscalationTransitionID(i.ID, report.CheckedAt)
	payload := routeMonitorWebhookPayload(i, slug, report.CheckedAt, report)
	payload.TransitionID = transitionID
	payload.Escalation = escalation
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("encode route monitor escalation notification: %w", err)
	}
	return AppWebhookEventRouteMonitorEscalated, transitionID, body, nil
}

func routeMonitorEscalationTransitionID(incidentID string, checkedAt time.Time) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:route-monitor-escalation:"+incidentID+":"+checkedAt.UTC().Format(time.RFC3339Nano))).String()
}

func routeMonitorWebhookPayload(i api.RouteMonitorIncident, slug string, checkedAt time.Time, report api.RouteMonitorReport) api.RouteMonitorWebhookPayload {
	payload := api.RouteMonitorWebhookPayload{Version: api.RouteMonitorVersion, AppID: i.AppID, DeploymentID: i.DeploymentID, IncidentID: i.ID, Revision: i.Revision, Status: i.Status, CheckedAt: checkedAt, IncidentPath: "/v1/apps/" + url.PathEscape(slug) + "/route-monitor/incidents/" + i.ID}
	if customers := report.Customers; customers != nil {
		payload.CustomerImpact = &api.RouteMonitorCustomerImpact{GroupBy: customers.GroupBy, Coverage: customers.Coverage, ObservedCustomers: customers.ObservedCustomers, ViolatedCustomers: customers.ViolatedCustomers, UnknownCustomers: customers.UnknownCustomers}
	}
	return payload
}

// Optional identity projection keeps the ordinary monitor store usable by
// memory/test stores that cannot resolve production customer cohorts.
type RouteMonitorCustomerDetailsStore interface {
	GetRouteMonitorReportWithCustomerDetails(context.Context, string, string, bool) (api.RouteMonitorReport, error)
	GetRouteMonitorIncidentWithCustomerDetails(context.Context, string, string, string, bool) (api.RouteMonitorIncident, error)
}

type RouteMonitorPreviewDetailsStore interface {
	PreviewRouteMonitorWithCustomerDetails(context.Context, string, string, api.PreviewRouteMonitorRequest, bool) (api.RouteMonitorPreview, error)
}
