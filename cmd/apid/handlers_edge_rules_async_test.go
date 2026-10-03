package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func asyncEdgeRuleRequest() api.CreateEdgeRuleRequest {
	return api.CreateEdgeRuleRequest{
		MatchHost:    "api.example.com",
		MatchPath:    "/reports",
		MatchMethods: []string{"POST"},
		Kind:         string(state.EdgeRuleKindAsync),
		Action:       json.RawMessage(`{}`),
	}
}

func TestCreateEdgeRuleAsyncRoundTrip(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "reports")
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", asyncEdgeRuleRequest(), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var response api.EdgeRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Kind != string(state.EdgeRuleKindAsync) {
		t.Errorf("kind = %q, want async", response.Kind)
	}
	if string(response.Action) != `{"kind":"async","async":{}}` {
		t.Errorf("action = %s, want async envelope", response.Action)
	}
}

func TestCreateEdgeRuleAsyncExecutionPolicyRoundTrip(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "reports-policy")
	req := asyncEdgeRuleRequest()
	req.Action = json.RawMessage(`{"retry_policy":{"max_attempts":4,"base_seconds":1,"max_seconds":30,"jitter_seconds":0.2},"max_age_seconds":600}`)
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var response api.EdgeRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var persisted state.EdgeRuleAction
	if err := json.Unmarshal(response.Action, &persisted); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if persisted.Async == nil || persisted.Async.RetryPolicy == nil {
		t.Fatalf("persisted async action = %+v", persisted.Async)
	}
	if got := persisted.Async.RetryPolicy; got.MaxAttempts != 4 || got.BaseSeconds != 1 || got.MaxSeconds != 30 || got.JitterSeconds != 0.2 {
		t.Errorf("persisted retry policy = %+v", got)
	}
	if persisted.Async.MaxAgeSeconds != 600 {
		t.Errorf("max_age_seconds = %d, want 600", persisted.Async.MaxAgeSeconds)
	}
}

func TestCreateEdgeRuleAsyncRequiresPaidPlan(t *testing.T) {
	e := setup(t, api.PlanFree)
	slug := mustSeedEdgeRuleApp(t, e, "free-reports")
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", asyncEdgeRuleRequest(), nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateEdgeRuleAsyncRejectsWorker(t *testing.T) {
	e := setup(t, api.PlanHobby)
	_, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID, Slug: "report-worker", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/report-worker/edge-rules", asyncEdgeRuleRequest(), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidateEdgeRuleAsyncActionRejectsFields(t *testing.T) {
	if problem := validateEdgeRuleAction(string(state.EdgeRuleKindAsync), json.RawMessage(`{}`), api.PlanHobby); problem != nil {
		t.Fatalf("empty action rejected: %+v", problem)
	}
	if problem := validateEdgeRuleAction(string(state.EdgeRuleKindAsync), json.RawMessage(`{"queue":"custom"}`), api.PlanHobby); problem == nil {
		t.Fatal("async action with unsupported field was accepted")
	}
	for _, raw := range []string{
		`{"max_age_seconds":-1}`,
		`{"max_age_seconds":86401}`,
		`{"retry_policy":{"max_attempts":-1}}`,
		`{"retry_policy":{"unknown":1}}`,
	} {
		if problem := validateEdgeRuleAction(string(state.EdgeRuleKindAsync), json.RawMessage(raw), api.PlanHobby); problem == nil {
			t.Errorf("async action %s was accepted", raw)
		}
	}
}
