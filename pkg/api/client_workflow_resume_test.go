package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientWorkflowResumeRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("authorization absent")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/workflows/runs/run/resume":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("resume idempotency key absent")
			}
			var body ResumeWorkflowRunRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedResumeCount == nil || *body.ExpectedResumeCount != 0 {
				t.Error("request revision absent", err)
			}
			_, _ = w.Write([]byte(`{"id":"run","status":"pending","resume_count":1}`))
		case "GET /v1/workflows/runs/run/resumes":
			_, _ = w.Write([]byte(`{"resumes":[{"run_id":"run","resume_number":1,"account_id":"account","previous_status":"dead","resumed_steps":["send"],"created_at":"2026-10-03T12:00:00Z"}]}`))
		default:
			t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	zero := 0
	run, err := client.ResumeWorkflowRun(context.Background(), "run", ResumeWorkflowRunRequest{ExpectedResumeCount: &zero})
	if err != nil || run.ResumeCount != 1 {
		t.Fatal(run, err)
	}
	history, err := client.ListWorkflowResumes(context.Background(), "run")
	if err != nil || len(history.Resumes) != 1 || history.Resumes[0].ResumedSteps[0] != "send" {
		t.Fatal(history, err)
	}
}

func TestClientResumeWorkflowRunWithIdempotencyKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workflows/runs/run/resume" {
			t.Errorf("request route = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "resume-after-outage-1" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":"run","status":"pending","resume_count":1}`))
	}))
	defer server.Close()
	zero := 0
	run, err := NewClient(server.URL, "token").ResumeWorkflowRunWithIdempotencyKey(
		context.Background(), "run", ResumeWorkflowRunRequest{ExpectedResumeCount: &zero}, "resume-after-outage-1",
	)
	if err != nil || run.ResumeCount != 1 {
		t.Fatalf("ResumeWorkflowRunWithIdempotencyKey() = (%+v, %v)", run, err)
	}
}
