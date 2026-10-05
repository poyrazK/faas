package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type RouteMonitorStore interface {
	GetRouteMonitor(context.Context, string, string) (api.RouteMonitorConfig, error)
	SetRouteMonitor(context.Context, string, string, api.SetRouteMonitorRequest) (api.RouteMonitorConfig, error)
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
func newRouteMonitorIncident(r api.RouteMonitorReport) api.RouteMonitorIncident {
	return api.RouteMonitorIncident{Version: api.RouteMonitorVersion, ID: uuid.NewString(), AppID: r.AppID, DeploymentID: r.DeploymentID, Revision: r.Revision, Status: "open", OpenedAt: r.CheckedAt, OpeningReport: r, Evidence: []api.RouteMonitorEvidence{}}
}
func closeRouteMonitorIncident(i *api.RouteMonitorIncident, status string, now time.Time, r *api.RouteMonitorReport) {
	i.Status = status
	i.ClosedAt = &now
	i.RecoveryReport = r
}
func encodeRouteMonitorIncident(i api.RouteMonitorIncident) ([]byte, error) {
	body, err := json.Marshal(i)
	if err != nil {
		return nil, fmt.Errorf("encode production route incident: %w", err)
	}
	if len(body) > api.RouteMonitorIncidentMaxBytes {
		return nil, fmt.Errorf("production route incident exceeds %d bytes", api.RouteMonitorIncidentMaxBytes)
	}
	return body, nil
}
func routeMonitorNotification(i api.RouteMonitorIncident, slug string) (AppWebhookEvent, []byte, error) {
	event := AppWebhookEventRouteMonitorViolated
	now := i.OpenedAt
	if i.Status == "recovered" {
		event = AppWebhookEventRouteMonitorRecovered
		now = *i.ClosedAt
	}
	payload := api.RouteMonitorWebhookPayload{Version: api.RouteMonitorVersion, AppID: i.AppID, DeploymentID: i.DeploymentID, IncidentID: i.ID, Revision: i.Revision, Status: i.Status, CheckedAt: now, IncidentPath: "/v1/apps/" + url.PathEscape(slug) + "/route-monitor/incidents/" + i.ID}
	impactReport := i.OpeningReport
	if i.Status == "recovered" && i.RecoveryReport != nil {
		impactReport = *i.RecoveryReport
	}
	if customers := impactReport.Customers; customers != nil {
		payload.CustomerImpact = &api.RouteMonitorCustomerImpact{GroupBy: customers.GroupBy, Coverage: customers.Coverage, ObservedCustomers: customers.ObservedCustomers, ViolatedCustomers: customers.ViolatedCustomers}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("encode route monitor notification: %w", err)
	}
	return event, body, nil
}

// Optional identity projection keeps the ordinary monitor store usable by
// memory/test stores that cannot resolve production customer cohorts.
type RouteMonitorCustomerDetailsStore interface {
	GetRouteMonitorReportWithCustomerDetails(context.Context, string, string, bool) (api.RouteMonitorReport, error)
	GetRouteMonitorIncidentWithCustomerDetails(context.Context, string, string, string, bool) (api.RouteMonitorIncident, error)
}
