// adr: 429 — promotion consumes exact candidate evidence and fences later changes.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func completePromotionProbe(t *testing.T, e testEnv, app state.App, target state.Deployment, output string) {
	t.Helper()
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, VerificationDeploymentID: target.ID})
	running, err := e.store.ClaimNextAppTask(context.Background(), "promotion-probe", time.Now().UTC(), time.Minute)
	if err != nil || running.ID != task.ID {
		t.Fatalf("claim: %+v %v", running, err)
	}
	running, err = e.store.MarkAppTaskRunning(context.Background(), running.ID, *running.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	_, err = e.store.CompleteAppTask(context.Background(), state.CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: output, ExitCode: &zero, FinishedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
}

func promotionFixture(t *testing.T) (testEnv, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, serving := seedAppTaskDeployment(t, e, "gated-promotion")
	candidate := seedBindingCandidate(t, e, app, "default")
	readyVerificationBinding(t, e, app)
	if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	return e, app, serving, candidate
}

func seedHealthyPromotionQueue(t *testing.T, e testEnv, app state.App) state.QueueBinding {
	t.Helper()
	binding := seedInventoryQueue(t, e, app, "events", false)
	if err := e.s.syncQueueBindingConsumer(context.Background(), app, e.acct, binding); err != nil {
		t.Fatal(err)
	}
	id, err := queueBindingTriggerID(context.Background(), e.store, app.ID, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.RecordTriggerConsumerHealth(context.Background(), id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now().UTC(), Success: true}); err != nil {
		t.Fatal(err)
	}
	return binding
}

func assertPromotionWeights(t *testing.T, e testEnv, serving, candidate state.Deployment, percent int) {
	t.Helper()
	for id, want := range map[string]int{serving.ID: 100 - percent, candidate.ID: percent} {
		dep, err := e.store.DeploymentByID(context.Background(), id)
		if err != nil || dep.TrafficPercent != want {
			t.Fatalf("weights: %+v want=%d err=%v", dep, want, err)
		}
	}
}

func TestBindingPromotionRequiresExactCandidateEvidenceAndChecksRetries(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, serving, passedPostgresVerification)
	path := "/v1/deployments/" + candidate.ID + "/promote"
	req := api.BindingPromotionRequest{ExpectedServingDeploymentID: &serving.ID}
	blocked := e.do(t, http.MethodPost, path, req, nil)
	var problem api.Problem
	if err := json.Unmarshal(blocked.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != 409 || problem.Code != "bindings_check_failed" || problem.BindingsCheck == nil || problem.BindingsCheck.Passed {
		t.Fatalf("unprobed candidate accepted: %d %s", blocked.Code, blocked.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	for _, already := range []bool{false, true} {
		response := e.do(t, http.MethodPost, path, req, nil)
		var receipt api.BindingPromotionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || receipt.AlreadyPromoted != already || receipt.BindingsCheck == nil || !receipt.BindingsCheck.Passed || receipt.BindingsCheck.ExpectedDeploymentID != candidate.ID || receipt.BindingsCheck.MaxVerificationAge != "10m0s" || receipt.Deployment.TrafficPercent != 100 {
			t.Fatalf("promotion: %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(response.Body.String(), "private-secret") {
			t.Fatalf("unsafe report: %s", response.Body.String())
		}
		assertPromotionWeights(t, e, serving, candidate, 100)
	}
	if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	response := e.do(t, http.MethodPost, path, req, nil)
	if response.Code != 409 {
		t.Fatalf("idempotent retry skipped check: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
}

type changingBindingPromotionStore struct {
	*state.MemStore
	before func()
}

func (s *changingBindingPromotionStore) PromoteDeploymentWithBindings(ctx context.Context, id string, fence state.BindingPromotionFence, serving string) (state.BindingPromotionResult, error) {
	s.before()
	return s.MemStore.PromoteDeploymentWithBindings(ctx, id, fence, serving)
}

func TestBindingPromotionRejectsConfigChangeAfterPassedCheck(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	e.s.store = &changingBindingPromotionStore{MemStore: e.store, before: func() {
		if err := e.store.UpsertAppEnv(context.Background(), e.acct.ID, app.ID, "FEATURE", "new-config"); err != nil {
			t.Fatal(err)
		}
	}}
	r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
	var problem api.Problem
	if err := json.Unmarshal(r.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if r.Code != 409 || problem.Code != "bindings_check_changed" || problem.BindingsCheck == nil || problem.BindingsCheck.Passed || problem.BindingsCheck.Blockers[0].Code != "promotion_observations_changed" {
		t.Fatalf("race accepted: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
}

func TestBindingPromotionRejectsServingChangeAndInvalidPolicy(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	unknown := uuid.NewString()
	for _, tc := range []struct {
		req    api.BindingPromotionRequest
		status int
		code   string
	}{
		{api.BindingPromotionRequest{ExpectedServingDeploymentID: &unknown}, 409, "traffic_serving_changed"},
		{api.BindingPromotionRequest{ExpectedServingDeploymentID: &candidate.ID}, 422, api.CodeValidation},
		{api.BindingPromotionRequest{MaxVerificationAge: "0s"}, 422, api.CodeValidation},
		{api.BindingPromotionRequest{MaxVerificationAge: "later"}, 422, api.CodeValidation},
		{api.BindingPromotionRequest{MaxVerificationAge: "1ns"}, 409, "bindings_check_failed"},
	} {
		r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", tc.req, nil)
		var problem api.Problem
		_ = json.Unmarshal(r.Body.Bytes(), &problem)
		if r.Code != tc.status || problem.Code != tc.code {
			t.Fatalf("policy %+v: %d %s", tc.req, r.Code, r.Body.String())
		}
		assertPromotionWeights(t, e, serving, candidate, 0)
	}
}

func TestBindingPromotionRejectsForeignDeploymentAndIncompleteCatalog(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	other, err := e.store.CreateAccount(context.Background(), "foreign-promotion@test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.store.CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "foreign-promotion"})
	if err != nil {
		t.Fatal(err)
	}
	target := seedBindingCandidate(t, e, foreign, "default")
	r := e.do(t, http.MethodPost, "/v1/deployments/"+target.ID+"/promote", api.BindingPromotionRequest{}, nil)
	if r.Code != 404 {
		t.Fatalf("foreign target: %d %s", r.Code, r.Body.String())
	}
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	e.s.managedPostgresBindings = nil
	r = e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
	if r.Code != 503 {
		t.Fatalf("missing catalog bypass: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
}

func TestBindingPromotionUnsupportedWaiverDoesNotHideRuntimeBlockers(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	seedHealthyPromotionQueue(t, e, app)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	path := "/v1/deployments/" + candidate.ID + "/promote"
	r := e.do(t, http.MethodPost, path, api.BindingPromotionRequest{}, nil)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "verification_unsupported") {
		t.Fatalf("implicit waiver: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	instance, err := e.store.CreateInstance(context.Background(), app.ID, serving.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	e.store.BackdateForTest(instance.ID, time.Now().Add(-time.Hour))
	r = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "runtime_stale") {
		t.Fatalf("waiver hid runtime blocker: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
	if err := e.store.UpdateInstanceState(context.Background(), instance.ID, "stopped"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, http.MethodPost, path, api.BindingPromotionRequest{AllowUnsupported: true}, nil)
	var receipt api.BindingPromotionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || receipt.BindingsCheck == nil || !receipt.BindingsCheck.Passed || receipt.BindingsCheck.Coverage != "partial" || !receipt.BindingsCheck.AllowUnsupported {
		t.Fatalf("waiver: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 100)
}

type changedPolicyBindingPromotionStore struct {
	*state.MemStore
	accountID string
}

func (s *changedPolicyBindingPromotionStore) ReadBindingPromotionRevision(ctx context.Context, accountID, appID string) (string, error) {
	if err := s.UpdateAccountPlan(ctx, s.accountID, api.Plan("unknown")); err != nil {
		return "", err
	}
	return s.MemStore.ReadBindingPromotionRevision(ctx, accountID, appID)
}

func TestBindingPromotionReloadsPlanAfterRevisionCapture(t *testing.T) {
	e, app, serving, candidate := promotionFixture(t)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	e.s.store = &changedPolicyBindingPromotionStore{MemStore: e.store, accountID: e.acct.ID}
	r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
	if r.Code != 403 {
		t.Fatalf("stale account plan authorized promotion: %d %s", r.Code, r.Body.String())
	}
	assertPromotionWeights(t, e, serving, candidate, 0)
}

func TestBindingPromotionRequiresWriteAndInventoryPermissions(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		status int
	}{
		{[]string{api.ScopeAppsRead}, http.StatusForbidden},
		{[]string{api.ScopeDeployWrite}, http.StatusConflict},
		{[]string{api.ScopeDeployWrite, api.ScopeAppsRead, api.ScopeManagedPostgresRead, api.ScopeStorageManage}, http.StatusOK},
	} {
		t.Run(strings.Join(tc.scopes, ","), func(t *testing.T) {
			e, app, serving, candidate := promotionFixture(t)
			completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "promotion", tc.scopes); err != nil {
				t.Fatal(err)
			}
			e.key = plain
			r := e.do(t, http.MethodPost, "/v1/deployments/"+candidate.ID+"/promote", api.BindingPromotionRequest{}, nil)
			if r.Code != tc.status {
				t.Fatalf("scopes=%v: %d %s", tc.scopes, r.Code, r.Body.String())
			}
			percent := 0
			if tc.status == http.StatusOK {
				percent = 100
			}
			assertPromotionWeights(t, e, serving, candidate, percent)
		})
	}
}
