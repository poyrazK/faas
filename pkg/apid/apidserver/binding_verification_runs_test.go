package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBindingVerificationRunAPIRecoveryConsentAndDrift(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, account := *e.Store(), *e.Acct()
	ctx := t.Context()
	metadata := apidserver.SourceRefTestEnv{}
	*metadata.AcctID() = account.ID
	*metadata.Srv() = *e.S()
	apidserver.ConfigureSourceRefManagedPostgres(t, metadata)
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "receipt-api", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`, "PRIVATE_RUN_SECRET": "secret-run-value"})
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:run", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	binding, err := s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordTriggerConsumerHealth(ctx, binding.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Success: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetBindingReleasePolicy(ctx, account.ID, app.ID, dep.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/bindings/verification-runs"
	originalKey := *e.Key()
	for _, test := range []struct {
		scopes []string
		want   int
	}{
		{[]string{api.ScopeDeployWrite}, http.StatusForbidden},
		{[]string{api.ScopeAppsRead, api.ScopeStorageManage, api.ScopeManagedPostgresRead}, http.StatusOK},
	} {
		plain, hash, keyErr := api.GenerateAPIKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if _, keyErr = s.CreateAPIKey(ctx, account.ID, hash, "review", test.scopes); keyErr != nil {
			t.Fatal(keyErr)
		}
		*e.Key() = plain
		result := e.Do(t, http.MethodPost, path+"/review", api.ReviewBindingVerificationRequest{}, nil)
		if result.Code != test.want {
			t.Fatalf("review scope %v: %d %s", test.scopes, result.Code, result.Body.String())
		}
		if test.want == http.StatusOK {
			result = e.Do(t, http.MethodPost, path, api.CreateBindingVerificationRunRequest{}, nil)
			if result.Code != http.StatusForbidden {
				t.Fatalf("review key admitted receipt: %d %s", result.Code, result.Body.String())
			}
		}
	}
	*e.Key() = originalKey
	response := e.Do(t, http.MethodPost, path+"/review", api.ReviewBindingVerificationRequest{}, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("review: %d %s", response.Code, response.Body.String())
	}
	var review api.BindingVerificationReview
	if err = json.Unmarshal(response.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	if !review.Ready || !review.QueuePublishRequired || review.ReviewDigest == "" || review.DeploymentID != dep.ID {
		t.Fatalf("review selection: %+v", review)
	}
	if strings.Contains(response.Body.String(), "secret-run-value") || strings.Contains(response.Body.String(), "PRIVATE_RUN_SECRET") {
		t.Fatal("review exposed secrets")
	}
	messages, err := s.QueuePeek(ctx, app.ID, 10, "")
	if err != nil || len(messages) != 0 {
		t.Fatalf("review published: %d %v", len(messages), err)
	}
	request := api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: review.DeploymentID, ReviewDigest: review.ReviewDigest, TTLSeconds: 60}
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("consent: %d %s", response.Code, response.Body.String())
	}
	request.QueuePublish = true
	request.MaxConcurrency = 9
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid budget: %d %s", response.Code, response.Body.String())
	}
	request.MaxConcurrency = 0
	request.QueuePublish = true
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-run-value") || strings.Contains(response.Body.String(), "PRIVATE_RUN_SECRET") || strings.Contains(response.Body.String(), "Admissions") {
		t.Fatal("receipt exposed private configuration")
	}
	var run api.BindingVerificationRun
	if err = json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.MaxConcurrency != api.BindingVerificationRunnerDefaultConcurrency {
		t.Fatalf("default budget: %+v", run)
	}
	diagnosticResponse := e.Do(t, http.MethodGet, path+"/"+run.ID+"/diagnostics", nil, nil)
	if diagnosticResponse.Code != http.StatusOK || diagnosticResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("diagnostics: %d %s", diagnosticResponse.Code, diagnosticResponse.Body.String())
	}
	var diagnostic api.BindingVerificationRunDiagnostics
	if err = json.Unmarshal(diagnosticResponse.Body.Bytes(), &diagnostic); err != nil || diagnostic.RunID != run.ID || !diagnostic.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("diagnostic identity: %+v %v", diagnostic, err)
	}
	if strings.Contains(diagnosticResponse.Body.String(), "secret-run-value") || strings.Contains(diagnosticResponse.Body.String(), "Admissions") {
		t.Fatal("diagnostics exposed private metadata")
	}

	diagnosticKey := *e.Key()
	for _, test := range []struct {
		scopes []string
		want   int
	}{
		{[]string{api.ScopeDeployWrite}, http.StatusForbidden},
		{[]string{api.ScopeAppsRead, api.ScopeStorageManage, api.ScopeManagedPostgresRead}, http.StatusOK},
	} {
		plain, hash, keyErr := api.GenerateAPIKey()
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if _, keyErr = s.CreateAPIKey(ctx, account.ID, hash, "diagnostics", test.scopes); keyErr != nil {
			t.Fatal(keyErr)
		}
		*e.Key() = plain
		result := e.Do(t, http.MethodGet, path+"/"+run.ID+"/diagnostics", nil, nil)
		if result.Code != test.want {
			t.Fatalf("diagnostic scope %v: %d %s", test.scopes, result.Code, result.Body.String())
		}
	}
	*e.Key() = diagnosticKey
	messages, err = s.QueuePeek(ctx, app.ID, 10, "")
	if err != nil || len(messages) != 0 {
		t.Fatalf("diagnostics published messages: %d %v", len(messages), err)
	}
	invalid := e.Do(t, http.MethodGet, path+"/not-a-uuid/diagnostics", nil, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid diagnostic UUID: %d %s", invalid.Code, invalid.Body.String())
	}

	stepPath := path + "/" + run.ID + "/steps/0/admit"
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("admit: %d %s", response.Code, response.Body.String())
	}
	var first api.BindingVerificationRunStep
	if err = json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	} // Drop this response.
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/resume", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	var second api.BindingVerificationRunStep
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &second) != nil || second.ResourceID != first.ResourceID {
		t.Fatalf("duplicate admission: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/complete", nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("publication qualified: %d %s", response.Code, response.Body.String())
	}
	disabled := false
	if _, err = s.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, binding.Binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/resume", nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("drift resumed: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodDelete, path+"/"+run.ID, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel after drift: %d %s", response.Code, response.Body.String())
	}
	canary, err := s.GetQueueBindingCanary(ctx, account.ID, app.ID, first.ResourceID)
	if err != nil || canary.Status != "cancelled" {
		t.Fatalf("owned drifted canary not cancelled: %+v %v", canary, err)
	}
	response = e.Do(t, http.MethodPost, stepPath, nil, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("cancelled run admitted: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodGet, path+"/"+run.ID, nil, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
}

func TestBindingVerificationRunAPIWaitsForQualification(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, account := *e.Store(), *e.Acct()
	ctx := t.Context()
	metadata := apidserver.SourceRefTestEnv{}
	*metadata.AcctID() = account.ID
	*metadata.Srv() = *e.S()
	apidserver.ConfigureSourceRefManagedPostgres(t, metadata)
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "qualification-api", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`, "PRIVATE_RUN_SECRET": "secret-run-value"})
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:run", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	binding, err := s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetBindingReleasePolicy(ctx, account.ID, app.ID, dep.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceConsumerReadiness}})
	if err != nil {
		t.Fatal(err)
	}

	path := "/v1/apps/" + app.Slug + "/bindings/verification-runs"
	response := e.Do(t, http.MethodPost, path+"/review", api.ReviewBindingVerificationRequest{}, nil)
	var review api.BindingVerificationReview
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &review) != nil || !review.Ready {
		t.Fatalf("review: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, path, api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: review.DeploymentID, ReviewDigest: review.ReviewDigest, TTLSeconds: 60}, nil)
	var run api.BindingVerificationRun
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &run) != nil {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/complete", nil, nil)
	var pending api.BindingVerificationRun
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &pending) != nil || pending.Status != "pending" || pending.Qualification == nil || len(pending.Qualification.Conditions) != 1 || pending.Qualification.Conditions[0].Code != "queue_consumer_unobserved" || pending.ID != run.ID || !pending.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("pending: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "secret-run-value") {
		t.Fatal("pending response exposed private data or was cacheable")
	}
	messages, err := s.QueuePeek(ctx, app.ID, 10, "")
	if err != nil || len(messages) != 0 {
		t.Fatalf("completion admitted work: %d %v", len(messages), err)
	}
	if err = s.RecordTriggerConsumerHealth(ctx, binding.Changes[0].TriggerID, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Success: true}); err != nil {
		t.Fatal(err)
	}
	response = e.Do(t, http.MethodPost, path+"/"+run.ID+"/complete", nil, nil)
	var passed api.BindingVerificationRun
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &passed) != nil || passed.Status != "passed" || passed.Qualification != nil || passed.ID != run.ID || !passed.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("passed: %d %s", response.Code, response.Body.String())
	}
}
