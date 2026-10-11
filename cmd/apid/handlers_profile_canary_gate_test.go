package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
)

func TestProfileCanaryGateHTTPReadHoldAndOverride(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "cpu-gated"})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	options := api.DefaultProfileRegressionOptions()
	options.Routes = []string{"POST /checkout"}
	zero := int64(0)
	policy, err := e.store.SaveProfileDeploymentPolicy(ctx, e.acct.ID, app.ID, api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: 60, Options: options, CanaryGate: &api.ProfileCanaryGatePolicy{Confirmations: 2, TimeoutSeconds: 1800, OnTimeout: "hold"}}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/deployments/" + candidate.ID + "/canary/"
	read := e.do(t, http.MethodGet, base+"profile-gate", nil, nil)
	var decision api.ProfileCanaryGateDecision
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &decision) != nil || decision.Status != "collecting" || decision.PolicyRevision != policy.Revision {
		t.Fatal(read.Code, read.Body.String())
	}
	held := e.do(t, http.MethodPost, base+"advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if held.Code != http.StatusConflict {
		t.Fatal("gate not held", held.Code, held.Body.String())
	}
	worker := e.do(t, http.MethodPost, base+"advance", api.AdvanceCanaryRequest{ExpectedStep: 0, ProfileGateRollback: true}, nil)
	if worker.Code != http.StatusBadRequest {
		t.Fatal("public worker intent accepted", worker.Code, worker.Body.String())
	}
	request := api.AdvanceCanaryRequest{ExpectedStep: 0, ProfileGateOverride: &api.ProfileGateOverride{ExpectedPolicyRevision: policy.Revision, Reason: "Reviewed CPU/request evidence and accepted the cost."}}
	overridden := e.do(t, http.MethodPost, base+"advance", request, nil)
	if overridden.Code != http.StatusOK {
		t.Fatal(overridden.Code, overridden.Body.String())
	}
	audits, err := e.store.ListDeploymentAudit(ctx, candidate.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, audit := range audits {
		if audit.Kind == state.DeployTrafficChanged {
			var data map[string]json.RawMessage
			if json.Unmarshal(audit.Data, &data) == nil && len(data["profile_gate"]) > 0 && audit.Actor == "account:"+e.acct.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("override actor/decision missing from audit", audits)
	}
}
