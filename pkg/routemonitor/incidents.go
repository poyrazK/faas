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
		if recovered.Status != "healthy" || recovered.AppID != r.AppID || recovered.DeploymentID != r.DeploymentID || recovered.Revision != r.Revision || !reflect.DeepEqual(recovered.ObservationAnchor, r.ObservationAnchor) || !recovered.CheckedAt.Equal(*i.ClosedAt) || len(recovered.Routes) != len(r.Routes) {
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
	expected := []api.RouteMonitorEvidence{}
	for _, f := range r.Routes {
		for _, signal := range []string{"errors", "latency"} {
			if signal == "errors" && f.ErrorStatus == "violated" || signal == "latency" && f.LatencyStatus == "violated" {
				expected = append(expected, api.RouteMonitorEvidence{Method: f.Route.Method, Path: f.Route.Path, Signal: signal})
			}
		}
	}
	if len(i.Evidence) != min(len(expected), api.RouteMonitorEvidenceRoutesLimit) || i.EvidenceTruncated != (len(expected) > api.RouteMonitorEvidenceRoutesLimit) {
		return errors.New("incident diagnostic coverage does not reconcile")
	}
	for j, e := range i.Evidence {
		if e.Method != expected[j].Method || e.Path != expected[j].Path || e.Signal != expected[j].Signal || len(e.Windows) != api.RouteHealthWindows {
			return errors.New("incident diagnostics do not match selected signal")
		}
		var finding api.RouteMonitorFinding
		for _, f := range r.Routes {
			if f.Route.Method == e.Method && f.Route.Path == e.Path {
				finding = f
				break
			}
		}
		f := api.RouteHealthFinding{Method: e.Method, Path: e.Path, CheckLatency: e.Signal == "latency", ErrorStatus: "regressed", LatencyStatus: "regressed", Windows: []api.RouteHealthWindowEvidence{}}
		for _, w := range finding.Windows {
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
