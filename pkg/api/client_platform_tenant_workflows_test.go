package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlatformTenantWorkflowClientRoutes(t *testing.T) {
	const tenantID = "11111111-1111-4111-8111-111111111111"
	const runID = "22222222-2222-4222-8222-222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("authorization absent")
		}
		if r.Method == http.MethodPost && r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("idempotency key absent for %s", r.URL.Path)
		}

		switch r.Method + " " + r.URL.Path {
		case "GET /v1/platform-tenant-self/apps/shop/workflows/runs":
			q := r.URL.Query()
			if q.Get("limit") != "5" || q.Get("offset") != "2" || q.Get("status") != "failed" ||
				q.Get("workflow_name") != "process-order" || q.Get("created_after") != "2026-10-01T00:00:00Z" {
				t.Errorf("tenant run-list query = %v", q)
			}
			_, _ = w.Write([]byte(`{"runs":[{"id":"` + runID + `","status":"failed"}],"total":1}`))
		case "POST /v1/account/platform-tenants/" + tenantID + "/apps/shop/workflows/process-order/runs",
			"POST /v1/platform-tenant-self/apps/shop/workflows/process-order/runs":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["order_id"] != "ord_42" {
				t.Errorf("workflow input = %v, err = %v", body, err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"` + runID + `","status":"pending"}`))
		case "GET /v1/platform-tenant-self/workflows/runs/" + runID:
			_, _ = w.Write([]byte(`{"id":"` + runID + `","status":"running"}`))
		case "POST /v1/platform-tenant-self/workflows/runs/" + runID + "/cancel":
			_, _ = w.Write([]byte(`{"id":"` + runID + `","status":"cancelled"}`))
		case "POST /v1/platform-tenant-self/workflows/runs/" + runID + "/resume":
			var body ResumeWorkflowRunRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedResumeCount == nil || *body.ExpectedResumeCount != 3 {
				t.Errorf("resume request = %+v, err = %v", body, err)
			}
			_, _ = w.Write([]byte(`{"id":"` + runID + `","status":"pending","resume_count":4}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	input := json.RawMessage(`{"order_id":"ord_42"}`)
	created, err := client.CreateTenantWorkflowRun(context.Background(), tenantID, "shop", "process-order", input)
	if err != nil || created.ID != runID || created.Status != "pending" {
		t.Fatalf("account-created run = %+v, err = %v", created, err)
	}
	created, err = client.CreatePlatformTenantSelfWorkflowRun(context.Background(), "shop", "process-order", input)
	if err != nil || created.ID != runID || created.Status != "pending" {
		t.Fatalf("tenant-created run = %+v, err = %v", created, err)
	}
	got, err := client.GetPlatformTenantSelfWorkflowRun(context.Background(), runID)
	if err != nil || got.Status != "running" {
		t.Fatalf("tenant run = %+v, err = %v", got, err)
	}
	createdAfter := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	listed, err := client.ListPlatformTenantSelfWorkflowRuns(context.Background(), "shop", WorkflowRunListOptions{
		Limit: 5, Offset: 2, Status: "failed", WorkflowName: "process-order", CreatedAfter: &createdAfter,
	})
	if err != nil || listed.Total != 1 || len(listed.Runs) != 1 || listed.Runs[0].ID != runID {
		t.Fatalf("tenant run history = %+v, err = %v", listed, err)
	}
	got, err = client.CancelPlatformTenantSelfWorkflowRun(context.Background(), runID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("cancelled tenant run = %+v, err = %v", got, err)
	}
	resumeCount := 3
	got, err = client.ResumePlatformTenantSelfWorkflowRun(context.Background(), runID, ResumeWorkflowRunRequest{ExpectedResumeCount: &resumeCount})
	if err != nil || got.Status != "pending" || got.ResumeCount != 4 {
		t.Fatalf("resumed tenant run = %+v, err = %v", got, err)
	}
}
