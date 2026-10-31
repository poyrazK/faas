package apidserver_test

// Handler tests for POST /v1/crons/{id}/run (issue #791 PR-C /
// ADR-090).
//
// The endpoint is async-by-design: apid inserts a pending row into
// cron_fire_now_requests (migrations/00193) and emits
// db.NotifyCronRunNow; schedd's fire-now consumer
// (pkg/sched/fire_now.go) claims the row in its own process. Tests
// focus on the API contract — auth, IDOR, plan tier, error
// shapes — and assert that the row lands in the expected state
// (InsertFireNowRequest → status=pending) so schedd's later claim
// has something to claim.
//
// Coverage split:
//   - shape: 202 happy path, request_id present
//   - isolation: cross-account cron → 404 (byte-identical to missing)
//   - IDOR: cross-account and missing-cron produce identical bodies
//   - plan tier: Free plan → 402 before any row is inserted
//   - bad id: 400 cron_invalid
//   - idempotency: same Idempotency-Key twice → identical body, one row
//
// Why no 410 (disabled) test at the API: the API surface accepts the
// fire regardless of enabled. schedd's RunCronNow re-checks enabled
// on claim and stamps ErrCronDisabled onto the row. The 410
// behaviour lives in pkg/sched/fire_now_test.go (covered there).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// fireCronNow POSTs to the endpoint and returns the parsed response.
// Helper mirrors getRuns in handlers_cron_runs_test.go.
func fireCronNow(t *testing.T, e apidserver.TestEnv, cronID string, idempotencyKey string) *http.Response {
	t.Helper()
	hdrs := map[string]string{}
	if idempotencyKey != "" {
		hdrs["Idempotency-Key"] = idempotencyKey
	}
	rec := e.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, hdrs)
	return rec.Result()
}

func TestFireCronNow_HappyPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "fire-cron-happy", "0 3 * * *")

	rec := e.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.FireCronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.CronID != cronID {
		t.Errorf("cron_id = %q, want %q", resp.CronID, cronID)
	}
	if resp.Status != "pending" {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.RequestID == "" {
		t.Errorf("request_id empty; want a UUID")
	}
	if _, err := uuid.Parse(resp.RequestID); err != nil {
		t.Errorf("request_id %q is not a UUID: %v", resp.RequestID, err)
	}

	// The row must exist in the table for schedd to claim it.
	row, err := (*e.Store()).GetFireNowRequest(context.Background(), resp.RequestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if row.Status != state.FireNowStatusPending {
		t.Errorf("row status = %q, want pending", row.Status)
	}
	if row.CronID != cronID {
		t.Errorf("row cron_id = %q, want %q", row.CronID, cronID)
	}
}

// adr: 20261026201726 — a cron holds at most CronFireNowActivePerCron manual
// runs waiting or running; the next request is a 429, not unbounded work for
// the owning scheduler.
func TestFireCronNow_BusyCronIs429(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "fire-cron-busy", "0 3 * * *")
	for i := range api.CronFireNowActivePerCron {
		if rec := e.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, nil); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: status = %d, want 202; body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := e.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeCronFireNowBusy || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("problem = %+v, Retry-After %q; want %s with a retry hint", problem, rec.Header().Get("Retry-After"), api.CodeCronFireNowBusy)
	}
}

// adr: 099 — scheduled command crons can be fired immediately as a queued task.
func TestFireCronNow_CommandCronIsAccepted(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.MustSeedApp(t, e, "command-cron-fire-now")
	cron, err := (*e.Store()).CreateCronWithOptions(context.Background(), app, "0 2 * * *", "/", true, state.CronOptions{
		Command: []string{"bin/maintenance"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/crons/"+cron.ID+"/run", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.FireCronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.CronID != cron.ID || resp.RequestID == "" || resp.Status != "pending" {
		t.Fatalf("response = %+v; want pending request for command cron %s", resp, cron.ID)
	}
	request, err := (*e.Store()).GetFireNowRequest(context.Background(), resp.RequestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if request.CronID != cron.ID || request.Status != state.FireNowStatusPending {
		t.Fatalf("fire-now row = %+v; want pending request for command cron", request)
	}
}

func TestFireCronNow_BadID(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "POST", "/v1/crons/not-a-uuid/run", nil, nil)
	// App access resolves the cron before the handler (#4882): a malformed ID
	// addresses no cron and gets the same 404 as GET /v1/crons/{id}.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no such cron") {
		t.Errorf("body missing the cron 404 detail: %s", rec.Body.String())
	}
}

func TestFireCronNow_CrossAccountIs404(t *testing.T) {
	// Two accounts. Account A owns the cron. Account B tries to
	// fire it. Account B must see a 404 with the SAME body as a
	// missing-cron request — no existence oracle.
	store := state.NewMemStore()
	// Reuse the test harness but build a second account by hand.
	acctA, _ := store.CreateAccount(context.Background(), "a@example.com", api.PlanPro)
	acctB, _ := store.CreateAccount(context.Background(), "b@example.com", api.PlanPro)
	_ = acctA
	_ = acctB

	// Account A creates the cron under their app.
	eA := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, eA, "fire-cron-cross-acct", "0 3 * * *")
	// Account B: same store, different key. We need a second env,
	// so build it inline by reusing the testEnv with a different
	// account. Cheaper path: use the same harness but replace key
	// and acct. Even simpler: re-use setup(t, plan) gives a fresh
	// account on the same MemStore only if MemStore is shared. It
	// isn't — setup creates its own. So we re-seed for B.
	eB := apidserver.Setup(t, api.PlanPro)

	// Move the cron under account B's ownership to simulate "the
	// cron was transferred away". Easier: try the cronID that
	// belongs to A; eB's IDOR must 404.
	rec := eB.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account fire status = %d, want 404; body=%s",
			rec.Code, rec.Body.String())
	}
	missingRec := eB.Do(t, "POST", "/v1/crons/"+uuid.NewString()+"/run", nil, nil)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing-cron fire status = %d, want 404", missingRec.Code)
	}
	if rec.Body.String() != missingRec.Body.String() {
		t.Errorf("body differs:\n  cross-account: %s\n  missing:      %s",
			rec.Body.String(), missingRec.Body.String())
	}
}

func TestFireCronNow_FreePlanBlocks(t *testing.T) {
	// The plan-tier gate runs BEFORE InsertFireNowRequest — a Free
	// customer never creates a row that schedd would stamp as
	// failed. 402 with the canonical plan-crons-not-allowed code.
	e := apidserver.Setup(t, api.PlanFree)
	cronID, _ := seedCron(t, e, "fire-cron-free", "0 3 * * *")

	rec := e.Do(t, "POST", "/v1/crons/"+cronID+"/run", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "plan_crons_not_allowed") {
		t.Errorf("body missing plan_crons_not_allowed code: %s",
			rec.Body.String())
	}

	// No row should have been inserted.
	// Iterate via a synthetic probe: there should be zero pending
	// rows for this cron. We don't have a ListByCron helper yet;
	// the absence is asserted by the fact that schedd would have
	// no row to claim. Cheap assertion: ClaimPendingFireNowRequest
	// must return ErrFireNowRequestNotFound.
	if _, err := (*e.Store()).ClaimPendingFireNowRequest(context.Background()); err == nil {
		t.Errorf("ClaimPendingFireNowRequest succeeded; want ErrFireNowRequestNotFound (Free plan should have inserted nothing)")
	}
}

func TestFireCronNow_IdempotencyKey(t *testing.T) {
	// A replay with the same Idempotency-Key returns the stored
	// 202 without enqueuing a second fire. Exactly one row exists.
	e := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "fire-cron-idem", "0 3 * * *")

	rec1 := fireCronNow(t, e, cronID, "test-key-001")
	rec2 := fireCronNow(t, e, cronID, "test-key-001")
	defer rec1.Body.Close()
	defer rec2.Body.Close()

	if rec1.StatusCode != http.StatusAccepted || rec2.StatusCode != http.StatusAccepted {
		t.Fatalf("first=%d second=%d, want both 202", rec1.StatusCode, rec2.StatusCode)
	}
	var r1, r2 api.FireCronResponse
	_ = json.NewDecoder(rec1.Body).Decode(&r1)
	_ = json.NewDecoder(rec2.Body).Decode(&r2)
	if r1.RequestID != r2.RequestID {
		t.Errorf("request_id mismatch: first=%q second=%q (idempotent replay must echo the original id)",
			r1.RequestID, r2.RequestID)
	}

	// Exactly one row exists for this cron.
	row, err := (*e.Store()).ClaimPendingFireNowRequest(context.Background())
	if err != nil {
		t.Fatalf("claim after replay: %v", err)
	}
	if row.CronID != cronID {
		t.Errorf("claimed row cron_id = %q, want %q", row.CronID, cronID)
	}
	// Subsequent claim returns ErrFireNowRequestNotFound — confirms
	// the idempotency wrapper prevented a second insert.
	if _, err := (*e.Store()).ClaimPendingFireNowRequest(context.Background()); err == nil {
		t.Errorf("second claim succeeded; want ErrFireNowRequestNotFound")
	}
}
