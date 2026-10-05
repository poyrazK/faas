package routemonitor

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func ValidateIncident(i api.RouteMonitorIncident, slug string) error {
	r := i.OpeningReport
	if i.Version != api.RouteMonitorVersion || !validUUID(i.ID) || i.AppID != r.AppID || i.DeploymentID != r.DeploymentID || i.Revision != r.Revision || !i.OpenedAt.Equal(r.CheckedAt) || r.Status != "violated" || len(i.Evidence) > api.RouteMonitorEvidenceRoutesLimit {
		return errors.New("invalid incident identity or opening evidence")
	}
	if err := ValidateReport(r); err != nil {
		return fmt.Errorf("invalid incident opening report: %w", err)
	}
	switch i.Status {
	case "open":
		if i.ClosedAt != nil || i.RecoveryReport != nil {
			return errors.New("open incident has closing evidence")
		}
	case "recovered":
		if i.ClosedAt == nil || i.ClosedAt.Before(i.OpenedAt) || i.RecoveryReport == nil {
			return errors.New("recovery requires comparable healthy evidence")
		}
		recovered := *i.RecoveryReport
		if err := ValidateReport(recovered); err != nil {
			return err
		}
		if recovered.CustomerGroupBy != r.CustomerGroupBy || recovered.Status != "healthy" || recovered.AppID != r.AppID || recovered.DeploymentID != r.DeploymentID || recovered.Revision != r.Revision || !reflect.DeepEqual(recovered.ObservationAnchor, r.ObservationAnchor) || !recovered.CheckedAt.Equal(*i.ClosedAt) || len(recovered.Routes) != len(r.Routes) {
			return errors.New("recovery context changed")
		}
		for j := range r.Routes {
			if !reflect.DeepEqual(r.Routes[j].Route, recovered.Routes[j].Route) {
				return errors.New("recovery budgets changed")
			}
		}
	case "superseded":
		if i.ClosedAt == nil || i.ClosedAt.Before(i.OpenedAt) || i.RecoveryReport != nil {
			return errors.New("superseded incident has invalid closure")
		}
	default:
		return errors.New("unknown incident state")
	}
	// Bind the saved one-sided request inventory to each opening route/signal.
	// The existing debugger validator checks weights, percentiles, redacted
	// dependency groups, exact windows and authenticated paths.
	type expectedEvidence struct {
		api.RouteMonitorEvidence
		route   api.RouteMonitorRoute
		windows []api.RouteMonitorWindow
	}
	expected := []expectedEvidence{}
	for _, f := range r.Routes {
		for _, signal := range []string{"errors", "latency"} {
			if signal == "errors" && f.ErrorStatus == "violated" || signal == "latency" && f.LatencyStatus == "violated" {
				expected = append(expected, expectedEvidence{RouteMonitorEvidence: api.RouteMonitorEvidence{Method: f.Route.Method, Path: f.Route.Path, Signal: signal}, route: f.Route, windows: f.Windows})
			}
		}
	}
	if customers := r.Customers; customers != nil {
		for j, route := range customers.Routes {
			for _, cohort := range route.Customers {
				if cohort.Status != "violated" {
					continue
				}
				for _, signal := range []string{"errors", "latency"} {
					if signal == "errors" && cohort.ErrorStatus != "violated" || signal == "latency" && cohort.LatencyStatus != "violated" {
						continue
					}
					customerID := ""
					if customers.DetailsIncluded {
						customerID = cohort.CustomerID
					}
					expected = append(expected, expectedEvidence{RouteMonitorEvidence: api.RouteMonitorEvidence{CustomerGroupBy: customers.GroupBy, CustomerID: customerID, Method: route.Method, Path: route.Path, Signal: signal}, route: r.Routes[j].Route, windows: cohort.Windows})
				}
			}
		}
	}
	if len(i.Evidence) != min(len(expected), api.RouteMonitorEvidenceRoutesLimit) || i.EvidenceTruncated != (len(expected) > api.RouteMonitorEvidenceRoutesLimit) {
		return errors.New("incident diagnostic coverage does not reconcile")
	}
	for j, e := range i.Evidence {
		want := expected[j]
		if e.Method != want.Method || e.Path != want.Path || e.Signal != want.Signal || e.CustomerGroupBy != want.CustomerGroupBy || e.CustomerID != want.CustomerID || len(e.Windows) != api.RouteHealthWindows {
			return errors.New("incident diagnostics do not match selected signal")
		}
		f := api.RouteHealthFinding{Method: e.Method, Path: e.Path, CheckLatency: want.route.MaxP95MS > 0, MaxP95MS: want.route.MaxP95MS, ErrorStatus: "regressed", LatencyStatus: "regressed", Windows: []api.RouteHealthWindowEvidence{}}
		for _, w := range want.windows {
			f.Windows = append(f.Windows, api.RouteHealthWindowEvidence{Start: w.Start, End: w.End, Candidate: w.Observed})
		}
		report := api.RouteHealthReport{AppID: r.AppID, DeploymentID: r.DeploymentID, StableDeploymentID: r.DeploymentID, Routes: []api.RouteHealthFinding{f}}
		opts := api.RouteHealthInvestigationOptions{Method: e.Method, Path: e.Path, Signal: e.Signal}
		investigation := routehealth.NewInvestigation(report, f, opts.Selection())
		investigation.EvidenceStatus = "observed"
		for k, w := range e.Windows {
			investigation.Windows[k] = api.RouteHealthInvestigationWindow{Start: w.Start, End: w.End, Candidate: w.Requests, Stable: api.RouteHealthInvestigationSide{Examples: []api.RouteHealthInvestigationExample{}}, Diagnostics: w.Diagnostics}
		}
		if err := routehealth.ValidateInvestigation(investigation, opts, slug); err != nil {
			return fmt.Errorf("invalid saved route diagnostics: %w", err)
		}
	}
	return nil
}
