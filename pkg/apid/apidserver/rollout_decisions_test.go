package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type decisionFixture struct {
	e      apidserver.TestEnv
	app    state.App
	prior  state.Deployment
	canary state.Deployment
}

func newDecisionFixture(ctx context.Context, t *testing.T, slug string) decisionFixture {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
		CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).SetDeploymentCanaryState(ctx, canary.ID, "balanced", 0, 4, time.Now().Add(-24*time.Hour), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	return decisionFixture{e: e, app: app, prior: prior, canary: canary}
}

func breakerDecision(rule string, step int, stableID string) api.RolloutDecision {
	now := time.Now().UTC()
	return api.RolloutDecision{
		Kind: api.RolloutRuleKind(rule), Rule: rule, Summary: "insufficient request samples (candidate=0 stable=3; need 20 each)",
		Step: step, TrafficPercent: 1, WindowStart: now.Add(-time.Minute).Format(time.RFC3339Nano), WindowEnd: now.Format(time.RFC3339Nano),
		StableDeploymentID: stableID, Candidate: &api.RolloutDecisionSample{ProbeRequests: 30}, Stable: &api.RolloutDecisionSample{Requests: 3, ProbeRequests: 21},
		ProbesIncluded: rule == api.RolloutRuleAdvanceProbeEvidence, DecidedAt: now.Format(time.RFC3339Nano),
	}
}

func (f decisionFixture) get(t *testing.T) api.DeploymentResponse {
	t.Helper()
	rec := f.e.Do(t, http.MethodGet, "/v1/deployments/"+f.canary.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get deployment status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSafeDeployRoutesRecordRolloutDecisions(t *testing.T) {
	ctx := context.Background()
	f := newDecisionFixture(ctx, t, "decision-worker")
	canaryToken := "canary-service-secret-0000000000000001"
	actionToken := "action-service-secret-0000000000000001"
	mux := http.NewServeMux()
	if err := (*f.e.S()).MountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	if err := (*f.e.Store()).StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	client := api.NewInternalSafeDeployClient(server.URL, canaryToken, actionToken)

	held, err := client.RecordRolloutHold(ctx, f.canary.ID, 0, breakerDecision(api.RolloutRuleHoldInsufficient, 0, f.prior.ID))
	if err != nil || !held.Recorded || held.AuditID == "" {
		t.Fatalf("first hold = %+v, %v", held, err)
	}
	if again, err := client.RecordRolloutHold(ctx, f.canary.ID, 0, breakerDecision(api.RolloutRuleHoldInsufficient, 0, f.prior.ID)); err != nil || again.Recorded {
		t.Fatalf("repeated hold = %+v, %v; want deduplicated", again, err)
	}
	if got := f.get(t).RolloutHold; got == nil || got.Rule != api.RolloutRuleHoldInsufficient || got.Candidate == nil || got.Candidate.ProbeRequests != 30 {
		t.Fatalf("rollout_hold = %+v", got)
	}
	var problem *api.APIError
	if _, err := client.RecordRolloutHold(ctx, f.canary.ID, 0, breakerDecision(api.RolloutRuleAdvanceHealthy, 0, f.prior.ID)); !errors.As(err, &problem) || problem.Problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("advance record on the hold route = %v, want 422", err)
	}

	advanced, err := client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, breakerDecision(api.RolloutRuleAdvanceProbeEvidence, 0, f.prior.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got := advanced.Deployment.RolloutDecision; got == nil || got.Rule != api.RolloutRuleAdvanceProbeEvidence || !got.ProbesIncluded {
		t.Fatalf("advance response decision = %+v", got)
	}
	if got := f.get(t); got.RolloutHold != nil || got.RolloutDecision == nil || got.RolloutDecision.Rule != api.RolloutRuleAdvanceProbeEvidence {
		t.Fatalf("after advance hold=%+v decision=%+v; want the old step's hold gone", got.RolloutHold, got.RolloutDecision)
	}
	audits, err := (*f.e.Store()).ListDeploymentAudit(ctx, f.canary.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var sawAdvance, sawHold bool
	for _, a := range audits {
		var data struct {
			Decision *api.RolloutDecision `json:"decision"`
		}
		_ = json.Unmarshal(a.Data, &data)
		switch {
		case a.Kind == state.DeployRolloutHeld && data.Decision != nil && data.Decision.Rule == api.RolloutRuleHoldInsufficient:
			sawHold = true
		case a.Kind == state.DeployTrafficChanged && data.Decision != nil && data.Decision.Rule == api.RolloutRuleAdvanceProbeEvidence:
			sawAdvance = true
		}
	}
	if !sawAdvance || !sawHold {
		t.Fatalf("audit timeline advance=%v hold=%v: %+v", sawAdvance, sawHold, audits)
	}

	if _, err := client.RecoverDeploymentRolloutWithDecision(ctx, f.canary.ID, f.prior.ID, "abort", "circuit breaker test", "decision-abort",
		breakerDecision(api.RolloutRuleHoldInsufficient, 1, f.prior.ID)); !errors.As(err, &problem) || problem.Problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("hold record on the abort route = %v, want 422", err)
	}
	abort := breakerDecision(api.RolloutRuleAbort5xx, 1, f.prior.ID)
	abort.Summary = "5xx regression (candidate 66.7%, stable 0.0%; 20/30 vs 0/30 requests)"
	recovered, err := client.RecoverDeploymentRolloutWithDecision(ctx, f.canary.ID, f.prior.ID, "abort", "circuit breaker test", "decision-abort-ok", abort)
	if err != nil {
		t.Fatal(err)
	}
	if got := recovered.Deployment.RolloutDecision; recovered.Deployment.RolloutState != "aborted" || got == nil || got.Rule != api.RolloutRuleAbort5xx {
		t.Fatalf("abort response = state %q decision %+v", recovered.Deployment.RolloutState, got)
	}
}

// TestSafeDeployWorkerAdvancesPacedStageOnEvidence pins ADR-1321 at APID: a
// worker may advance an adaptive stage after its MinDuration only with a
// conclusive, clean evidence record; anything else waits for the timer.
func TestSafeDeployWorkerAdvancesPacedStageOnEvidence(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		preset   string
		decision *api.RolloutDecision
		wantOK   bool
	}{
		{name: "adaptive with probe evidence", preset: "adaptive", wantOK: true,
			decision: ptrDecision(api.RolloutRuleAdvanceProbeEvidence)},
		{name: "adaptive with healthy evidence", preset: "adaptive", wantOK: true,
			decision: ptrDecision(api.RolloutRuleAdvanceHealthy)},
		{name: "adaptive with the no-news rule", preset: "adaptive",
			decision: ptrDecision(api.RolloutRuleAdvanceLowTraffic)},
		{name: "adaptive without a record", preset: "adaptive"},
		{name: "explicit balanced with probe evidence", preset: "balanced",
			decision: ptrDecision(api.RolloutRuleAdvanceProbeEvidence)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDecisionFixture(ctx, t, "paced-worker")
			if err := (*f.e.Store()).SetDeploymentCanaryState(ctx, f.canary.ID, tc.preset, 0, 4, time.Now().Add(-70*time.Second), "rolling_out"); err != nil {
				t.Fatal(err)
			}
			canaryToken := "canary-service-secret-0000000000000001"
			actionToken := "action-service-secret-0000000000000001"
			mux := http.NewServeMux()
			if err := (*f.e.S()).MountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			if err := (*f.e.Store()).StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
				t.Fatal(err)
			}
			client := api.NewInternalSafeDeployClient(server.URL, canaryToken, actionToken)
			var err error
			if tc.decision != nil {
				tc.decision.StableDeploymentID = f.prior.ID
				_, err = client.AdvanceCanaryWithDecision(ctx, f.canary.ID, 0, *tc.decision)
			} else {
				_, err = client.AdvanceCanary(ctx, f.canary.ID, 0)
			}
			step := f.get(t).CanaryStep
			if tc.wantOK {
				if err != nil || step != 1 {
					t.Fatalf("advance after 70s = step %d, err %v; want step 1", step, err)
				}
				return
			}
			var problem *api.APIError
			if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict || step != 0 {
				t.Fatalf("advance after 70s = step %d, err %v; want 409 at step 0", step, err)
			}
		})
	}
}

func ptrDecision(rule string) *api.RolloutDecision {
	d := breakerDecision(rule, 0, "")
	return &d
}

func TestCustomerRolloutTransitionsRecordManualDecisions(t *testing.T) {
	f := newDecisionFixture(context.Background(), t, "decision-manual")
	path := "/v1/deployments/" + f.canary.ID + "/canary/advance"
	forged := breakerDecision(api.RolloutRuleAdvanceHealthy, 0, f.prior.ID)
	rec := f.e.Do(t, http.MethodPost, path, api.AdvanceCanaryRequest{ExpectedStep: 0, Decision: &forged}, nil)
	apidserver.AssertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeValidation)
	if got := f.get(t); got.CanaryStep != 0 || got.RolloutDecision != nil {
		t.Fatalf("forged customer decision changed the rollout: %+v", got)
	}
	rec = f.e.Do(t, http.MethodPost, path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual advance status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := f.get(t).RolloutDecision; got == nil || got.Rule != api.RolloutRuleAdvanceManual || got.Step != 0 {
		t.Fatalf("manual advance decision = %+v", got)
	}
	rec = f.e.Do(t, http.MethodPost, "/v1/apps/"+f.app.Slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: "abort", Reason: "stop it"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual abort status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := f.get(t); got.RolloutState != "aborted" || got.RolloutDecision == nil || got.RolloutDecision.Rule != api.RolloutRuleAbortManual {
		t.Fatalf("manual abort = state %q decision %+v", got.RolloutState, got.RolloutDecision)
	}
}
