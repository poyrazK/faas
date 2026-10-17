package apidserver_test

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProjectBindingVerificationRunAPIRecoveryAndMembershipDrift(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	metadata := apidserver.SourceRefTestEnv{}
	*metadata.AcctID() = acct.ID
	*metadata.Srv() = *e.S()
	apidserver.ConfigureSourceRefManagedPostgres(t, metadata)
	p, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "run-graph"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: p.ID, Slug: "production"}); err != nil && !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	deployments := map[string]string{}
	apps := map[string]state.App{}
	for _, name := range []string{"graph-front", "graph-back"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: p.ID, Slug: name, WorkloadName: name, Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		apps[name] = app
		env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`, "PRIVATE_GRAPH_SECRET": "secret-graph-value"})
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:graph", OverrideEnv: env})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(ctx, d.ID, "/rootfs", "rootfs", 1); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		deployments[name] = d.ID
		requirements := map[string]string{}
		if name == "graph-front" || name == "graph-back" {
			b, err := s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.RecordTriggerConsumerHealth(ctx, b.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Success: true}); err != nil {
				t.Fatal(err)
			}
			requirements["orders"] = api.QueueEvidenceHandlerCompletion
		}
		if err = s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetBindingReleasePolicy(ctx, acct.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m", QueueRequirements: requirements}); err != nil {
			t.Fatal(err)
		}
	}
	base := "/v1/projects/" + p.Slug + "/environments/production/release-sets"
	response := e.Do(t, http.MethodPost, base+"/prepare", api.PublishProjectReleaseSetRequest{Deployments: deployments, TTLSeconds: 60}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", response.Code, response.Body.String())
	}
	var manifest api.CheckedProjectReleaseManifest
	if err = json.Unmarshal(response.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	path := base + "/verification-runs"
	response = e.Do(t, http.MethodPost, path+"/review", api.ReviewProjectBindingVerificationRequest{Manifest: manifest, QueuePublish: true}, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("graph review: %d %s", response.Code, response.Body.String())
	}
	var review api.ProjectBindingVerificationReview
	if err = json.Unmarshal(response.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if !review.Ready || !review.QueuePublishRequired || len(review.Members) != 2 || review.ReviewDigest == "" {
		t.Fatalf("graph review: %+v", review)
	}
	if strings.Contains(response.Body.String(), "secret-graph-value") || strings.Contains(response.Body.String(), "PRIVATE_GRAPH_SECRET") {
		t.Fatal("graph review exposed secrets")
	}
	for _, app := range apps {
		messages, err := s.QueuePeek(ctx, app.ID, 10, "")
		if err != nil || len(messages) != 0 {
			t.Fatalf("review published: %d %v", len(messages), err)
		}
	}
	request := api.CreateProjectBindingVerificationRunRequest{MaxConcurrency: 1, ID: uuid.NewString(), Manifest: review.Manifest, ReviewDigests: review.ReviewDigests, TTLSeconds: 60}
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing consent: %d %s", response.Code, response.Body.String())
	}
	request.QueuePublish = true
	request.ReviewDigest = review.ReviewDigest
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-graph-value") || strings.Contains(response.Body.String(), "PRIVATE_GRAPH_SECRET") {
		t.Fatal("graph exposed credentials")
	}
	var run api.ProjectBindingVerificationRun
	if err = json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Members) != 2 {
		t.Fatalf("partial graph: %+v", run)
	}
	child := run.Members["graph-front"]
	diagnosticResponse := e.Do(t, http.MethodGet, path+"/"+run.ID+"/diagnostics", nil, nil)
	if diagnosticResponse.Code != http.StatusOK || diagnosticResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("diagnostics: %d %s", diagnosticResponse.Code, diagnosticResponse.Body.String())
	}
	var diagnostic api.ProjectBindingVerificationRunDiagnostics
	if err = json.Unmarshal(diagnosticResponse.Body.Bytes(), &diagnostic); err != nil || diagnostic.RunID != run.ID || !diagnostic.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("diagnostic identity: %+v %v", diagnostic, err)
	}
	if strings.Contains(diagnosticResponse.Body.String(), "secret-graph-value") || strings.Contains(diagnosticResponse.Body.String(), "Admissions") {
		t.Fatal("diagnostics exposed private metadata")
	}

	stepPath := "/v1/apps/graph-front/bindings/verification-runs/" + child.ID + "/steps/0/admit"
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	var first api.BindingVerificationRunStep
	if err = json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	worker := run.Members["graph-back"]
	budgetResponse := e.Do(t, http.MethodPost, "/v1/apps/graph-back/bindings/verification-runs/"+worker.ID+"/steps/0/admit", nil, nil)
	var budgetProblem api.Problem
	if budgetResponse.Code != http.StatusTooManyRequests || json.Unmarshal(budgetResponse.Body.Bytes(), &budgetProblem) != nil || budgetProblem.Code != "binding_verification_budget_full" {
		t.Fatalf("shared budget: %d %s", budgetResponse.Code, budgetResponse.Body.String())
	}
	budgetResponse = e.Do(t, http.MethodGet, path+"/"+run.ID+"/diagnostics", nil, nil)
	if budgetResponse.Code != http.StatusOK || json.Unmarshal(budgetResponse.Body.Bytes(), &diagnostic) != nil || diagnostic.Budget == nil || diagnostic.Budget.RunID != run.ID || diagnostic.Budget.MaxConcurrency != 1 || diagnostic.Budget.ActiveResources != 1 || diagnostic.Budget.WaitingSteps != 1 || !diagnostic.Budget.Full {
		t.Fatalf("budget diagnostics: %d %s", budgetResponse.Code, budgetResponse.Body.String())
	}
	changedBudget := request
	changedBudget.MaxConcurrency = 2
	budgetResponse = e.Do(t, http.MethodPost, path, changedBudget, nil)
	if budgetResponse.Code != http.StatusConflict {
		t.Fatalf("budget mutation accepted: %d %s", budgetResponse.Code, budgetResponse.Body.String())
	}
	// Lose both creation and admission responses; replay identities remain fixed.
	response = e.Do(t, http.MethodPost, path, request, nil)
	var replay api.ProjectBindingVerificationRun
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &replay) != nil || replay.Members["graph-front"].ID != child.ID || !replay.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("creation replay: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/resume", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	var second api.BindingVerificationRunStep
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &second) != nil || second.ResourceID != first.ResourceID {
		t.Fatalf("duplicate work: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/complete", nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("queued graph passed: %d %s", response.Code, response.Body.String())
	}
	if _, err = s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: p.ID, Slug: "graph-new", WorkloadName: "graph-new", Type: state.AppTypeFunction}); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("membership changed but admitted: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodGet, path+"/"+run.ID, nil, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"blocked"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodDelete, path+"/"+run.ID, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel after drift: %d %s", response.Code, response.Body.String())
	}
	canary, err := s.GetQueueBindingCanary(ctx, acct.ID, apps["graph-front"].ID, first.ResourceID)
	if err != nil || canary.Status != "cancelled" {
		t.Fatalf("owned cleanup: %+v %v", canary, err)
	}
}
