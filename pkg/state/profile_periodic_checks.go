package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

type ProfilePeriodicWork struct {
	Monitor   api.ProfilePeriodicMonitor `json:"monitor"`
	AccountID string                     `json:"account_id"`
	Token     string                     `json:"-"`
}
type ProfilePeriodicResult struct {
	Assessment             api.ProfileRegressionAssessment
	Retry, BaselineExpired bool
}
type ProfilePeriodicStore interface {
	DiscoverProfilePeriodicMonitors(context.Context, time.Time) error
	ClaimProfilePeriodicMonitor(context.Context, time.Time) (ProfilePeriodicWork, error)
	FinishProfilePeriodicMonitor(context.Context, ProfilePeriodicWork, ProfilePeriodicResult, time.Time) error
	ListProfilePeriodicMonitors(context.Context, string, string) ([]api.ProfilePeriodicMonitor, error)
}

var _ ProfilePeriodicStore = (*MemStore)(nil)
var _ ProfilePeriodicStore = (*PgStore)(nil)

func validatePeriodicProfilePolicy(c api.ProfileDeploymentPolicyConfig) error {
	p := c.Periodic
	if p == nil {
		return nil
	}
	if !c.Enabled || len(c.Options.Routes) == 0 {
		return errors.New("periodic profiling requires enabled checks and explicit advisory routes")
	}
	if p.IntervalSeconds < api.ProfilePeriodicMinIntervalSeconds || p.IntervalSeconds > api.ProfilePeriodicMaxIntervalSeconds || p.IntervalSeconds < c.WindowSeconds || p.IntervalSeconds%api.ProfilePeriodicMinIntervalSeconds != 0 {
		return errors.New("periodic interval must be a whole number of minutes, at least the capture window, and at most one day")
	}
	if p.Confirmations < 1 || p.Confirmations > api.ProfilePeriodicMaxConfirmations {
		return errors.New("periodic confirmations must be between 1 and 5")
	}
	return nil
}
func newPeriodicMonitor(d Deployment, p api.ProfileDeploymentPolicy, route string, now time.Time) api.ProfilePeriodicMonitor {
	interval := time.Duration(p.Config.Periodic.IntervalSeconds) * time.Second
	window := time.Duration(p.Config.WindowSeconds) * time.Second
	start := d.RolloutCompletedAt.Add(time.Duration(p.Config.WarmupSeconds) * time.Second)
	if p.UpdatedAt != nil && p.UpdatedAt.After(start) {
		start = *p.UpdatedAt
	}
	end := now.Add(-api.ProfileAutoIngestionGrace).UTC().Truncate(interval)
	for end.Add(-window).Before(start) {
		end = end.Add(interval)
	}
	return api.ProfilePeriodicMonitor{Active: true, ID: uuid.NewString(), AppID: d.AppID, DeploymentID: d.ID, Scope: normalizedDeploymentScope(d.Scope), Route: route, PolicyRevision: p.Revision, Config: p.Config, Candidate: api.ProfileQuery{DeploymentID: d.ID, Runtime: p.Config.Runtime, Start: end.Add(-window), End: end}, NextAttemptAt: end.Add(api.ProfileAutoIngestionGrace), History: []api.ProfilePeriodicObservation{}}
}
func PeriodicProfileCheck(m api.ProfilePeriodicMonitor) api.ProfileDeploymentCheck {
	c := m.Config
	c.Options.Routes = []string{m.Route}
	baseline := m.Baseline
	if baseline == nil {
		q := m.Candidate
		baseline = &q
	}
	return api.ProfileDeploymentCheck{AppID: m.AppID, DeploymentID: m.DeploymentID, Scope: m.Scope, Config: c, Baseline: baseline, Candidate: m.Candidate, Attempts: m.Attempts}
}
func periodicQueryEqual(a, b api.ProfileQuery) bool {
	return a.DeploymentID == b.DeploymentID && a.Runtime == b.Runtime && a.Route == b.Route && a.Start.Equal(b.Start) && a.End.Equal(b.End)
}
func validatePeriodicResult(m api.ProfilePeriodicMonitor, r ProfilePeriodicResult) error {
	if err := validateProfileCanaryAssessment(r.Assessment); err != nil {
		return err
	}
	if len(r.Assessment.RouteChecks) > 1 || len(r.Assessment.RouteChecks) == 1 && r.Assessment.RouteChecks[0].Route != m.Route {
		return errors.New("periodic result has unexpected routes")
	}
	expected := PeriodicProfileCheck(m)
	expectedOptions, _ := json.Marshal(api.NormalizeProfileRegressionOptions(expected.Config.Options))
	actualOptions, _ := json.Marshal(api.NormalizeProfileRegressionOptions(r.Assessment.Options))
	if !bytes.Equal(expectedOptions, actualOptions) || r.Assessment.CheckedAt.IsZero() {
		return errors.New("periodic result does not match its pinned policy")
	}
	if r.BaselineExpired && (m.Baseline == nil || r.Retry || r.Assessment.Status != "inconclusive") {
		return errors.New("invalid periodic baseline expiry")
	}
	if !periodicQueryEqual(r.Assessment.Baseline, *expected.Baseline) || !periodicQueryEqual(r.Assessment.Candidate, expected.Candidate) {
		return errors.New("periodic result does not match its pinned windows")
	}
	return nil
}
func periodicObservation(m api.ProfilePeriodicMonitor, a api.ProfileRegressionAssessment, slug, account string) profileAlertObservation {
	if len(a.RouteChecks) == 0 {
		a.RouteChecks = []api.ProfileRouteRegression{{Route: m.Route, Status: "insufficient_data", Reason: a.Reason}}
	}
	return profileAlertObservation{AppID: m.AppID, AccountID: account, Slug: slug, Scope: m.Scope, Source: "periodic", Revision: m.PolicyRevision, Config: m.Config, Assessment: a, Confirmations: m.Config.Periodic.Confirmations, EvidencePath: "/v1/apps/" + slug + "/profiles/periodic-monitors"}
}
func advancePeriodicMonitor(m api.ProfilePeriodicMonitor, r ProfilePeriodicResult, plans []profileAlertPlan, id string, now time.Time) api.ProfilePeriodicMonitor {
	a := r.Assessment
	if r.Retry && m.Attempts < api.ProfileAutoMaxAttempts {
		m.NextAttemptAt = now.Add(api.ProfileAutoRetryInterval)
		return m
	}
	observation := api.ProfilePeriodicObservation{ID: uuid.NewString(), Status: a.Status, Reason: safetext.Truncate(a.Reason, api.ProfileAlertMaxSymbolBytes), CheckedAt: a.CheckedAt, Baseline: a.Baseline, Candidate: a.Candidate, InvestigationID: id}
	for _, row := range a.RouteChecks {
		if row.Route == m.Route {
			q := row
			observation.RouteCheck = &q
			observation.Status = row.Status
			if row.Status == "insufficient_data" {
				observation.Status = "inconclusive"
			}
			observation.Reason = safetext.Truncate(row.Reason, api.ProfileAlertMaxSymbolBytes)
			break
		}
	}
	o := periodicObservation(m, a, "", "")
	if r.BaselineExpired {
		m.Baseline = nil
		observation.Status = "baseline_expired"
	} else if m.Baseline == nil && observation.RouteCheck != nil && observation.RouteCheck.Status == "no_regression_detected" && profileAlertEligible(o, *observation.RouteCheck) {
		baseline := m.Candidate
		m.Baseline = &baseline
		observation.Status = "baseline_pinned"
	}
	for _, plan := range plans {
		observation.IncidentID = plan.State.IncidentID
		observation.Transition = string(plan.Event)
	}
	history := []api.ProfilePeriodicObservation{observation}
	for _, row := range m.History {
		if len(history) >= api.ProfilePeriodicMaxHistory {
			break
		}
		if row.CheckedAt.After(now.Add(-api.ProfileAutoReceiptRetention)) {
			history = append(history, row)
		}
	}
	m.History = history
	interval := time.Duration(m.Config.Periodic.IntervalSeconds) * time.Second
	end := m.Candidate.End.Add(interval)
	latest := now.Add(-api.ProfileAutoIngestionGrace).UTC().Truncate(interval)
	if latest.After(end) {
		end = latest
	}
	m.Candidate.Start, m.Candidate.End = end.Add(-time.Duration(m.Config.WindowSeconds)*time.Second), end
	m.NextAttemptAt = end.Add(api.ProfileAutoIngestionGrace)
	m.Attempts = 0
	return m
}
func encodePeriodicMonitor(m api.ProfilePeriodicMonitor) ([]byte, error) {
	body, err := json.Marshal(m)
	if err == nil && len(body) > api.ProfilePeriodicMaxDataBytes {
		err = errors.New("periodic monitor exceeds its storage bound")
	}
	return body, err
}

func trimPeriodicHistory(m api.ProfilePeriodicMonitor, now time.Time) api.ProfilePeriodicMonitor {
	history := []api.ProfilePeriodicObservation{}
	for _, h := range m.History {
		if len(history) >= api.ProfilePeriodicMaxHistory {
			break
		}
		if !h.CheckedAt.Before(now.Add(-api.ProfileAutoReceiptRetention)) {
			history = append(history, h)
		}
	}
	m.History = history
	return m
}
