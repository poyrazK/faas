package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

var ErrRouteHealthRevision = errors.New("route health configuration revision changed")
var ErrRouteHealthPlan = errors.New("route health enforcement requires traffic splits and request telemetry")

type RouteHealthBlockedError struct{ Decision api.RouteHealthDecision }

func (e *RouteHealthBlockedError) Error() string {
	return "route health gate blocked: " + e.Decision.Reason
}

type RouteHealthStore interface {
	GetRouteHealthGate(context.Context, string, string) (api.RouteHealthGate, error)
	SetRouteHealthGate(context.Context, string, string, api.SetRouteHealthGateRequest) (api.RouteHealthGate, error)
	GetRouteHealthReport(context.Context, string, string, string) (api.RouteHealthReport, error)
}

// Customer health is opt-in and computed only for live report reads.
type RouteCustomerHealthStore interface {
	GetRouteHealthReportWithCustomers(context.Context, string, string, string, string, bool) (api.RouteHealthReport, error)
}

func defaultRouteHealthGate(appID string) api.RouteHealthGate {
	return api.RouteHealthGate{AppID: appID, Mode: "report", OnRegression: "hold", Routes: []api.RouteHealthRoute{}}
}
func newRouteHealthReport(g api.RouteHealthGate, d Deployment, now time.Time) (api.RouteHealthReport, *time.Time) {
	report := api.RouteHealthReport{AppID: g.AppID, DeploymentID: d.ID, CandidateCommitSHA: d.CommitSHA, CanaryStep: d.CanaryStep, Mode: g.Mode, OnRegression: g.OnRegression, Revision: g.Revision, CheckedAt: now, Routes: []api.RouteHealthFinding{}}
	for _, r := range g.Routes {
		report.Routes = append(report.Routes, api.RouteHealthFinding{WatchStatuses: slices.Clone(r.WatchStatuses), Method: r.Method, Path: r.Path, CheckLatency: r.CheckLatency, MaxP95MS: r.MaxP95MS, Windows: routehealth.Windows(now)})
	}
	anchor := d.CanaryStepStartedAt
	if anchor != nil {
		copy := *anchor
		anchor = &copy
	}
	if g.UpdatedAt != nil && anchor != nil && g.UpdatedAt.After(*anchor) {
		copy := *g.UpdatedAt
		anchor = &copy
	}
	report.ObservationAnchor = anchor
	return report, anchor
}
func requireRouteHealth(report api.RouteHealthReport, output *api.RouteHealthDecision, historyID string) error {
	decision := routehealth.Decision(report)
	decision.HistoryID = historyID
	if output != nil {
		*output = decision
	}
	if decision.Mode == "enforce" && decision.Status != "allowed" {
		return &RouteHealthBlockedError{Decision: decision}
	}
	return nil
}
func legacyRouteHealth(g api.RouteHealthGate, d Deployment, action string) error {
	if g.Mode != "enforce" || d.CanaryTotalSteps <= 0 || action == "abort" {
		return nil
	}
	return &RouteHealthBlockedError{Decision: api.RouteHealthDecision{Mode: g.Mode, Revision: g.Revision, DeploymentID: d.ID, Status: "blocked", Reason: "use_canary_advance"}}
}
func stampRouteHealthAudit(params *CanaryAdvanceParams) error {
	if params.RouteHealthDecision == nil {
		return nil
	}
	var fields map[string]any
	if len(params.Audit.Data) > 0 {
		if err := json.Unmarshal(params.Audit.Data, &fields); err != nil {
			return fmt.Errorf("decode route health audit: %w", err)
		}
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["route_health"] = *params.RouteHealthDecision
	body, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode route health audit: %w", err)
	}
	params.Audit.Data = body
	return nil
}
