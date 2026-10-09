package state

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func profileGateFixture(t *testing.T) (*MemStore, Deployment, string, time.Time) {
	t.Helper()
	m := NewMemStore()
	appID, accountID, stableID, candidateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	anchor := time.Now().UTC().Add(-10 * time.Minute)
	m.accounts[accountID] = Account{ID: accountID, Plan: api.PlanPro, Status: AccountActive}
	m.apps[appID] = App{ID: appID, AccountID: accountID, Slug: "gated", Runtime: "node24", Status: AppActive, Manifest: AppManifest{Profiling: &api.ProfilingConfig{Enabled: true}}}
	stable := Deployment{ID: stableID, AppID: appID, Scope: "prod", Status: DeployLive, TrafficPercent: 90, RolloutState: "complete", CreatedAt: anchor.Add(-time.Hour)}
	d := Deployment{ID: candidateID, AppID: appID, Scope: "prod", Status: DeployLive, TrafficPercent: 10, RolloutState: "rolling_out", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, CanaryStepStartedAt: &anchor, CreatedAt: anchor}
	m.deployments[stableID] = stable
	m.deployments[candidateID] = d
	options := api.DefaultProfileRegressionOptions()
	options.Routes = []string{"POST /checkout"}
	m.profileDeploymentPolicies = map[string]api.ProfileDeploymentPolicy{appID: {AppID: appID, Revision: 1, Config: api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: 60, Options: options, CanaryGate: &api.ProfileCanaryGatePolicy{Confirmations: 2, TimeoutSeconds: 3600, OnTimeout: "hold"}}}}
	if count, err := m.DiscoverProfileCanaryChecks(t.Context(), anchor); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	return m, d, accountID, anchor.Add(90 * time.Second)
}

func gateAssessment(work ProfileCanaryCheckWork, at time.Time, status string) api.ProfileRegressionAssessment {
	config := api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: work.Check.Candidate.Runtime, WindowSeconds: work.Check.WindowSeconds, Options: work.Check.Options}
	monitor := api.ProfilePeriodicMonitor{Route: work.Check.Options.Routes[0], Config: config, Baseline: work.Check.Baseline, Candidate: *work.Check.Candidate}
	a := periodicAssessment(ProfilePeriodicWork{Monitor: monitor}, at, status).Assessment
	if a.BaselineCoverage != nil {
		a.BaselineCoverage.ContributingCollectors = 1
		a.CandidateCoverage.ContributingCollectors = 1
	}
	return a
}

func applyGateWindow(t *testing.T, m *MemStore, at *time.Time, status string) (api.CanaryProfileSignal, ProfileCanaryCheckWork) {
	t.Helper()
	work, err := m.ClaimProfileCanaryCheck(t.Context(), *at)
	if err != nil {
		t.Fatal(err)
	}
	a := gateAssessment(work, *at, status)
	out, err := m.FinishProfileCanaryCheck(t.Context(), work, a, false, *at)
	if err != nil {
		t.Fatal(err)
	}
	if out.NextAttemptAt != nil {
		*at = *out.NextAttemptAt
	}
	return out, work
}

func TestProfileCanaryGateDistinctWindowsAndUnknownReset(t *testing.T) {
	m, d, acct, at := profileGateFixture(t)
	out, first := applyGateWindow(t, m, &at, "regressed")
	if out.Gate.Status != "collecting" || out.Gate.Streaks[0].Count != 1 || out.Gate.NextCandidate == nil || out.CompletedAt != nil {
		t.Fatalf("first window %+v", out)
	}
	if !out.Candidate.End.Equal(out.Gate.LastWindowEnd.UTC()) || !out.Gate.NextCandidate.Start.Equal(out.Candidate.End) {
		t.Fatal("retained evidence queries shifted")
	}
	if _, err := m.FinishProfileCanaryCheck(t.Context(), first, gateAssessment(first, at, "regressed"), false, at); !errors.Is(err, ErrProfileCheckLease) {
		t.Fatal("replay accepted", err)
	}
	out, _ = applyGateWindow(t, m, &at, "inconclusive")
	if out.Gate.Streaks[0].Count != 0 {
		t.Fatal("unknown preserved confirmation", out.Gate)
	}
	out, _ = applyGateWindow(t, m, &at, "regressed")
	if out.Gate.Status != "collecting" || out.Gate.Streaks[0].Count != 1 {
		t.Fatal(out.Gate)
	}
	out, _ = applyGateWindow(t, m, &at, "regressed")
	if out.Gate.Status != "regressed" || out.CompletedAt == nil || out.Gate.Streaks[0].Count != 2 {
		t.Fatal(out.Gate)
	}
	decision, err := m.ReadProfileCanaryGate(t.Context(), acct, d.AppID, d.ID)
	if err != nil || decision.Status != "regressed" || decision.Signal == nil {
		t.Fatal(decision, err)
	}
	p := m.profileDeploymentPolicies[d.AppID]
	p.Revision++
	m.profileDeploymentPolicies[d.AppID] = p
	decision, err = m.ReadProfileCanaryGate(t.Context(), acct, d.AppID, d.ID)
	if err != nil || decision.Status != "collecting" || decision.Signal != nil {
		t.Fatal("old revision qualified", decision, err)
	}
}

func TestProfileCanaryGateAdvanceOverrideAndRollback(t *testing.T) {
	for _, mode := range []string{"passed", "held", "override", "rollback", "service"} {
		t.Run(mode, func(t *testing.T) {
			m, d, acct, at := profileGateFixture(t)
			status := "regressed"
			if mode == "passed" {
				status = "no_regression_detected"
			}
			if mode == "rollback" || mode == "service" {
				p := m.profileDeploymentPolicies[d.AppID]
				p.Config.CanaryGate.AutoRollback = true
				m.profileDeploymentPolicies[d.AppID] = p
			}
			if mode == "service" {
				app := m.apps[d.AppID]
				app.Manifest.ExecutionMode = api.ExecutionModeService
				m.apps[d.AppID] = app
			}
			applyGateWindow(t, m, &at, status)
			applyGateWindow(t, m, &at, status)
			decision := api.ProfileCanaryGateDecision{}
			params := CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 50, ProfileGateDecision: &decision, Audit: DeploymentAudit{Kind: DeployTrafficChanged, Actor: "account:" + acct}}
			if mode == "override" {
				params.ProfileGateOverride = &api.ProfileGateOverride{ExpectedPolicyRevision: 1, Reason: "Investigated workload change and accepted the CPU cost."}
			}
			if mode == "rollback" {
				params.ProfileGateRollback = true
				params.RequireSafeReleaseLease = true
				m.safeReleaseWorkerLeaseUntil = time.Now().Add(time.Minute)
			}
			got, auditID, err := m.AdvanceCanary(t.Context(), d.ID, params)
			if mode == "held" || mode == "service" {
				var blocked *ProfileGateBlockedError
				if !errors.As(err, &blocked) || m.deployments[d.ID].TrafficPercent != 10 {
					t.Fatal("gate bypass", got, err)
				}
				return
			}
			if err != nil || auditID == 0 {
				t.Fatal(got, err)
			}
			if mode == "rollback" {
				if got.RolloutState != "aborted" || got.TrafficPercent != 0 || decision.Status != "rolled_back" || m.deployments[decision.StableDeploymentID].TrafficPercent != 100 {
					t.Fatal("rollback not atomic", got, decision)
				}
			} else if got.CanaryStep != 1 || got.TrafficPercent != 50 {
				t.Fatal("advance failed", got)
			}
			audits, _ := m.ListDeploymentAudit(t.Context(), d.ID, 10)
			var data map[string]json.RawMessage
			if len(audits) != 1 || json.Unmarshal(audits[0].Data, &data) != nil || len(data["profile_gate"]) == 0 {
				t.Fatal("missing gate audit", audits)
			}
			if mode == "override" && decision.Status != "overridden" {
				t.Fatal(decision)
			}
		})
	}
}

func TestProfileCanaryGateTimeoutAndFences(t *testing.T) {
	m, d, acct, at := profileGateFixture(t)
	p := m.profileDeploymentPolicies[d.AppID]
	decision := decideProfileCanaryGate(d, p, nil, "stable", 1, at.Add(2*time.Hour))
	if decision.Status != "timed_out" {
		t.Fatal(decision)
	}
	params := CanaryAdvanceParams{}
	if authorizeProfileGate(decision, &params) == nil {
		t.Fatal("timeout held policy continued")
	}
	decision.OnTimeout = "continue"
	if err := authorizeProfileGate(decision, &params); err != nil {
		t.Fatal(err)
	}
	decision.Status = "regressed"
	if authorizeProfileGate(decision, &params) == nil {
		t.Fatal("timeout bypassed regression")
	}
	params.ProfileGateOverride = &api.ProfileGateOverride{ExpectedPolicyRevision: 2, Reason: "reviewed"}
	if authorizeProfileGate(decision, &params) == nil {
		t.Fatal("stale override accepted")
	}
	params.ProfileGateOverride.ExpectedPolicyRevision = 1
	params.RequireSafeReleaseLease = true
	if authorizeProfileGate(decision, &params) == nil {
		t.Fatal("worker override accepted")
	}
	if _, err := m.ReadProfileCanaryGate(t.Context(), uuid.NewString(), d.AppID, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign evidence", err)
	}
	if _, _, err := m.RecoverRollout(t.Context(), d.AppID, "promote", "bypass"); err == nil {
		t.Fatal("legacy promotion bypassed gate")
	}
	if _, err := m.UpdateDeploymentTraffic(t.Context(), d.ID, 100); err == nil {
		t.Fatal("traffic split bypassed gate")
	}
	work, err := m.ClaimProfileCanaryCheck(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	a := gateAssessment(work, at, "regressed")
	a.Candidate.End = a.Candidate.End.Add(time.Second)
	if _, err = m.FinishProfileCanaryCheck(t.Context(), work, a, false, at); err == nil {
		t.Fatal("foreign window accepted")
	}
	a = gateAssessment(work, at, "regressed")
	changed := m.deployments[d.ID]
	changed.CanaryStep = 1
	m.deployments[d.ID] = changed
	if _, err = m.FinishProfileCanaryCheck(t.Context(), work, a, false, at); !errors.Is(err, ErrProfileCheckLease) {
		t.Fatal("stale stage committed", err)
	}
	_ = acct
}

func TestProfileCanaryGatePolicyBounds(t *testing.T) {
	options := api.DefaultProfileRegressionOptions()
	options.Routes = []string{"POST /checkout"}
	c := api.ProfileDeploymentPolicyConfig{Enabled: true, WindowSeconds: 60, Options: options, CanaryGate: &api.ProfileCanaryGatePolicy{Confirmations: 2, TimeoutSeconds: 150, OnTimeout: "hold"}}
	if err := validateProfileCanaryGatePolicy(c); err != nil {
		t.Fatal(err)
	}
	c.CanaryGate.TimeoutSeconds = 149
	if validateProfileCanaryGatePolicy(c) == nil {
		t.Fatal("capture timeout accepted")
	}
	c.CanaryGate.TimeoutSeconds = 1800
	c.CanaryGate.Confirmations = 6
	if validateProfileCanaryGatePolicy(c) == nil {
		t.Fatal("unbounded confirmations")
	}
}
