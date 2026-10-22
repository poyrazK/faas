package apidserver

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPatchDeploymentTrafficMCPAccessPolicy(t *testing.T) {
	e := setup(t, api.PlanPro)
	notifier := &captureNotifier{}
	e.s.notif = notifier
	stable := mustSeedDeployment(t, e, "mcp-policy-api")
	if err := e.store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	app, err := e.store.AppBySlug(t.Context(), "mcp-policy-api")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := api.MCPAccessPolicyFingerprint("https://reports.example/mcp", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expectation := &api.MCPAccessPolicyExpectation{Resource: "https://reports.example/mcp", Fingerprint: fingerprint}
	_, err = e.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: app.AccountID, AppID: app.ID, Kind: state.EdgeRuleKindJWT, Enabled: true, MatchHost: "*.example", MatchPath: "/**", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT, JWT: &state.EdgeRuleJWTAction{Issuer: "https://identity.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &stable.ID, ExpectedMCPAccessPolicy: expectation}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeMCPAccessPolicyChanged)
	before, _ := e.store.DeploymentByID(t.Context(), stable.ID)
	after, _ := e.store.DeploymentByID(t.Context(), candidate.ID)
	if before.TrafficPercent != 100 || after.TrafficPercent != 0 {
		t.Fatal("stale policy PATCH changed traffic")
	}
	if len(notifier.byChannel(db.NotifyDeploymentChanged)) != 0 {
		t.Fatal("failed policy guard emitted notification")
	}
	if strings.Contains(rec.Body.String(), "identity.example") {
		t.Fatal("error exposed private rule action")
	}
	rules, err := e.store.ListEdgeRulesForAccount(t.Context(), app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	projected := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, r := range rules {
		projected = append(projected, edgerules.EdgeRuleResponse(r))
	}
	expectation.Fingerprint, err = api.MCPAccessPolicyFingerprint(expectation.Resource, projected, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	expectation.AccountRevision = &revision
	rec = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedMCPAccessPolicy: expectation}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeMCPGatewayEvidenceUnavailable)
	after, _ = e.store.DeploymentByID(t.Context(), candidate.ID)
	if after.TrafficPercent != 0 || len(notifier.byChannel(db.NotifyDeploymentChanged)) != 0 {
		t.Fatal("unavailable evidence changed traffic or notified")
	}
	revision = 0
	rec = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedMCPAccessPolicy: expectation}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	expectation.AccountRevision = nil
	rec = e.do(t, http.MethodGet, "/v1/deployments/"+candidate.ID+"/audit", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deploy.mcp_admission") || !strings.Contains(rec.Body.String(), api.CodeMCPGatewayEvidenceUnavailable) || strings.Contains(rec.Body.String(), "identity.example") {
		t.Fatalf("missing or private admission history: %s", rec.Body.String())
	}
	// The existing audit route must enforce the owning account.
	other, err := e.store.CreateAccount(t.Context(), "other-admission@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "other-admission", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	otherDeployment, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: otherApp.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:other"})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, "/v1/deployments/"+otherDeployment.ID+"/audit", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
	expectation.Fingerprint = "not-a-fingerprint"
	rec = e.do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedMCPAccessPolicy: expectation}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
