package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrivatePublicationReadinessTrafficHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	stable := apidserver.MustSeedDeployment(t, e, "publication-roles")
	if err := store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.AppBySlug(t.Context(), "publication-roles")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	policy, err := store.SetMCPReadinessPolicy(t.Context(), app.AccountID, app.ID, api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "review required role", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: hash}}})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := store.MCPReadinessSubject(t.Context(), app.AccountID, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := store.RecordMCPReadiness(t.Context(), subject, api.MCPReadinessEvidence{Role: "reader", Passed: true, ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: hash, Checks: []api.MCPReadinessCheck{{Name: "publication_readiness", Status: "passed"}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := &api.MCPReadinessExpectation{PolicyRevision: policy.Revision, ArtifactSHA256: subject.ArtifactSHA256, EvidenceIDs: map[string]string{"reader": uuid.NewString()}}
	req := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedServingDeploymentID: &stable.ID, ExpectedMCPReadiness: expected}
	rec := e.Do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", req, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeMCPReadinessRequired)
	got, _ := store.DeploymentByID(t.Context(), candidate.ID)
	if got.TrafficPercent != 0 {
		t.Fatal("rejected observation moved traffic")
	}
	expected.EvidenceIDs["reader"] = "private-supplied-data"
	rec = e.Do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", req, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	if strings.Contains(rec.Body.String(), "private-supplied-data") {
		t.Fatal("invalid observation leaked input")
	}
	expected.EvidenceIDs["reader"] = evidence.ID
	rec = e.Do(t, http.MethodPatch, "/v1/deployments/"+candidate.ID+"/traffic", req, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fresh observation rejected: %d %s", rec.Code, rec.Body.String())
	}
	got, _ = store.DeploymentByID(t.Context(), candidate.ID)
	if got.TrafficPercent != 100 {
		t.Fatal("fresh observation failed to promote")
	}
}
