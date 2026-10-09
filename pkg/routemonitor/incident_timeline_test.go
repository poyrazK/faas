package routemonitor

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func productionIncidentFixture(t *testing.T) api.RouteMonitorIncident {
	t.Helper()
	body, err := os.ReadFile("../../tests/fixtures/production-route-incident.json")
	if err != nil {
		t.Fatal(err)
	}
	var incident api.RouteMonitorIncident
	if err := json.Unmarshal(body, &incident); err != nil {
		t.Fatal(err)
	}
	return incident
}

func TestIncidentTimelineKeepsOpeningAndNewestSnapshots(t *testing.T) {
	incident := productionIncidentFixture(t)
	opening := IncidentTimelineEntry(incident.OpeningReport)
	incident.Timeline = []api.RouteMonitorIncidentTimelineEntry{opening}
	for index := 1; index <= api.RouteMonitorIncidentTimelineMaxEntries+4; index++ {
		report := incident.OpeningReport
		report.CheckedAt = incident.OpenedAt.Add(time.Duration(index) * time.Minute)
		AppendIncidentTimeline(&incident, report)
	}
	if len(incident.Timeline) != api.RouteMonitorIncidentTimelineMaxEntries || !incident.TimelineTruncated {
		t.Fatalf("timeline bound not enforced: entries=%d truncated=%t", len(incident.Timeline), incident.TimelineTruncated)
	}
	if incident.Timeline[0].CheckedAt != opening.CheckedAt || incident.Timeline[len(incident.Timeline)-1].CheckedAt != incident.OpenedAt.Add(time.Duration(api.RouteMonitorIncidentTimelineMaxEntries+4)*time.Minute) {
		t.Fatal("opening baseline or newest observation was discarded")
	}
	if err := ValidateIncident(incident, "demo"); err != nil {
		t.Fatalf("bounded timeline rejected: %v", err)
	}
}

func TestProjectLegacyIncidentProvidesOpeningBaseline(t *testing.T) {
	incident := productionIncidentFixture(t)
	if len(incident.Timeline) != 0 {
		t.Fatal("fixture should represent a legacy incident without timeline data")
	}
	projected := ProjectIncident(incident, false)
	if len(projected.Timeline) != 1 || projected.Timeline[0].CheckedAt != incident.OpenedAt || projected.TimelineTruncated {
		t.Fatalf("legacy incident did not receive a truthful opening baseline: %+v", projected.Timeline)
	}
	if err := ValidateIncident(projected, "demo"); err != nil {
		t.Fatalf("legacy incident projection failed validation: %v", err)
	}
}

func TestIncidentHealthyBaselineIsValidatedAndLegacySafe(t *testing.T) {
	incident := productionIncidentFixture(t)
	if err := ValidateIncident(incident, "demo"); err != nil {
		t.Fatalf("legacy incident without a baseline failed validation: %v", err)
	}
	incident.Baseline = &api.RouteMonitorDeploymentBaseline{
		DeploymentID: "66666666-6666-4666-8666-666666666666",
		CommitSHA:    strings.Repeat("b", 40),
		Repository:   "github.com/team/service",
		SourceRoot:   ".",
	}
	if err := ValidateIncident(incident, "demo"); err != nil {
		t.Fatalf("valid healthy baseline failed validation: %v", err)
	}
	cases := map[string]func(*api.RouteMonitorDeploymentBaseline){
		"same deployment":       func(b *api.RouteMonitorDeploymentBaseline) { b.DeploymentID = incident.DeploymentID },
		"invalid deployment id": func(b *api.RouteMonitorDeploymentBaseline) { b.DeploymentID = "not-a-uuid" },
		"invalid revision":      func(b *api.RouteMonitorDeploymentBaseline) { b.CommitSHA = "abcd" },
		"noncanonical repo":     func(b *api.RouteMonitorDeploymentBaseline) { b.Repository = "https://github.com/team/service" },
		"unsafe source root":    func(b *api.RouteMonitorDeploymentBaseline) { b.SourceRoot = "../outside" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			copy := incident
			baseline := *incident.Baseline
			mutate(&baseline)
			copy.Baseline = &baseline
			if err := ValidateIncident(copy, "demo"); err == nil {
				t.Fatal("invalid healthy baseline was accepted")
			}
		})
	}
}

func TestIncidentTimelineContainsAggregateCustomerCountsOnly(t *testing.T) {
	incident := productionIncidentFixture(t)
	report := incident.OpeningReport
	report.CustomerGroupBy = "tenant"
	report.Customers = &api.RouteMonitorCustomerReport{
		GroupBy: "tenant", Coverage: "observed_only", ObservedCustomers: 5, ViolatedCustomers: 2, UnknownCustomers: 1,
		Routes: []api.RouteMonitorCustomerRoute{{Method: report.Routes[0].Route.Method, Path: report.Routes[0].Route.Path, ObservedCustomers: 3, ViolatedCustomers: 2, UnknownCustomers: 1}},
	}
	entry := IncidentTimelineEntry(report)
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "customer_id") || !strings.Contains(string(body), `"violated_customers":2`) || !strings.Contains(string(body), `"unknown_customers":1`) {
		t.Fatalf("timeline should retain aggregate counts without identity fields: %s", body)
	}
}

func TestIncidentTimelineEscalationReportsOnlyNewViolatedSignals(t *testing.T) {
	checkedAt := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	previous := api.RouteMonitorIncidentTimelineEntry{
		CheckedAt: checkedAt,
		Routes: []api.RouteMonitorIncidentTimelineRoute{
			{RouteIndex: 0, ErrorStatus: "violated", LatencyStatus: "healthy"},
			{RouteIndex: 1, ErrorStatus: "unknown", LatencyStatus: "healthy"},
		},
	}
	current := api.RouteMonitorIncidentTimelineEntry{
		CheckedAt: checkedAt.Add(time.Minute),
		Routes: []api.RouteMonitorIncidentTimelineRoute{
			{RouteIndex: 0, ErrorStatus: "violated", LatencyStatus: "violated"},
			{RouteIndex: 1, ErrorStatus: "violated", LatencyStatus: "violated"},
		},
	}
	escalation := IncidentTimelineEscalation(previous, current)
	if escalation == nil || !escalation.PreviousCheckedAt.Equal(checkedAt) || escalation.NewlyViolatedRoutes != 2 || escalation.NewlyViolatedSignals != 3 {
		t.Fatalf("new route and signal transitions not summarized: %+v", escalation)
	}
	changes := NewlyViolatedIncidentTimelineSignals(previous, current)
	wantChanges := []IncidentTimelineSignal{{RouteIndex: 0, Signal: "latency"}, {RouteIndex: 1, Signal: "errors"}, {RouteIndex: 1, Signal: "latency"}}
	if len(changes) != len(wantChanges) {
		t.Fatalf("transition signal identities do not match: %+v", changes)
	}
	for index := range changes {
		if changes[index] != wantChanges[index] {
			t.Fatalf("transition signal order is not stable: got=%+v want=%+v", changes, wantChanges)
		}
	}
	if got := IncidentTimelineEscalation(current, current); got != nil {
		t.Fatalf("duplicate observation produced escalation: %+v", got)
	}
	unchanged := current
	unchanged.CheckedAt = checkedAt.Add(2 * time.Minute)
	if got := IncidentTimelineEscalation(current, unchanged); got != nil {
		t.Fatalf("unchanged violations produced escalation: %+v", got)
	}
	recovered := current
	recovered.CheckedAt = checkedAt.Add(2 * time.Minute)
	recovered.Routes = []api.RouteMonitorIncidentTimelineRoute{
		{RouteIndex: 0, ErrorStatus: "healthy", LatencyStatus: "healthy"},
		{RouteIndex: 1, ErrorStatus: "healthy", LatencyStatus: "healthy"},
	}
	if got := IncidentTimelineEscalation(current, recovered); got != nil {
		t.Fatalf("recovery produced escalation: %+v", got)
	}
	recurrent := current
	recurrent.CheckedAt = checkedAt.Add(3 * time.Minute)
	if got := IncidentTimelineEscalation(recovered, recurrent); got == nil || got.NewlyViolatedRoutes != 2 || got.NewlyViolatedSignals != 4 {
		t.Fatalf("recurrence after recovery was not recognized: %+v", got)
	}
}

func TestIncidentEscalationEvidenceValidatesAndProjectsRedacted(t *testing.T) {
	incident := productionIncidentFixture(t)
	opening := IncidentTimelineEntry(incident.OpeningReport)
	previous := opening
	previous.CheckedAt = incident.OpenedAt.Add(time.Minute)
	previous.Routes = append([]api.RouteMonitorIncidentTimelineRoute(nil), opening.Routes...)
	previous.Routes[0].ErrorStatus = "healthy"
	previous.Routes[0].Status = previous.Routes[0].LatencyStatus
	previous.Status = "violated"
	previous.Reason = "sustained_budget_violation"

	report := incident.OpeningReport
	report.CheckedAt = incident.OpenedAt.Add(2 * time.Minute)
	report.Routes = append([]api.RouteMonitorFinding(nil), report.Routes...)
	finding := report.Routes[0]
	finding.Windows = append([]api.RouteMonitorWindow(nil), finding.Windows...)
	for index, window := range routehealth.Windows(report.CheckedAt) {
		finding.Windows[index].Start = window.Start
		finding.Windows[index].End = window.End
	}
	EvaluateFinding(&finding, report.ObservationAnchor)
	report.Routes[0] = finding
	current := IncidentTimelineEntry(report)
	incident.Timeline = []api.RouteMonitorIncidentTimelineEntry{opening, previous, current}

	var evidence api.RouteMonitorEvidence
	for _, candidate := range incident.Evidence {
		if candidate.Signal == "errors" {
			evidence = candidate
			break
		}
	}
	if evidence.Signal == "" {
		t.Fatal("fixture has no opening error diagnostics")
	}
	evidenceBody, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var copiedEvidence api.RouteMonitorEvidence
	if err := json.Unmarshal(evidenceBody, &copiedEvidence); err != nil {
		t.Fatal(err)
	}
	evidence = copiedEvidence
	for index := range evidence.Windows {
		evidence.Windows[index].Start = finding.Windows[index].Start
		evidence.Windows[index].End = finding.Windows[index].End
		for exampleIndex := range evidence.Windows[index].Requests.Examples {
			evidence.Windows[index].Requests.Examples[exampleIndex].ReceivedAt = finding.Windows[index].Start.Add(time.Second)
		}
	}
	incident.Escalations = []api.RouteMonitorIncidentEscalation{{
		TransitionID: "44444444-4444-4444-8444-444444444444", CheckedAt: report.CheckedAt, PreviousCheckedAt: previous.CheckedAt,
		NewlyViolatedRoutes: 1, NewlyViolatedSignals: 1,
		Signals:  []api.RouteMonitorIncidentEscalationSignal{{RouteIndex: 0, Signal: "errors", Finding: finding}},
		Evidence: []api.RouteMonitorEvidence{evidence},
	}}
	if err := ValidateIncident(incident, "demo"); err != nil {
		t.Fatalf("valid escalation evidence was rejected: %v", err)
	}
	withIdentity := incident
	withIdentity.Escalations = append([]api.RouteMonitorIncidentEscalation(nil), incident.Escalations...)
	withIdentity.Escalations[0].Evidence = append([]api.RouteMonitorEvidence(nil), incident.Escalations[0].Evidence...)
	withIdentity.Escalations[0].Evidence[0].CustomerID = "customer-secret"
	projected := ProjectIncident(withIdentity, false)
	if projected.Escalations[0].Evidence[0].CustomerID != "" {
		t.Fatal("escalation projection exposed a customer identity")
	}
	tampered := ProjectIncident(incident, false)
	tampered.Escalations[0].Evidence[0].Windows[0].Requests.MatchingRequests++
	if err := ValidateIncident(tampered, "demo"); err == nil {
		t.Fatal("tampered transition evidence was accepted")
	}
}

func TestAppendIncidentEscalationRetainsNewestBoundedTransitions(t *testing.T) {
	incident := api.RouteMonitorIncident{}
	opened := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	for index := 0; index < api.RouteMonitorIncidentEscalationMaxEntries+3; index++ {
		at := opened.Add(time.Duration(index+1) * time.Minute)
		escalation := api.RouteMonitorIncidentEscalation{TransitionID: time.Unix(int64(index+1), 0).UTC().Format(time.RFC3339), CheckedAt: at}
		if !AppendIncidentEscalation(&incident, escalation) {
			t.Fatalf("could not append increasing transition %d", index)
		}
	}
	if len(incident.Escalations) != api.RouteMonitorIncidentEscalationMaxEntries || !incident.EscalationsTruncated || !incident.Escalations[len(incident.Escalations)-1].CheckedAt.Equal(opened.Add(time.Duration(api.RouteMonitorIncidentEscalationMaxEntries+3)*time.Minute)) {
		t.Fatalf("escalation history did not retain its newest bounded transitions: %+v", incident)
	}
	duplicate := incident.Escalations[len(incident.Escalations)-1]
	if AppendIncidentEscalation(&incident, duplicate) {
		t.Fatal("duplicate escalation timestamp was appended")
	}
}
