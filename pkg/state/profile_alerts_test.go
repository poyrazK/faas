package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfileRouteAlertLifecycle(t *testing.T) {
	at := time.Now().UTC()
	requests := int64(1000)
	percent := float64(95)
	relative := float64(100)
	options := api.DefaultProfileRegressionOptions()
	options.Routes = []string{"POST /checkout"}
	coverage := &api.ProfileCoverage{Available: true, ReceivedProfiles: options.MinimumProfiles, CoveredSeconds: 60, WindowSeconds: 60}
	labels := &api.ProfileRouteLabelCoverage{Available: true, Percent: &percent}
	route := api.ProfileRouteRegression{Route: options.Routes[0], Status: "regressed", BaselineRequests: &requests, CandidateRequests: &requests, LabelCoverage: &api.ProfileRouteLabelComparison{Available: true, Consistent: true, Baseline: labels, Candidate: labels}, Metric: &api.ProfileRegressionCPUPerRequestMetric{BaselineCPUSecondsPerRequest: 0.001, CandidateCPUSecondsPerRequest: 0.002, DeltaCPUSecondsPerRequest: 0.001, RelativeIncreasePercent: &relative, ExceedsThreshold: true}}
	o := state.ProfileAlertObservation{AppID: "app", AccountID: "account", Slug: "demo", Revision: 1, Source: "canary", Config: api.ProfileDeploymentPolicyConfig{Enabled: true, NotifyRouteRegressions: true, Options: options}, Assessment: api.ProfileRegressionAssessment{Baseline: api.ProfileQuery{DeploymentID: "baseline", End: at.Add(-time.Hour)}, Candidate: api.ProfileQuery{DeploymentID: "candidate", End: at}, CheckedAt: at, Attribution: &api.ProfileAttributionComparison{Available: true}, BaselineCoverage: coverage, CandidateCoverage: coverage, RouteChecks: []api.ProfileRouteRegression{route}}}
	m := state.NewMemStore()
	(*m.Apps())[o.AppID] = state.App{ID: o.AppID, AccountID: o.AccountID, Slug: o.Slug, Status: state.AppActive}
	(*m.ProfileDeploymentPolicies()) = map[string]api.ProfileDeploymentPolicy{o.AppID: {Revision: o.Revision, Config: o.Config}}
	(*m.AppWebhooks()) = map[string]state.AppWebhook{"hook": {ID: "hook", AppID: o.AppID, AccountID: o.AccountID, Scope: state.AppWebhookScopeApp, Enabled: true, EventFilter: []string{"profile.route_regressed", "profile.route_recovered"}}}
	apply := func() []state.ProfileAlertPlan {
		t.Helper()
		plans, err := m.PrepareProfileAlertsLocked(o)
		if err != nil {
			t.Fatal(err)
		}
		m.PublishProfileAlertsLocked(o, plans)
		return plans
	}
	first := apply()
	if len(first) != 1 || first[0].Event != state.AppWebhookEventProfileRouteRegressed || len((*m.AppWebhookEventOutbox())) != 1 {
		t.Fatal("missing initial regression")
	}
	var payload api.ProfileRouteAlertPayload
	if err := json.Unmarshal(first[0].Payload, &payload); err != nil || payload.IncidentID == "" || payload.RouteCheck.Route != route.Route {
		t.Fatal(payload, err)
	}
	apply()
	o.Assessment.Candidate.End = at.Add(time.Minute)
	o.Assessment.CheckedAt = at.Add(time.Minute)
	apply()
	if len((*m.AppWebhookEventOutbox())) != 1 {
		t.Fatal("duplicate regression event")
	}
	o.Assessment.Candidate.End = at.Add(2 * time.Minute)
	o.Assessment.CheckedAt = at.Add(2 * time.Minute)
	o.Assessment.RouteChecks[0].Status = "no_regression_detected"
	o.Assessment.RouteChecks[0].Metric.ExceedsThreshold = false
	o.Assessment.RouteChecks[0].LabelCoverage = nil
	apply()
	if len((*m.AppWebhookEventOutbox())) != 1 {
		t.Fatal("missing evidence falsely recovered")
	}
	o.Assessment.RouteChecks[0].LabelCoverage = &api.ProfileRouteLabelComparison{Available: true, Consistent: true, Baseline: labels, Candidate: labels}
	o.Assessment.Candidate.End = at.Add(time.Minute)
	apply()
	if len((*m.AppWebhookEventOutbox())) != 1 {
		t.Fatal("stale result falsely recovered")
	}
	o.Assessment.Candidate.End = at.Add(3 * time.Minute)
	o.Assessment.CheckedAt = at.Add(3 * time.Minute)
	recovered := apply()
	if recovered[0].Event != state.AppWebhookEventProfileRouteRecovered || recovered[0].State.IncidentID != payload.IncidentID || len((*m.AppWebhookEventOutbox())) != 2 {
		t.Fatal("missing matching recovery")
	}
	apply()
	if len((*m.AppWebhookEventOutbox())) != 2 {
		t.Fatal("duplicate recovery")
	}
	changed := o
	changed.Revision++
	if state.ProfileAlertKey(changed, route.Route) == first[0].Key {
		t.Fatal("policy contexts merged")
	}
	changed = o
	changed.Assessment.Baseline.DeploymentID = "different"
	if state.ProfileAlertKey(changed, route.Route) == first[0].Key {
		t.Fatal("baseline contexts merged")
	}
	p := (*m.ProfileDeploymentPolicies())[o.AppID]
	p.Config.NotifyRouteRegressions = false
	(*m.ProfileDeploymentPolicies())[o.AppID] = p
	if plans := apply(); len(plans) != 0 {
		t.Fatal("disabled policy emitted plans")
	}
}
