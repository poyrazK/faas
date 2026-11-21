package apidserver_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardRunPipelinesHistoryAndRecovery(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "report-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "other-report-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	manager, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	value, err := apidserver.MintDashboardSession(ctx, store, manager, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionCookie := &http.Cookie{Name: apidserver.SessionCookie, Value: value}
	server := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{},
		"", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, manager, nil, 15*time.Minute, "").
		WithExecutionAPIEnabled(true).WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true).WithExecutionPipelinesEnabled(true)
	h := server.Handler()
	manifest := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "report-jan", Version: "v1",
		Pipeline: &api.ExecutionPipelineOptions{}, Steps: []api.CreateManagedExecutionWorkflowStep{
			{Label: "north", Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default () => 'example'"}},
			{Label: "merge", DependsOn: []string{"north"}, Request: api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default () => 'example'"}},
		}}
	normalized, err := runpipeline.Normalize(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	policy := state.ExecutionPipelinePolicy{ManifestDigest: hex.EncodeToString(digest[:]), SubmissionDigest: hex.EncodeToString(digest[:]),
		TotalTimeoutMS: 120000, Steps: []state.ExecutionPipelineStep{
			{Label: "north", Retry: api.ExecutionRetryPolicy{MaxAttempts: 1, BackoffMS: api.ExecutionPipelineMinBackoffMS}},
			{Label: "merge", Retry: api.ExecutionRetryPolicy{MaxAttempts: 1, BackoffMS: api.ExecutionPipelineMinBackoffMS}},
		}}
	now := time.Now().UTC()
	const filename = "reports/summary #1.json"
	report := []byte(`{"orders":4}`)
	reportDigest := sha256.Sum256(report)
	artifact := api.ExecutionArtifact{Name: filename, SizeBytes: len(report), SHA256: "sha256:" + hex.EncodeToString(reportDigest[:]), Content: report}
	outputRequest := manifest.Steps[0].Request
	outputRequest.OutputFiles = []string{filename}
	resolved, problem := outputRequest.Resolve(account.Plan)
	if problem != nil {
		t.Fatal(problem)
	}
	admitted := now.Add(-time.Second)
	produced, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account.ID, RunsPrincipalID: &account.ID,
		Request: resolved, SourceBytes: resolved.SourceBytes(), InputBytes: len(resolved.Input), SealedPayload: []byte("sealed"), PayloadKID: "fixture",
		AdmittedAt: admitted, DeadlineAt: admitted.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := store.ClaimExecutionForAccount(ctx, account.ID, "fixture", admitted.Add(time.Millisecond), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.MarkExecutionRunning(ctx, produced.ID, *guest.LeaseToken, admitted.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteExecution(ctx, state.CompleteExecutionParams{ID: produced.ID, LeaseToken: *guest.LeaseToken,
		Status: api.ExecutionStatusSucceeded, Result: report, Artifacts: []api.ExecutionArtifact{artifact}, FinishedAt: admitted.Add(3 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	create := func(acct state.Account, name string, at time.Time) state.ExecutionWorkflowJob {
		t.Helper()
		job, err := store.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{
			AccountID: acct.ID, RunsPrincipalID: &acct.ID, WorkflowID: name, PlanID: strings.Repeat("a", 24), StepCount: 2,
			SealedPlan: []byte("sealed"), PayloadKID: "fixture", CreatedAt: at,
		}, policy)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	parent := create(account, manifest.WorkflowID, now)
	claim, err := store.ClaimExecutionPipeline(ctx, "fixture", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	err = store.UpdateExecutionPipeline(ctx, parent.ID, claim.ClaimToken, state.ExecutionWorkflowJobUpdate{
		Status: api.ManagedExecutionWorkflowFailed, UpdatedAt: now, ScheduledFor: now, PipelineCompletion: &api.ExecutionPipelineCompletion{
			Version: "v1", CompletionID: parent.ID, WorkflowID: parent.WorkflowID, Outcome: "failed", FinishedAt: now.Format(time.RFC3339Nano),
			Steps: []api.ExecutionPipelineCompletedStep{
				{Label: "north", Outcome: "succeeded", Attempts: []api.ExecutionPipelineCompletedAttempt{{
					RunID: produced.ID, Status: api.ExecutionStatusSucceeded, Attempt: 1,
					Artifacts: []api.ExecutionPipelineArtifactRef{{Name: artifact.Name, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256}},
				}}},
				{Label: "merge", Outcome: "failed", Attempts: []api.ExecutionPipelineCompletedAttempt{{RunID: runID, Status: api.ExecutionStatusFailed, Attempt: 1}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := create(other, "private-foreign-report", now.Add(time.Second))
	const path = "/dashboard/run-pipelines"
	page := dashboardGET(t, h, sessionCookie, path)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "report-jan") ||
		strings.Contains(page.Body.String(), "private-foreign-report") || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("history returned %d: %s", page.Code, page.Body.String())
	}
	if foreignPage := dashboardGET(t, h, sessionCookie, path+"/"+foreign.ID); foreignPage.Code != http.StatusNotFound {
		t.Fatalf("foreign pipeline returned %d", foreignPage.Code)
	}
	detailPath := path + "/" + parent.ID
	detail := dashboardGET(t, h, sessionCookie, detailPath)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), filename) || !strings.Contains(detail.Body.String(), runID) {
		t.Fatalf("detail returned %d: %s", detail.Code, detail.Body.String())
	}
	for _, want := range []string{`id="pipeline-new-runs">2`, "Run output ready (UTC)", "Unavailable", "Per-pipeline compute and artifact storage charges are unavailable."} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Errorf("pipeline usage missing %q", want)
		}
	}
	downloadPath := "/runs/" + produced.ID + "/artifacts/reports%2Fsummary%20%231.json"
	download := dashboardGET(t, h, sessionCookie, detailPath+downloadPath)
	if download.Code != http.StatusOK || download.Body.String() != string(report) ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download returned %d: %s", download.Code, download.Body.String())
	}
	if denied := dashboardGET(t, h, sessionCookie, path+"/"+foreign.ID+downloadPath); denied.Code != http.StatusNotFound {
		t.Fatalf("artifact on another pipeline returned %d", denied.Code)
	}
	var csrf *http.Cookie
	for _, cookie := range detail.Result().Cookies() {
		if cookie.Name == "dashboard_pipeline_csrf" {
			csrf = cookie
		}
	}
	if csrf == nil || csrf.Path != detailPath || !csrf.HttpOnly {
		t.Fatal("missing scoped rerun proof")
	}
	preview := func(proof string, manifest []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://gregale.dev"+detailPath+"/rerun-preview", strings.NewReader(string(manifest)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		r.Header.Set("X-CSRF-Token", proof)
		r.AddCookie(sessionCookie)
		r.AddCookie(csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if rejected := preview("", raw); rejected.Code < 400 {
		t.Fatalf("missing approval proof returned %d", rejected.Code)
	}
	response := preview(csrf.Value, raw)
	var planned runpipeline.RerunPlan
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &planned) != nil ||
		strings.Join(planned.RunSteps, ",") != "merge" || strings.Join(planned.ReuseSteps, ",") != "north" ||
		strings.Join(planned.RepeatSteps, ",") != "merge" {
		t.Fatalf("preview returned %d: %s", response.Code, response.Body.String())
	}
	wrong := strings.Replace(string(raw), "example", "changed", 1)
	if rejected := preview(csrf.Value, []byte(wrong)); rejected.Code != http.StatusConflict {
		t.Fatalf("changed manifest returned %d: %s", rejected.Code, rejected.Body.String())
	}
	if child := dashboardGET(t, h, sessionCookie, path); strings.Count(child.Body.String(), detailPath) != 1 {
		t.Fatal("review unexpectedly created a child")
	}
}
