package apidserver

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
	"time"
)

func TestWorkflowRecoveryPolicyHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := mustSeedApp(t, e, "policy-http")
	path := "/v1/apps/policy-http/recovery-policy"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var defaults api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &defaults) != nil || defaults.Version != 0 || defaults.Policy.Enabled || defaults.AppID != uuid.MustParse(app).String() {
		t.Fatalf("defaults=%d %s", rec.Code, rec.Body.String())
	}
	req := api.SetWorkflowRecoveryPolicyRequest{Policy: api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "provider incident"}}
	rec = e.do(t, http.MethodPut, path, req, nil)
	var out api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Version != 1 || out.ActorKind != "api_key" || out.ActorID == "" || !out.Policy.Enabled {
		t.Fatalf("set=%d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, http.MethodPut, path, req, nil); rec.Code != 409 {
		t.Fatalf("stale=%d %s", rec.Code, rec.Body.String())
	}
	req.ExpectedVersion = 1
	req.Policy.Mode = "automatic"
	if rec = e.do(t, http.MethodPut, path, req, nil); rec.Code != 400 {
		t.Fatalf("automatic=%d %s", rec.Code, rec.Body.String())
	}
	req.Policy.Mode = "automatic_retry"
	if rec = e.do(t, http.MethodPut, path, req, nil); rec.Code != 400 {
		t.Fatalf("unbounded automatic mode accepted=%d %s", rec.Code, rec.Body.String())
	}
	req.Policy.FailureStatuses = []string{"dead"}
	req.Policy.HourlyExecutionBudget = 2
	req.Policy.PauseAfterFailures = 2
	rec = e.do(t, http.MethodPut, path, req, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Version != 2 || out.Policy.Mode != "automatic_retry" || out.AutomaticExecution == nil || out.AutomaticExecution.PausedAt != nil {
		t.Fatalf("automatic mode=%d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, path+"/reviews", nil, nil)
	var reviews api.WorkflowRecoveryPolicyReviews
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &reviews) != nil || len(reviews.Reviews) != 0 {
		t.Fatalf("reviews=%d %s", rec.Code, rec.Body.String())
	}
	for _, suffix := range []string{"?limit=0", "?limit=101", "?after=invalid"} {
		if rec = e.do(t, http.MethodGet, path+"/reviews"+suffix, nil, nil); rec.Code != 400 {
			t.Fatalf("invalid query=%d %s", rec.Code, rec.Body.String())
		}
	}
	if rec = e.do(t, http.MethodGet, path+"/reviews/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatalf("missing review=%d %s", rec.Code, rec.Body.String())
	}
	for _, suffix := range []string{"/status", "/decision"} {
		method := http.MethodGet
		var body any
		if suffix == "/decision" {
			method = http.MethodPost
			body = api.WorkflowRecoveryReviewDecisionRequest{Decision: "approved", Reason: "reviewed"}
		}
		rec = e.do(t, method, path+"/reviews/"+uuid.NewString()+suffix, body, nil)
		if rec.Code != 404 {
			t.Fatalf("missing review operation=%d %s", rec.Code, rec.Body.String())
		}
	}
	rec = e.do(t, http.MethodPost, path+"/reviews/"+uuid.NewString()+"/decision", api.WorkflowRecoveryReviewDecisionRequest{Decision: "automatic", Reason: "reviewed"}, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid decision=%d %s", rec.Code, rec.Body.String())
	}

}

func TestWorkflowRecoveryPolicyReviewDecisionHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app := mustSeedApp(t, e, "review-http")
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send"}]}`)}
	if err = e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusDead, Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateEventRecovery(ctx, e.acct.ID, app, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", ExecutionDeadlineSeconds: 60, Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	policy := api.WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"deadline_exceeded"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "deadline incident"}
	if _, err = e.store.SetWorkflowRecoveryPolicy(ctx, e.acct.ID, app, api.SetWorkflowRecoveryPolicyRequest{Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.ProcessNextWorkflowRecoveryPolicy(ctx, time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reviews, err := e.store.ListWorkflowRecoveryPolicyReviews(ctx, e.acct.ID, app, "", 100)
	if err != nil || len(reviews.Reviews) != 1 || reviews.Reviews[0].Plan == nil {
		t.Fatalf("reviews=%+v %v", reviews, err)
	}
	path := "/v1/apps/review-http/recovery-policy/reviews/" + reviews.Reviews[0].ID
	req := api.WorkflowRecoveryReviewDecisionRequest{Decision: "approved", Reason: "reviewed timeout"}
	rec := e.do(t, http.MethodPost, path+"/decision", req, nil)
	var out api.WorkflowRecoveryPolicyReview
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.ReviewDecision == nil || out.ReviewDecision.ActorKind != "api_key" || out.ReviewDecision.ActorID == "" {
		t.Fatalf("decision=%d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("review decision must not cache")
	}
	req.Decision = "dismissed"
	rec = e.do(t, http.MethodPost, path+"/decision", req, nil)
	if rec.Code != 409 {
		t.Fatalf("conflict=%d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, path+"/status", nil, nil)
	var status api.WorkflowRecoveryReviewStatus
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &status) != nil || status.Status != "ready" || status.Outcome != "pending" || status.Complete {
		t.Fatalf("freshness=%d %s", rec.Code, rec.Body.String())
	}
	action := reviews.Reviews[0].Plan.Actions[0]
	if _, err := e.store.WorkflowRecoveryCancellation(ctx, e.acct.ID, reviews.Reviews[0].JobID, *action.Cancellation, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ProcessNextWorkflowRecoveryReviewCompletion(ctx, time.Now().UTC().Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, path+"/status", nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &status) != nil || !status.Complete || status.Outcome != "cancelled" || status.Actions[0].EvidenceSource != "cancellation_receipt" {
		t.Fatalf("final status=%d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.CompletionNotification == nil || out.CompletionNotification.Status != "no_receivers" {
		t.Fatalf("completion=%d %s", rec.Code, rec.Body.String())
	}
	live, err := e.store.GetWorkflowRun(ctx, run.ID)
	if err != nil || live.CancelledAt == nil || live.ResumeCount != 1 {
		t.Fatalf("explicit cancellation missing=%+v %v", live, err)
	}
}

func TestWorkflowRecoveryPolicyControlsHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "control-http")
	path := "/v1/apps/control-http/recovery-policy"
	policy := api.WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_retry", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 2, PauseAfterFailures: 2, RatePerSecond: 1, Reason: "bounded recovery"}
	rec := e.do(t, http.MethodPut, path, api.SetWorkflowRecoveryPolicyRequest{Policy: policy}, nil)
	var current api.WorkflowRecoveryPolicyResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &current) != nil {
		t.Fatalf("configure %d %s", rec.Code, rec.Body.String())
	}
	req := api.ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: current.Version, ExpectedUpdatedAt: current.UpdatedAt, Operation: "pause", Reason: "investigate provider"}
	rec = e.do(t, http.MethodPost, path+"/control", req, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &current) != nil || current.Status != "paused" || current.ActorKind != "api_key" {
		t.Fatalf("control %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("control must not be cached")
	}
	rec = e.do(t, http.MethodPost, path+"/control", req, nil)
	if rec.Code != 409 {
		t.Fatalf("stale %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, path+"/queue", nil, nil)
	var queue api.WorkflowRecoveryAutomaticQueue
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &queue) != nil || queue.Policy.Status != "paused" || queue.RemainingBudget != 2 || queue.Entries == nil {
		t.Fatalf("queue %d %s", rec.Code, rec.Body.String())
	}
	req.ExpectedUpdatedAt = current.UpdatedAt
	req.ExpectedPausedAt = current.AutomaticExecution.PausedAt
	req.Operation = "resume"
	rec = e.do(t, http.MethodPost, path+"/control", req, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &current) != nil || current.Status != "monitoring" || current.Version != 1 {
		t.Fatalf("resume %d %s", rec.Code, rec.Body.String())
	}
	req.Reason = ""
	rec = e.do(t, http.MethodPost, path+"/control", req, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid %d", rec.Code)
	}
}

func TestWorkflowRecoveryAutomaticSuppressionHTTP(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app := mustSeedApp(t, e, "suppression-http")
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app, WorkflowName: "recover", DefinitionSnapshot: json.RawMessage(`{"name":"recover","steps":[{"name":"send","run":"send","retry":{"max_attempts":2}}]}`)}
	if err = e.store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = e.store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "send", Status: state.WorkflowStepStatusDead, Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateEventRecovery(ctx, e.acct.ID, app, api.EventRecoveryRequest{RequestID: uuid.NewString(), WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover", Runs: []api.WorkflowRunRecoveryTarget{{RunID: run.ID}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.ProcessNextEventRecovery(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.ClaimNextDueWorkflowRun(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.StartWorkflowStep(ctx, run.ID, "send", 2, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	message := "provider unavailable"
	code := 503
	if err = e.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", state.WorkflowStepStatusDead, 2, &code, nil, &message); err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, &message); err != nil {
		t.Fatal(err)
	}
	policy := api.WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_retry", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 2, PauseAfterFailures: 2, RatePerSecond: 1, Reason: "provider incident"}
	if _, err = e.store.SetWorkflowRecoveryPolicy(ctx, e.acct.ID, app, api.SetWorkflowRecoveryPolicyRequest{Policy: policy}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.ProcessNextWorkflowRecoveryPolicy(ctx, time.Now().UTC().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reviews, err := e.store.ListWorkflowRecoveryPolicyReviews(ctx, e.acct.ID, app, "", 100)
	if err != nil || len(reviews.Reviews) != 1 || reviews.Reviews[0].AutomaticExecution == nil {
		t.Fatalf("reviews %+v %v", reviews, err)
	}
	review := reviews.Reviews[0]
	id := review.AutomaticExecution.Actions[0].ActionID
	path := "/v1/apps/suppression-http/recovery-policy/reviews/" + review.ID + "/actions/" + id + "/suppress"
	req := api.SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: review.PolicyVersion, Reason: "handled externally"}
	rec := e.do(t, http.MethodPost, path, req, nil)
	var action api.WorkflowRecoveryAutomaticAction
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &action) != nil || action.Status != "suppressed" || action.Suppression == nil || action.Suppression.ActorKind != "api_key" || action.Suppression.ActorID == "" {
		t.Fatalf("suppression %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("suppression must not be cached")
	}
	rec = e.do(t, http.MethodPost, path, req, nil)
	if rec.Code != 200 {
		t.Fatalf("replay %d %s", rec.Code, rec.Body.String())
	}
	req.Reason = "different reason"
	rec = e.do(t, http.MethodPost, path, req, nil)
	if rec.Code != 409 {
		t.Fatalf("changed %d %s", rec.Code, rec.Body.String())
	}
	req.Reason = ""
	rec = e.do(t, http.MethodPost, path, req, nil)
	if rec.Code != 400 {
		t.Fatalf("invalid %d %s", rec.Code, rec.Body.String())
	}
}
