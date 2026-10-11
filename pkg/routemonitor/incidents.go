package routemonitor

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

func ValidateIncident(i api.RouteMonitorIncident, slug string) error {
	r := i.OpeningReport
	if i.Version != api.RouteMonitorVersion || !validUUID(i.ID) || i.AppID != r.AppID || i.DeploymentID != r.DeploymentID || i.Revision != r.Revision || !i.OpenedAt.Equal(r.CheckedAt) || r.Status != "violated" || len(i.Evidence) > api.RouteMonitorEvidenceRoutesLimit {
		return errors.New("invalid incident identity or opening evidence")
	}
	if err := ValidateReport(r); err != nil {
		return fmt.Errorf("invalid incident opening report: %w", err)
	}
	if err := validateIncidentBaseline(i); err != nil {
		return err
	}
	if err := validateIncidentTimeline(i); err != nil {
		return err
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
	if err := validateIncidentEscalations(i, slug); err != nil {
		return err
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

func validateIncidentBaseline(i api.RouteMonitorIncident) error {
	if i.Baseline == nil {
		return nil // Older saved incidents predate release-pair baselines.
	}
	b := i.Baseline
	if !validUUID(b.DeploymentID) || b.DeploymentID == i.DeploymentID {
		return errors.New("invalid route incident healthy baseline deployment")
	}
	if b.CommitSHA != "" && !routeimpact.ValidCommit(b.CommitSHA) {
		return errors.New("invalid route incident healthy baseline revision")
	}
	if b.Repository != "" {
		repository, reference := routeimpact.RepositoryReference(b.Repository)
		if repository == "" || repository != b.Repository || reference != "" {
			return errors.New("invalid route incident healthy baseline repository")
		}
	}
	if b.SourceRoot != "" {
		root, err := sourcecontext.Normalize(b.SourceRoot)
		if err != nil || root != b.SourceRoot {
			return errors.New("invalid route incident healthy baseline source root")
		}
	}
	return nil
}

func validateIncidentEscalations(i api.RouteMonitorIncident, slug string) error {
	if len(i.Escalations) > api.RouteMonitorIncidentEscalationMaxEntries || len(i.Escalations) == 0 && i.EscalationsTruncated {
		return errors.New("route incident escalation history exceeds its bounds")
	}
	if len(i.Escalations) == 0 {
		return nil
	}
	if len(i.Timeline) < 2 {
		return errors.New("route incident escalation has no timeline observations")
	}
	seenTransitions := map[string]bool{}
	for index, escalation := range i.Escalations {
		if !validUUID(escalation.TransitionID) || seenTransitions[escalation.TransitionID] || escalation.CheckedAt.IsZero() || escalation.PreviousCheckedAt.IsZero() || !escalation.CheckedAt.After(escalation.PreviousCheckedAt) || !validTimelineTime(escalation.PreviousCheckedAt, i.OpenedAt, i.ClosedAt) || !validTimelineTime(escalation.CheckedAt, i.OpenedAt, i.ClosedAt) || len(escalation.Signals) == 0 || len(escalation.Signals) > api.RouteMonitorIncidentEscalationSignalsMax || len(escalation.Evidence) > api.RouteMonitorIncidentEscalationEvidenceLimit {
			return errors.New("invalid route incident escalation identity or bounds")
		}
		seenTransitions[escalation.TransitionID] = true
		if index > 0 && !escalation.CheckedAt.After(i.Escalations[index-1].CheckedAt) {
			return errors.New("route incident escalations are not chronological")
		}
		previous, current := incidentTimelineEntryAt(i.Timeline, escalation.PreviousCheckedAt), incidentTimelineEntryAt(i.Timeline, escalation.CheckedAt)
		if previous == nil || current == nil {
			return errors.New("route incident escalation does not match retained timeline entries")
		}
		changed := NewlyViolatedIncidentTimelineSignals(*previous, *current)
		if len(changed) != len(escalation.Signals) || escalation.NewlyViolatedSignals != len(changed) {
			return errors.New("route incident escalation signal counts do not reconcile")
		}
		routes := map[int]struct{}{}
		for signalIndex, signal := range escalation.Signals {
			if signalIndex >= len(changed) || signal.RouteIndex != changed[signalIndex].RouteIndex || signal.Signal != changed[signalIndex].Signal || signal.RouteIndex < 0 || signal.RouteIndex >= len(i.OpeningReport.Routes) {
				return errors.New("route incident escalation signals do not match the timeline")
			}
			routes[signal.RouteIndex] = struct{}{}
			selector := i.OpeningReport.Routes[signal.RouteIndex].Route
			if !reflect.DeepEqual(signal.Finding.Route, selector) || signal.Finding.Status != "violated" || len(signal.Finding.Windows) != api.RouteHealthWindows {
				return errors.New("route incident escalation finding changed its route selector")
			}
			if signal.Signal == "errors" && selector.Max5xxRateBPS == nil || signal.Signal == "latency" && selector.MaxP95MS == 0 || signal.Signal != "errors" && signal.Signal != "latency" {
				return errors.New("route incident escalation selected an unconfigured signal")
			}
			status := signal.Finding.ErrorStatus
			if signal.Signal == "latency" {
				status = signal.Finding.LatencyStatus
			}
			if status != "violated" || current.Routes[signal.RouteIndex].ErrorStatus != signal.Finding.ErrorStatus || current.Routes[signal.RouteIndex].LatencyStatus != signal.Finding.LatencyStatus {
				return errors.New("route incident escalation finding does not match the transition")
			}
			expectedWindows := routehealth.Windows(escalation.CheckedAt)
			copyFinding := signal.Finding
			copyFinding.Windows = append([]api.RouteMonitorWindow(nil), signal.Finding.Windows...)
			for windowIndex, window := range copyFinding.Windows {
				if !window.Start.Equal(expectedWindows[windowIndex].Start) || !window.End.Equal(expectedWindows[windowIndex].End) || !validMonitorCounts(window.Observed) {
					return errors.New("route incident escalation has invalid observation windows")
				}
			}
			EvaluateFinding(&copyFinding, i.OpeningReport.ObservationAnchor)
			if !reflect.DeepEqual(copyFinding, signal.Finding) {
				return errors.New("route incident escalation verdict does not match its observations")
			}
		}
		if escalation.NewlyViolatedRoutes != len(routes) || escalation.NewlyViolatedRoutes < 1 || escalation.NewlyViolatedSignals < 1 {
			return errors.New("route incident escalation route counts do not reconcile")
		}
		wantEvidence := min(len(escalation.Signals), api.RouteMonitorIncidentEscalationEvidenceLimit)
		if !escalation.EvidenceTruncated && len(escalation.Evidence) != wantEvidence || len(escalation.Signals) > api.RouteMonitorIncidentEscalationEvidenceLimit && !escalation.EvidenceTruncated {
			return errors.New("route incident escalation diagnostic coverage does not reconcile")
		}
		for evidenceIndex, evidence := range escalation.Evidence {
			signal := escalation.Signals[evidenceIndex]
			finding := signal.Finding
			if evidence.CustomerGroupBy != "" || evidence.CustomerID != "" || evidence.Method != finding.Route.Method || evidence.Path != finding.Route.Path || evidence.Signal != signal.Signal {
				return errors.New("route incident escalation diagnostics changed the aggregate selector")
			}
			if err := validateIncidentEscalationEvidence(i, escalation.CheckedAt, signal, evidence, slug); err != nil {
				return err
			}
		}
	}
	return nil
}

func incidentTimelineEntryAt(entries []api.RouteMonitorIncidentTimelineEntry, at time.Time) *api.RouteMonitorIncidentTimelineEntry {
	for index := range entries {
		if entries[index].CheckedAt.Equal(at) {
			return &entries[index]
		}
	}
	return nil
}

func validateIncidentEscalationEvidence(i api.RouteMonitorIncident, checkedAt time.Time, selected api.RouteMonitorIncidentEscalationSignal, evidence api.RouteMonitorEvidence, slug string) error {
	monitorFinding := selected.Finding
	finding := api.RouteHealthFinding{
		Method: monitorFinding.Route.Method, Path: monitorFinding.Route.Path,
		CheckLatency: monitorFinding.Route.MaxP95MS > 0, MaxP95MS: monitorFinding.Route.MaxP95MS,
		Status: monitorFinding.Status, Reason: monitorFinding.Reason,
		ErrorStatus: monitorFinding.ErrorStatus, ErrorReason: monitorFinding.Reason,
		LatencyStatus: monitorFinding.LatencyStatus, LatencyReason: monitorFinding.Reason,
		Windows: []api.RouteHealthWindowEvidence{},
	}
	for _, window := range monitorFinding.Windows {
		finding.Windows = append(finding.Windows, api.RouteHealthWindowEvidence{
			Start: window.Start, End: window.End, Candidate: window.Observed,
			ErrorStatus: window.ErrorStatus, ErrorReason: window.ErrorReason,
			LatencyStatus: window.LatencyStatus, LatencyReason: window.LatencyReason,
		})
	}
	report := api.RouteHealthReport{AppID: i.AppID, DeploymentID: i.DeploymentID, StableDeploymentID: i.DeploymentID, Routes: []api.RouteHealthFinding{finding}}
	opts := api.RouteHealthInvestigationOptions{Method: evidence.Method, Path: evidence.Path, Signal: evidence.Signal}
	investigation := routehealth.NewInvestigation(report, finding, opts.Selection())
	investigation.EvidenceStatus = "observed"
	if len(evidence.Windows) != len(monitorFinding.Windows) {
		return errors.New("route incident escalation diagnostics have incomplete windows")
	}
	for windowIndex, window := range evidence.Windows {
		base := monitorFinding.Windows[windowIndex]
		if !window.Start.Equal(base.Start) || !window.End.Equal(base.End) || window.End.After(checkedAt) {
			return errors.New("route incident escalation diagnostics do not match the transition windows")
		}
		investigation.Windows[windowIndex] = api.RouteHealthInvestigationWindow{
			Start: window.Start, End: window.End, Candidate: window.Requests,
			Stable:      api.RouteHealthInvestigationSide{Examples: []api.RouteHealthInvestigationExample{}},
			Diagnostics: window.Diagnostics,
		}
	}
	if err := routehealth.ValidateInvestigation(investigation, opts, slug); err != nil {
		return fmt.Errorf("invalid saved route escalation diagnostics: %w", err)
	}
	return nil
}

func validateIncidentTimeline(i api.RouteMonitorIncident) error {
	if len(i.Timeline) == 0 {
		if i.TimelineTruncated {
			return errors.New("empty route incident timeline is marked truncated")
		}
		return nil // Incidents persisted before timeline support remain readable.
	}
	if len(i.Timeline) > api.RouteMonitorIncidentTimelineMaxEntries {
		return errors.New("route incident timeline exceeds its bounds")
	}
	if !reflect.DeepEqual(i.Timeline[0], IncidentTimelineEntry(i.OpeningReport)) {
		return errors.New("route incident timeline does not start with the opening report")
	}
	for index, entry := range i.Timeline {
		if entry.CheckedAt.IsZero() || !validTimelineTime(entry.CheckedAt, i.OpenedAt, i.ClosedAt) || entry.Coverage != "observed_only" || !validIncidentStatus(entry.Status) || entry.Reason == "" || len(entry.Routes) != len(i.OpeningReport.Routes) {
			return errors.New("invalid route incident timeline observation")
		}
		if index > 0 && !entry.CheckedAt.After(i.Timeline[index-1].CheckedAt) {
			return errors.New("route incident timeline is not chronological")
		}
		if i.OpeningReport.CustomerGroupBy == "" {
			if entry.CustomerImpact != nil {
				return errors.New("route incident timeline has unselected customer impact")
			}
		} else if !validIncidentCustomerImpact(entry.CustomerImpact, i.OpeningReport.CustomerGroupBy) {
			return errors.New("invalid route incident customer impact summary")
		}
		for routeIndex, route := range entry.Routes {
			if route.RouteIndex != routeIndex || !validIncidentStatus(route.Status) || !validIncidentStatus(route.ErrorStatus) || !validIncidentStatus(route.LatencyStatus) || route.Status != combine(route.ErrorStatus, route.LatencyStatus) {
				return errors.New("invalid route incident timeline route status")
			}
			if i.OpeningReport.CustomerGroupBy == "" {
				if route.CustomerImpact != nil {
					return errors.New("route incident timeline has unselected route customer impact")
				}
			} else if !validIncidentCustomerImpact(route.CustomerImpact, i.OpeningReport.CustomerGroupBy) {
				return errors.New("invalid route incident timeline route customer impact")
			}
		}
	}
	return nil
}

func validIncidentStatus(status string) bool {
	switch status {
	case "healthy", "violated", "unknown", "disabled":
		return true
	default:
		return false
	}
}

func validIncidentCustomerImpact(impact *api.RouteMonitorCustomerImpact, groupBy string) bool {
	return impact != nil && impact.GroupBy == groupBy && impact.Coverage == "observed_only" && impact.ObservedCustomers >= 0 && impact.ViolatedCustomers >= 0 && impact.UnknownCustomers >= 0 && impact.ViolatedCustomers <= impact.ObservedCustomers && impact.UnknownCustomers <= impact.ObservedCustomers && impact.ViolatedCustomers <= impact.ObservedCustomers-impact.UnknownCustomers
}
