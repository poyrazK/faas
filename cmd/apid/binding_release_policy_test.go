package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
)

func TestBindingReleasePolicyPermissions(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	slug := mustSeedEdgeRuleApp(t, e, "read-only-release-policy")
	path := "/v1/apps/" + slug + "/bindings/release-policy"
	if response := e.do(t, http.MethodGet, path, nil, nil); response.Code != 200 {
		t.Fatalf("read policy: %d %s", response.Code, response.Body.String())
	}
	zero := int64(0)
	if response := e.do(t, http.MethodPut, path, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}, nil); response.Code != 403 {
		t.Fatalf("read-only policy write: %d %s", response.Code, response.Body.String())
	}
}

func TestBindingReleasePolicyCanaryManualAndWorker(t *testing.T) {
	e, app, serving, _ := promotionFixture(t)
	ctx := context.Background()
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	admissionPath := "/v1/apps/" + app.Slug + "/deployments"
	response := e.do(t, http.MethodPost, admissionPath, api.CreateDeploymentRequest{Image: goodSidecarImage, Canary: &api.CanaryPresetSpec{Preset: "balanced"}}, nil)
	if response.Code != 409 {
		t.Fatalf("positive canary admission: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPost, admissionPath, api.CreateDeploymentRequest{Image: goodSidecarImage, Canary: &api.CanaryPresetSpec{Preset: "custom", Stages: []canary.CustomStage{{Percent: 0, Duration: "1s"}, {Percent: 10, Duration: "1s"}, {Percent: 50, Duration: "1s"}, {Percent: 100, Duration: "0s"}}}}, nil)
	var admitted api.DeploymentResponse
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &admitted) != nil {
		t.Fatalf("zero-stage canary admission: %d %s", response.Code, response.Body.String())
	}
	if err := e.store.SetDeploymentRootfs(ctx, admitted.ID, "/candidate/image", "candidate/"+admitted.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, admitted.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.DeploymentByID(ctx, admitted.ID)
	if err != nil || candidate.TrafficPercent != 0 {
		t.Fatalf("zero-stage activation: %+v %v", candidate, err)
	}
	path := "/v1/deployments/" + candidate.ID + "/canary/advance"
	response = e.do(t, http.MethodPost, path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if response.Code != 409 {
		t.Fatalf("unverified advance: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	response = e.do(t, http.MethodPost, path, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if response.Code != 200 {
		t.Fatalf("verified advance: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 10)
	if err := e.store.StampSafeReleaseWorkerLease(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "custom", 1, 4, time.Now().Add(-time.Hour), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	worker := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"expected_step":1}`))
		r.SetPathValue("id", candidate.ID)
		w := httptest.NewRecorder()
		e.s.advanceCanaryByWorker(w, r, e.acct)
		return w
	}
	response = worker()
	if response.Code != 200 {
		t.Fatalf("verified worker advance: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 50)
}

func TestBindingReleasePolicyProtectsOrdinaryTrafficAndRequestWaivers(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	path := "/v1/apps/" + app.Slug + "/bindings/release-policy"
	zero := int64(0)
	response := e.do(t, http.MethodGet, path, nil, nil)
	if response.Code != 200 {
		t.Fatalf("get: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPut, path, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "1m"}, nil)
	if response.Code != 200 {
		t.Fatalf("set: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 25}, nil)
	if response.Code != 409 {
		t.Fatalf("missing evidence bypass: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	response = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 25}, nil)
	if response.Code != 200 {
		t.Fatalf("checked traffic: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 25)
	// The stored policy overrides a looser promotion request.
	response = e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{MaxVerificationAge: "24h", AllowUnsupported: true}, nil)
	var receipt api.BindingPromotionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || receipt.BindingsCheck == nil || receipt.BindingsCheck.MaxVerificationAge != "1m0s" || receipt.BindingsCheck.AllowUnsupported {
		t.Fatalf("weakened policy: %d %s", response.Code, response.Body.String())
	}
	// Recovery never implicitly waives policy: an explicit reasoned opt-out works.
	revision := int64(1)
	response = e.do(t, http.MethodPut, path, api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &revision}, nil)
	if response.Code != 400 {
		t.Fatalf("missing reason: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPut, path, api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &revision, Reason: "restore stable deployment"}, nil)
	if response.Code != 200 {
		t.Fatalf("disable: %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodPatch, "/v1/deployments/"+serving.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100}, nil)
	if response.Code != 200 {
		t.Fatalf("recovery: %d %s", response.Code, response.Body.String())
	}
	p, err := e.store.GetBindingReleasePolicy(context.Background(), e.acct.ID, app.ID, "staging")
	if err != nil || p.Mode != "off" {
		t.Fatalf("scope isolation: %+v %v", p, err)
	}
}
