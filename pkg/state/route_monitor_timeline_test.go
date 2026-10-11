package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRouteMonitorHealthyBaselineSanitizesAndRoundTrips(t *testing.T) {
	baseline := routeMonitorDeploymentBaseline(sqlc.RouteMonitorServingDeploymentsRow{
		ID:         "66666666-6666-4666-8666-666666666666",
		CommitSha:  pgtype.Text{String: strings.Repeat("A", 40), Valid: true},
		SourceUrl:  pgtype.Text{String: "https://credential:secret@github.com/team/service.git", Valid: true},
		SourceRoot: pgtype.Text{String: "internal/api", Valid: true},
	})
	if baseline.DeploymentID == "" || baseline.CommitSHA != strings.Repeat("a", 40) || baseline.Repository != "github.com/team/service" || baseline.SourceRoot != "internal/api" {
		t.Fatalf("baseline did not retain normalized identity: %+v", baseline)
	}
	body, err := encodeRouteMonitorDeploymentBaseline(baseline)
	if err != nil || strings.Contains(string(body), "credential") || strings.Contains(string(body), "secret") {
		t.Fatalf("baseline retained raw source credentials: %s (%v)", body, err)
	}
	decoded, err := decodeRouteMonitorDeploymentBaseline(string(body))
	if err != nil || decoded == nil || *decoded != *baseline {
		t.Fatalf("baseline did not round trip: %+v %v", decoded, err)
	}
	if empty, err := decodeRouteMonitorDeploymentBaseline(`{}`); err != nil || empty != nil {
		t.Fatalf("empty baseline should remain absent: %+v %v", empty, err)
	}
	conflicting := routeMonitorDeploymentBaseline(sqlc.RouteMonitorServingDeploymentsRow{
		ID:         baseline.DeploymentID,
		CommitSha:  pgtype.Text{String: strings.Repeat("a", 40), Valid: true},
		SourceUrl:  pgtype.Text{String: "github://team/service@" + strings.Repeat("b", 40), Valid: true},
		SourceRoot: pgtype.Text{String: ".", Valid: true},
	})
	if conflicting.Repository != "" {
		t.Fatalf("baseline retained a repository with a conflicting pinned reference: %+v", conflicting)
	}
	invalidRoot := routeMonitorDeploymentBaseline(sqlc.RouteMonitorServingDeploymentsRow{
		ID:         baseline.DeploymentID,
		CommitSha:  pgtype.Text{String: strings.Repeat("a", 40), Valid: true},
		SourceUrl:  pgtype.Text{String: "https://github.com/team/service", Valid: true},
		SourceRoot: pgtype.Text{String: " internal/api ", Valid: true},
	})
	if invalidRoot.SourceRoot != "" {
		t.Fatalf("baseline canonicalized source metadata that deployment binding would reject: %+v", invalidRoot)
	}
}

func TestEncodeRouteMonitorIncidentTrimsTimelineToFitEvidenceLimit(t *testing.T) {
	opened := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	incident := api.RouteMonitorIncident{Timeline: make([]api.RouteMonitorIncidentTimelineEntry, api.RouteMonitorIncidentTimelineMaxEntries)}
	for index := range incident.Timeline {
		incident.Timeline[index] = api.RouteMonitorIncidentTimelineEntry{
			CheckedAt: opened.Add(time.Duration(index) * time.Minute),
			Coverage:  "observed_only",
			Status:    "unknown",
			Reason:    strings.Repeat("x", 12*1024),
			Routes:    []api.RouteMonitorIncidentTimelineRoute{},
		}
	}
	body, err := encodeRouteMonitorIncident(&incident)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > api.RouteMonitorIncidentMaxBytes || len(incident.Timeline) < 2 || len(incident.Timeline) >= api.RouteMonitorIncidentTimelineMaxEntries || !incident.TimelineTruncated {
		t.Fatalf("timeline did not respect incident byte ceiling: bytes=%d entries=%d truncated=%t", len(body), len(incident.Timeline), incident.TimelineTruncated)
	}
	var stored api.RouteMonitorIncident
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	latest := stored.Timeline[len(stored.Timeline)-1].CheckedAt
	if !stored.Timeline[0].CheckedAt.Equal(opened) || !latest.Equal(opened.Add(time.Duration(api.RouteMonitorIncidentTimelineMaxEntries-1)*time.Minute)) {
		t.Fatalf("byte trimming discarded the opening or latest observation: first=%s latest=%s", stored.Timeline[0].CheckedAt, latest)
	}
}

func TestEncodeRouteMonitorIncidentPreservesNewestEscalationWithinByteLimit(t *testing.T) {
	opened := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	incident := api.RouteMonitorIncident{Escalations: make([]api.RouteMonitorIncidentEscalation, 3)}
	for index := range incident.Escalations {
		evidence := make([]api.RouteMonitorEvidence, 3)
		for evidenceIndex := range evidence {
			evidence[evidenceIndex] = api.RouteMonitorEvidence{Method: "POST", Path: strings.Repeat("x", 250*1024), Signal: "latency"}
		}
		incident.Escalations[index] = api.RouteMonitorIncidentEscalation{
			TransitionID: time.Unix(int64(index+1), 0).UTC().Format(time.RFC3339),
			CheckedAt:    opened.Add(time.Duration(index+1) * time.Minute),
			Evidence:     evidence,
		}
	}
	body, err := encodeRouteMonitorIncident(&incident)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > api.RouteMonitorIncidentMaxBytes || len(incident.Escalations) != 1 || !incident.EscalationsTruncated || !incident.Escalations[0].EvidenceTruncated || len(incident.Escalations[0].Evidence) != 2 || !incident.Escalations[0].CheckedAt.Equal(opened.Add(3*time.Minute)) {
		t.Fatalf("size compaction did not preserve newest escalation metadata: bytes=%d entries=%d truncated=%t evidence=%d evidence_truncated=%t", len(body), len(incident.Escalations), incident.EscalationsTruncated, len(incident.Escalations[0].Evidence), incident.Escalations[0].EvidenceTruncated)
	}
	var stored api.RouteMonitorIncident
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Escalations) != 1 || stored.Escalations[0].TransitionID != incident.Escalations[0].TransitionID {
		t.Fatalf("encoded incident lost newest escalation: %+v", stored.Escalations)
	}
}

func TestRouteMonitorEscalationNotificationIsStableAndRedacted(t *testing.T) {
	previous := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	checkedAt := previous.Add(time.Minute)
	incident := api.RouteMonitorIncident{ID: "incident-id", AppID: "app-id", DeploymentID: "deployment-id", Revision: 7, Status: "open"}
	report := api.RouteMonitorReport{
		CheckedAt: checkedAt,
		Customers: &api.RouteMonitorCustomerReport{GroupBy: "tenant", Coverage: "observed_only", ObservedCustomers: 4, ViolatedCustomers: 2, UnknownCustomers: 1},
	}
	escalation := &api.RouteMonitorWebhookEscalation{PreviousCheckedAt: previous, NewlyViolatedRoutes: 1, NewlyViolatedSignals: 2}
	event, transitionID, body, err := routeMonitorEscalationNotification(incident, "demo", report, escalation)
	if err != nil {
		t.Fatal(err)
	}
	duplicateEvent, duplicateID, duplicateBody, err := routeMonitorEscalationNotification(incident, "demo", report, escalation)
	if err != nil {
		t.Fatal(err)
	}
	if event != AppWebhookEventRouteMonitorEscalated || duplicateEvent != event || transitionID == "" || transitionID != duplicateID || string(body) != string(duplicateBody) {
		t.Fatalf("transition identity is not stable: event=%q duplicate=%q id=%q duplicate_id=%q", event, duplicateEvent, transitionID, duplicateID)
	}
	var payload api.RouteMonitorWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TransitionID != transitionID || payload.Escalation == nil || payload.Escalation.NewlyViolatedRoutes != 1 || payload.Escalation.NewlyViolatedSignals != 2 || payload.CustomerImpact == nil || payload.CustomerImpact.UnknownCustomers != 1 || strings.Contains(string(body), "customer_id") {
		t.Fatalf("escalation payload lost aggregate context or exposed identities: %s", body)
	}
}
