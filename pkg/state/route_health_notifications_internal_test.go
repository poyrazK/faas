package state

// adr: 457

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func notificationEntry(status string) api.RouteHealthHistoryEntry {
	checked := time.Date(2026, 10, 2, 19, 0, 45, 0, time.UTC)
	anchor := checked.Add(-time.Hour)
	r := api.RouteHealthReport{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), StableDeploymentID: uuid.NewString(), Revision: 1, Mode: "enforce", Status: status, CheckedAt: checked, ObservationAnchor: &anchor}
	e := api.RouteHealthHistoryEntry{ID: uuid.NewString(), Report: r, Decision: routehealth.Decision(r), CheckedAt: checked, Source: "manual", Policy: routehealth.HistoryPolicy(), TrafficPercent: 1, RequestedTrafficPercent: 10}
	return e
}

func TestRouteHealthNotificationTransitionRules(t *testing.T) {
	for _, scenario := range []string{"first_unknown", "first_regressed", "first_healthy", "retry", "escalation", "unknown_after_regression", "resume", "report", "revision", "stage", "stable", "anchor", "policy", "no_recipients_baseline"} {
		t.Run(scenario, func(t *testing.T) {
			entry := notificationEntry("regressed")
			previous := routeHealthNotificationState{}
			if scenario != "first_unknown" && scenario != "first_regressed" && scenario != "first_healthy" {
				opened, err := routeHealthTransition(previous, entry, "demo")
				if err != nil {
					t.Fatal(err)
				}
				previous = opened.State
			}
			want := AppWebhookEvent("")
			switch scenario {
			case "first_unknown":
				entry.Report.Status = "unknown"
				want = AppWebhookEventRouteHealthBlocked
			case "first_regressed":
				want = AppWebhookEventRouteHealthBlocked
			case "first_healthy", "resume", "revision", "stage", "stable", "anchor", "policy":
				entry.Report.Status = "healthy"
			case "escalation":
				previous.Status = "blocked_unknown"
				want = AppWebhookEventRouteHealthBlocked
			case "unknown_after_regression":
				entry.Report.Status = "unknown"
			case "report":
				entry.Report.Mode = "report"
			}
			if scenario == "resume" {
				want = AppWebhookEventRouteHealthResumed
			}
			if scenario == "revision" {
				entry.Report.Revision++
			}
			if scenario == "stage" {
				entry.Report.CanaryStep++
			}
			if scenario == "stable" {
				entry.Report.StableDeploymentID = uuid.NewString()
			}
			if scenario == "anchor" {
				anchor := entry.Report.ObservationAnchor.Add(time.Second)
				entry.Report.ObservationAnchor = &anchor
			}
			if scenario == "policy" {
				entry.Policy.Version++
			}
			entry.Decision = routehealth.Decision(entry.Report)
			next, err := routeHealthTransition(previous, entry, "demo")
			if err != nil || next.Event != want {
				t.Fatalf("event %s want %s: %v", next.Event, want, err)
			}
			if scenario == "unknown_after_regression" && next.State.Status != "blocked_regressed" {
				t.Fatal("unknown erased a confirmed regression")
			}
			if want == "" && len(next.Payload) != 0 {
				t.Fatal("no-op produced payload")
			}
			if want != "" {
				var payload api.RouteHealthTransitionWebhookPayload
				if json.Unmarshal(next.Payload, &payload) != nil || payload.DecisionID != entry.ID || payload.HistoryPath != "/v1/apps/demo/route-health/deployments/"+entry.Report.DeploymentID+"/history/"+entry.ID {
					t.Fatal("notification lost exact evidence")
				}
				if want == AppWebhookEventRouteHealthResumed && payload.BlockedDecisionID != previous.BlockedDecisionID {
					t.Fatal("resume lost original hold")
				}
				if strings.Contains(string(next.Payload), "requests") || strings.Contains(string(next.Payload), "routes") {
					t.Fatal("private observations included")
				}
			}
		})
	}
}
