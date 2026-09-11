package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedOperatorJobRun(t *testing.T, store *state.MemStore) (state.Account, state.Job, state.JobRun) {
	t.Helper()
	tenant, err := store.CreateAccount(context.Background(), "jobs-tenant@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(context.Background(), tenant.ID, "capacity-export", "batch",
		"ghcr.io/private/worker:secret", []string{"/bin/private", "secret-arg"},
		768, 300, 3, 1, json.RawMessage(`{"SECRET":"value"}`))
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(context.Background(), job.ID, tenant.ID, "manual",
		nil, nil, nil, json.RawMessage(`{"RUN_SECRET":"value"}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, job, run
}

func TestOperatorJobsActiveAndInspectUseSafeBoundedProjection(t *testing.T) {
	srv, store, cookie := newForceHarness(t, nil)
	tenant, _, run := seedOperatorJobRun(t, store)

	activeReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/ops/jobs/runs?account_id="+tenant.ID+"&limit=10", nil)
	activeReq.AddCookie(cookie)
	activeRec := httptest.NewRecorder()
	srv.handler().ServeHTTP(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("active status = %d; body=%s", activeRec.Code, activeRec.Body.String())
	}
	var active api.OperatorJobRunListResponse
	if err := json.Unmarshal(activeRec.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if len(active.Runs) != 1 || active.Runs[0].RunID != run.ID || active.Runs[0].RAMMB != 768 {
		t.Fatalf("active response = %+v", active)
	}
	for _, forbidden := range []string{"image_ref", "env_overrides", "command", "secret-arg", "RUN_SECRET"} {
		if strings.Contains(activeRec.Body.String(), forbidden) {
			t.Fatalf("active response leaked %q: %s", forbidden, activeRec.Body.String())
		}
	}

	inspectReq := httptest.NewRequest(http.MethodGet,
		"/v1/admin/ops/jobs/runs/"+run.ID+"?task_limit=2", nil)
	inspectReq.AddCookie(cookie)
	inspectRec := httptest.NewRecorder()
	srv.handler().ServeHTTP(inspectRec, inspectReq)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("inspect status = %d; body=%s", inspectRec.Code, inspectRec.Body.String())
	}
	var detail api.OperatorJobRunDetailResponse
	if err := json.Unmarshal(inspectRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Tasks) != 2 || detail.NextTaskOffset != 2 || detail.Run.RunID != run.ID {
		t.Fatalf("inspect response = %+v", detail)
	}
	if strings.Contains(inspectRec.Body.String(), "lease_token") {
		t.Fatalf("inspect response leaked task lease: %s", inspectRec.Body.String())
	}
}

func TestOperatorJobsCancelIsGuardedAuditedAndAtomic(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	srv, store, cookie := newForceHarness(t, nil)
	tenant, _, run := seedOperatorJobRun(t, store)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/ops/jobs/runs/"+run.ID+"/cancel?confirm=true&reason=jobs_queue_incident", nil)
	req.AddCookie(cookie)
	req.Header.Set("Idempotency-Key", "operator-job-cancel-test")
	req.Header.Set("X-Trace-Id", traceID)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Trace-Id") != traceID {
		t.Fatalf("trace header = %q", rec.Header().Get("X-Trace-Id"))
	}
	got, err := store.JobRunGetByID(context.Background(), run.ID)
	if err != nil || got.AggregateStatus != "cancelled" || got.TasksCancelled != 3 {
		t.Fatalf("cancelled run = %+v, err=%v", got, err)
	}
	var response api.OperatorJobRunCancelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Run.AccountID != tenant.ID || response.Reason != "jobs_queue_incident" || response.CancelledAt == "" {
		t.Fatalf("cancel response = %+v", response)
	}
	events, err := store.ListEvents(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Kind == "operator.action.cancel_job_run" && event.TraceID != nil && *event.TraceID == traceID {
			found = true
		}
	}
	if !found {
		t.Fatalf("trace-linked cancellation audit event not found: %+v", events)
	}
}

func TestOperatorJobsCancelRequiresConfirmationAndReason(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "confirmation", query: "reason=jobs_queue_incident"},
		{name: "reason", query: "confirm=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, store, cookie := newForceHarness(t, nil)
			_, _, run := seedOperatorJobRun(t, store)
			req := httptest.NewRequest(http.MethodPost,
				"/v1/admin/ops/jobs/runs/"+run.ID+"/cancel?"+tc.query, nil)
			req.AddCookie(cookie)
			req.Header.Set("Idempotency-Key", "operator-job-cancel-validation-"+tc.name)
			rec := httptest.NewRecorder()
			srv.handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
