package main

// Handler tests for GET /v1/cron-fire-now-requests/{request_id}
// (issue #791 PR-D / ADR-090 §Sub-decision 7).
//
// Coverage split:
//   - happy path: 200 + the full row shape, RequestID matches the row,
//     Status = pending, RequestedAt serialised, FinishedAt is null
//     (empty) for an unfinished row.
//   - cross-account: row owned by acct A, request from acct B → 404.
//     The body MUST be byte-identical to the missing-row branch.
//   - missing row: random UUID not in the table → 404.
//   - bad UUID: not a UUID shape → 404 (NOT 400 — closed oracle).
//   - terminal: row stamped with Status=succeeded and FinishedAt set
//     must serialise both fields.
//
// Why no 401/429 tests: the middleware is shared with listCronRuns;
// its assertions live in handlers_cron_runs_test.go.

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

func TestGetFireCronRequest_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "fire-req-happy", "0 4 * * *")
	requestID, err := e.store.InsertFireNowRequest(context.Background(), cronID, e.acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	rec := e.do(t, "GET", "/v1/cron-fire-now-requests/"+requestID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.FireCronRequestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if resp.RequestID != requestID {
		t.Errorf("request_id = %q, want %q", resp.RequestID, requestID)
	}
	if resp.CronID != cronID {
		t.Errorf("cron_id = %q, want %q", resp.CronID, cronID)
	}
	if resp.Status != string(state.FireNowStatusPending) {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.AccountID != e.acct.ID {
		t.Errorf("account_id = %q, want %q", resp.AccountID, e.acct.ID)
	}
	if resp.RequestedAt == "" {
		t.Errorf("requested_at empty; want an RFC3339Nano string")
	}
	// pending row → finished_at MUST be absent from the JSON (not
	// "0001-01-01T00:00:00Z"). Pin via FinishedAt (the *string).
	if resp.FinishedAt != nil {
		t.Errorf("finished_at = %v, want nil for an unfinished row", *resp.FinishedAt)
	}
	if resp.InvocationID != nil {
		t.Errorf("invocation_id = %v, want nil for an unfinished row", *resp.InvocationID)
	}
	if resp.Error != nil {
		t.Errorf("error = %v, want nil for an unfinished row", *resp.Error)
	}
}

func TestGetFireCronRequest_TerminalSucceeded(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "fire-req-term", "0 4 * * *")
	requestID, err := e.store.InsertFireNowRequest(context.Background(), cronID, e.acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}
	// Lifecycle: pending → running (claim) → succeeded (mark).
	// Mark* helpers require Status=Running (set by ClaimPending-
	// FireNowRequest) so the read shape sees a fully-terminal row.
	if _, err := e.store.ClaimPendingFireNowRequest(context.Background()); err != nil {
		t.Fatalf("ClaimPendingFireNowRequest: %v", err)
	}
	invID := uuid.NewString()
	if err := e.store.MarkFireNowRequestSucceeded(context.Background(), requestID, invID); err != nil {
		t.Fatalf("MarkFireNowRequestSucceeded: %v", err)
	}

	rec := e.do(t, "GET", "/v1/cron-fire-now-requests/"+requestID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.FireCronRequestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Status != string(state.FireNowStatusSucceeded) {
		t.Errorf("status = %q, want succeeded", resp.Status)
	}
	if resp.FinishedAt == nil {
		t.Errorf("finished_at nil; want a terminal stamp on a succeeded row")
	}
	if resp.InvocationID == nil || *resp.InvocationID != invID {
		t.Errorf("invocation_id = %v, want %q", resp.InvocationID, invID)
	}
}

func TestGetFireCronRequest_CommandCronIncludesTaskID(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, _ := seedAppTaskDeployment(t, e, "fire-req-command-task")
	cron, err := e.store.CreateCronWithOptions(context.Background(), app.ID, "0 0 1 1 *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	requestID, err := e.store.InsertFireNowRequest(context.Background(), cron.ID, e.acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}
	if _, err := e.store.ClaimPendingFireNowRequest(context.Background()); err != nil {
		t.Fatalf("ClaimPendingFireNowRequest: %v", err)
	}
	task, err := e.store.CreateManualCronAppTaskForFireNow(context.Background(), requestID, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateManualCronAppTaskForFireNow: %v", err)
	}

	rec := e.do(t, "GET", "/v1/cron-fire-now-requests/"+requestID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response api.FireCronRequestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if response.Status != string(state.FireNowStatusSucceeded) || response.TaskID == nil || *response.TaskID != task.ID {
		t.Fatalf("response = %+v; want command task receipt %s", response, task.ID)
	}
}

func TestGetFireCronRequest_MissingRowIs404(t *testing.T) {
	e := setup(t, api.PlanPro)
	missing := uuid.NewString()
	rec := e.do(t, "GET", "/v1/cron-fire-now-requests/"+missing, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// Pin the canonical "not_found" problem body shape — every
	// notFound call uses api.WriteProblem, so the absence of a
	// 200 here proves both the missing-row path AND the helper
	// share the same emit. The byte-equality tests below confirm
	// the cross-account + bad-uuid branches share the body.
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("body missing not_found code: %s", rec.Body.String())
	}
}

// TestGetFireCronRequest_CrossAccountIs404 mirrors the PR-C POST
// IDOR test (handlers_cron_run_test.go:102-140): account B is
// forbidden from reading account A's fire-now row, and the
// returned body must match the missing-row body byte-for-byte.
func TestGetFireCronRequest_CrossAccountIs404(t *testing.T) {
	eA := setup(t, api.PlanPro)
	cronID, _ := seedCron(t, eA, "fire-req-cross", "0 4 * * *")
	requestID, err := eA.store.InsertFireNowRequest(context.Background(), cronID, eA.acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	// Account B with their own valid session — must NOT see A's row.
	eB := setup(t, api.PlanPro)

	rec := eB.do(t, "GET", "/v1/cron-fire-now-requests/"+requestID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET status = %d, want 404; body=%s",
			rec.Code, rec.Body.String())
	}

	missing := uuid.NewString()
	missingRec := eB.do(t, "GET", "/v1/cron-fire-now-requests/"+missing, nil, nil)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing-row GET status = %d, want 404", missingRec.Code)
	}
	if rec.Body.String() != missingRec.Body.String() {
		t.Errorf("body differs (existence oracle):\n  cross-account: %s\n  missing:      %s",
			rec.Body.String(), missingRec.Body.String())
	}
}

// TestGetFireCronRequest_BadUUIDIs404 pins the closed-oracle contract:
// a non-UUID request_id returns 404 (NOT 400), so a probing customer
// cannot use the response shape to distinguish "bad id" from
// "missing id" or "wrong account".
func TestGetFireCronRequest_BadUUIDIs404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/cron-fire-now-requests/not-a-uuid-shape", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (closed oracle); body=%s",
			rec.Code, rec.Body.String())
	}
	missing := uuid.NewString()
	missingRec := e.do(t, "GET", "/v1/cron-fire-now-requests/"+missing, nil, nil)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing-row status = %d, want 404", missingRec.Code)
	}
	if rec.Body.String() != missingRec.Body.String() {
		t.Errorf("body differs:\n  bad-uuid:    %s\n  missing:     %s",
			rec.Body.String(), missingRec.Body.String())
	}
}
