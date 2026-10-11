package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
)

func periodicFixture(t *testing.T) (*MemStore, string, time.Time) {
	t.Helper()
	m := NewMemStore()
	app, account, dep := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Minute).Add(api.ProfileAutoIngestionGrace)
	old := now.Add(-time.Hour)
	m.accounts[account] = Account{ID: account, Plan: api.PlanHobby, Status: AccountActive}
	m.apps[app] = App{ID: app, AccountID: account, Slug: "periodic", Runtime: "node24", Status: AppActive}
	m.deployments[dep] = Deployment{ID: dep, AppID: app, Scope: "prod", Status: DeployLive, RolloutState: "complete", RolloutCompletedAt: &old}
	options := api.DefaultProfileRegressionOptions()
	options.Routes = []string{"POST /checkout"}
	m.profileDeploymentPolicies = map[string]api.ProfileDeploymentPolicy{app: {AppID: app, Revision: 1, UpdatedAt: &old, Config: api.ProfileDeploymentPolicyConfig{Enabled: true, NotifyRouteRegressions: true, Runtime: "node24", WindowSeconds: 60, Options: options, Periodic: &api.PeriodicProfilePolicy{IntervalSeconds: 60, Confirmations: 2}}}}
	m.appWebhooks = map[string]AppWebhook{"hook": {ID: "hook", AppID: app, AccountID: account, Scope: AppWebhookScopeApp, Enabled: true}}
	if err := m.DiscoverProfilePeriodicMonitors(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	return m, app, now
}
func periodicAssessment(work ProfilePeriodicWork, at time.Time, status string) ProfilePeriodicResult {
	check := PeriodicProfileCheck(work.Monitor)
	a := profiling.NewRegressionAssessment(api.ProfileInvestigation{Revision: 1, Investigation: api.ProfileInvestigationInput{Baseline: *check.Baseline, Candidate: check.Candidate}}, check.Config.Options, at)
	if status == "inconclusive" {
		return ProfilePeriodicResult{Assessment: a}
	}
	count := int64(1000)
	percent, zero := 100.0, 0.0
	quality := &api.ProfileAttributionQuality{Available: true, TotalCPUSeconds: 1, AttributedCPUSeconds: 1, AttributedPercent: &percent, UnattributedPercent: &zero}
	a.Attribution = &api.ProfileAttributionComparison{Available: true, Baseline: quality, Candidate: quality, DeltaPercentagePoints: &zero, MaximumChangePercentagePoints: 20}
	coverage := &api.ProfileCoverage{Available: true, ReceivedProfiles: 10, WindowSeconds: 60, CoveredSeconds: 60}
	a.BaselineCoverage, a.CandidateCoverage = coverage, coverage
	labels := &api.ProfileRouteLabelCoverage{Available: true, Percent: &percent, LabeledRequests: &count, ObservedRequests: &count, CapturedProfiles: 10}
	metric := &api.ProfileRegressionCPUPerRequestMetric{BaselineCPUSecondsPerRequest: 0.001, CandidateCPUSecondsPerRequest: 0.001, RelativeIncreasePercent: &zero}
	if status == "regressed" {
		change := 900.0
		metric.CandidateCPUSecondsPerRequest = 0.01
		metric.DeltaCPUSecondsPerRequest = 0.009
		metric.RelativeIncreasePercent = &change
		metric.ExceedsThreshold = true
	}
	a.Status = status
	a.RouteChecks = []api.ProfileRouteRegression{{Route: work.Monitor.Route, Status: status, BaselineRequests: &count, CandidateRequests: &count, Metric: metric, LabelCoverage: &api.ProfileRouteLabelComparison{Available: true, Consistent: true, Baseline: labels, Candidate: labels, DeltaPercentagePoints: &zero, MinimumPercent: 80, MaximumChangePercentagePoints: 20}}}
	return ProfilePeriodicResult{Assessment: a}
}
func TestProfilePeriodicBaselineConfirmationAndRecovery(t *testing.T) {
	m, app, at := periodicFixture(t)
	apply := func(status string) api.ProfilePeriodicMonitor {
		t.Helper()
		work, err := m.ClaimProfilePeriodicMonitor(t.Context(), at)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.FinishProfilePeriodicMonitor(t.Context(), work, periodicAssessment(work, at, status), at); err != nil {
			t.Fatal(err)
		}
		rows, err := m.ListProfilePeriodicMonitors(t.Context(), work.AccountID, app)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		at = rows[0].NextAttemptAt
		return rows[0]
	}
	first := apply("no_regression_detected")
	if first.Baseline == nil || first.History[0].Status != "baseline_pinned" {
		t.Fatal("baseline not pinned")
	}
	baseline := *first.Baseline
	apply("regressed")
	if len(m.appWebhookEventOutbox) != 0 {
		t.Fatal("single observation alerted")
	}
	apply("inconclusive")
	apply("regressed")
	if len(m.appWebhookEventOutbox) != 0 {
		t.Fatal("unknown evidence did not interrupt confirmation")
	}
	opened := apply("regressed")
	if len(m.appWebhookEventOutbox) != 1 || opened.History[0].Transition != "profile.route_regressed" || opened.History[0].InvestigationID == "" {
		t.Fatal("missing confirmed incident and evidence")
	}
	incident := opened.History[0].IncidentID
	if !periodicQueryEqual(*opened.Baseline, baseline) {
		t.Fatal("baseline drifted")
	}
	apply("regressed")
	apply("no_regression_detected")
	if len(m.appWebhookEventOutbox) != 1 {
		t.Fatal("duplicate or premature recovery")
	}
	recovered := apply("no_regression_detected")
	if len(m.appWebhookEventOutbox) != 2 || recovered.History[0].Transition != "profile.route_recovered" || recovered.History[0].IncidentID != incident {
		t.Fatal("missing matching confirmed recovery")
	}
	apply("regressed")
	reopened := apply("regressed")
	if len(m.appWebhookEventOutbox) != 3 || reopened.History[0].IncidentID == incident {
		t.Fatal("new confirmed episode did not get its own incident")
	}
	// Baseline expiry keeps the old incident context intact and restarts calibration.
	work, err := m.ClaimProfilePeriodicMonitor(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	r := periodicAssessment(work, at, "inconclusive")
	r.BaselineExpired = true
	if err := m.FinishProfilePeriodicMonitor(t.Context(), work, r, at); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.ListProfilePeriodicMonitors(t.Context(), work.AccountID, app)
	if rows[0].Baseline != nil || rows[0].History[0].Status != "baseline_expired" || len(m.appWebhookEventOutbox) != 3 {
		t.Fatal("expiry falsely recovered or retained expired baseline")
	}
	at = rows[0].NextAttemptAt
	repinned := apply("no_regression_detected")
	if repinned.Baseline == nil || periodicQueryEqual(*repinned.Baseline, baseline) {
		t.Fatal("expired reference was reused")
	}
	apply("no_regression_detected")
	apply("no_regression_detected")
	if len(m.appWebhookEventOutbox) != 3 {
		t.Fatal("new baseline recovered an old incident")
	}
}
func TestProfilePeriodicLeasesRetriesAndPolicyChanges(t *testing.T) {
	m, app, at := periodicFixture(t)
	first, err := m.ClaimProfilePeriodicMonitor(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimProfilePeriodicMonitor(t.Context(), at); !errors.Is(err, ErrNotFound) {
		t.Fatal("double claim", err)
	}
	later := at.Add(api.ProfileAutoLeaseDuration + time.Second)
	reclaimed, err := m.ClaimProfilePeriodicMonitor(t.Context(), later)
	if err != nil {
		t.Fatal(err)
	}
	if !periodicQueryEqual(first.Monitor.Candidate, reclaimed.Monitor.Candidate) {
		t.Fatal("lease recovery changed windows")
	}
	if err := m.FinishProfilePeriodicMonitor(t.Context(), first, periodicAssessment(first, later, "no_regression_detected"), later); !errors.Is(err, ErrProfileCheckLease) {
		t.Fatal("stale owner committed", err)
	}
	retry := periodicAssessment(reclaimed, later, "inconclusive")
	retry.Retry = true
	if err := m.FinishProfilePeriodicMonitor(t.Context(), reclaimed, retry, later); err != nil {
		t.Fatal(err)
	}
	next, err := m.ClaimProfilePeriodicMonitor(t.Context(), later.Add(api.ProfileAutoRetryInterval))
	if err != nil {
		t.Fatal(err)
	}
	if !periodicQueryEqual(next.Monitor.Candidate, first.Monitor.Candidate) {
		t.Fatal("retry changed windows")
	}
	malformed := periodicAssessment(next, later, "inconclusive")
	malformed.Assessment.Candidate.DeploymentID = uuid.NewString()
	if err := m.FinishProfilePeriodicMonitor(t.Context(), next, malformed, later.Add(api.ProfileAutoRetryInterval)); err == nil {
		t.Fatal("foreign comparison accepted")
	}
	p := m.profileDeploymentPolicies[app]
	p.Revision++
	m.profileDeploymentPolicies[app] = p
	if err := m.FinishProfilePeriodicMonitor(t.Context(), next, periodicAssessment(next, later, "inconclusive"), later.Add(api.ProfileAutoRetryInterval)); !errors.Is(err, ErrProfileCheckLease) {
		t.Fatal("changed policy committed", err)
	}
	if len(m.appWebhookEventOutbox) != 0 {
		t.Fatal("invalidated work alerted")
	}
}
func TestProfilePeriodicPolicyBoundsAndWorkContract(t *testing.T) {
	m, app, _ := periodicFixture(t)
	config := m.profileDeploymentPolicies[app].Config
	zero := int64(0)
	for _, interval := range []int{0, 59, 61, 86460} {
		c := config
		c.Periodic = &api.PeriodicProfilePolicy{IntervalSeconds: interval, Confirmations: 2}
		if ValidateProfileDeploymentPolicy(api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: c}) == nil {
			t.Fatal("bad interval accepted", interval)
		}
	}
	var work ProfilePeriodicWork
	if err := json.Unmarshal([]byte(`{"monitor":{"id":"test"},"account_id":"owned"}`), &work); err != nil || work.AccountID != "owned" {
		t.Fatal("SQL work contract lost ownership", err, work)
	}
}
