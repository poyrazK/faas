package main

// Handler tests for GET /v1/crons/{id}/runs (issue #791).
//
// The endpoint is the per-cron execution history that /v1/invocations
// cannot express: it filters on cron_id, computes duration_ms
// server-side, and normalizes the terminal state into an outcome so a
// timeout reads differently from a generic failure.
//
// Coverage split:
//   - shape: happy path, duration, running rows, ordering, paging
//   - isolation: rows of a sibling cron never leak into the page
//   - IDOR: cross-account and app-transferred-away both 404
//
// The IDOR pair mirrors TestReplayInvocation_CrossAccount /
// _AppTransferredAway (handlers_invocations_test.go) — the cron read
// runs the same CronByID → AppByID → AccountID check, so it needs the
// same tripwires.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedCron creates a cron on a freshly-seeded app and returns both ids.
func seedCron(t *testing.T, e testEnv, slug, schedule string) (cronID, appID string) {
	t.Helper()
	appID = mustSeedApp(t, e, slug)
	c, err := e.store.CreateCron(context.Background(), appID, schedule, "/cron", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	return c.ID, appID
}

// seedCronRun drives one invocation row through the real lifecycle so
// the outcome column is written by production code, not by the test.
// terminal selects the ending: "success", "failed", "timeout",
// "uncertain", or "running" (claimed but never terminated).
func seedCronRun(t *testing.T, e testEnv, cronID, appID, terminal string, createdAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	id := cronID
	inv, err := e.store.EnqueueInvocation(ctx, state.Invocation{
		AppID:     appID,
		AccountID: e.acct.ID,
		Source:    state.InvocationCron,
		Method:    "POST",
		Path:      "/cron",
		CronID:    &id,
		DueAt:     createdAt,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := e.store.ClaimInvocation(ctx, inv.ID, "inst-"+terminal, 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	switch terminal {
	case "success":
		if err := e.store.CompleteInvocation(ctx, inv.ID, nil); err != nil {
			t.Fatalf("CompleteInvocation: %v", err)
		}
	case "failed":
		if err := e.store.FailInvocation(ctx, inv.ID, "boom", 0, 0); err != nil {
			t.Fatalf("FailInvocation: %v", err)
		}
	case "timeout":
		if err := e.store.FailInvocation(ctx, inv.ID, "invoke: deadline", 0, 0,
			state.WithOutcome(state.OutcomeTimeout)); err != nil {
			t.Fatalf("FailInvocation timeout: %v", err)
		}
	case "uncertain":
		if err := e.store.FailInvocation(ctx, inv.ID, "invoke receipt uncertain", 0, 0,
			state.WithOutcome(state.OutcomeUncertain)); err != nil {
			t.Fatalf("FailInvocation uncertain: %v", err)
		}
	case "running":
		// Leave it dispatching.
	default:
		t.Fatalf("unknown terminal %q", terminal)
	}
	return inv.ID
}

func getRuns(t *testing.T, e testEnv, path string) api.ListCronRunsResponse {
	t.Helper()
	rec := e.do(t, "GET", path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status %d: %s", path, rec.Code, rec.Body)
	}
	var out api.ListCronRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestListCronRuns_HappyPath pins the headline shape: newest-first
// ordering and a server-computed duration on a completed run.
func TestListCronRuns_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "cron-runs-happy", "0 */6 * * *")
	base := time.Now().UTC().Add(-2 * time.Hour)
	seedCronRun(t, e, cronID, appID, "success", base)
	seedCronRun(t, e, cronID, appID, "success", base.Add(time.Hour))

	out := getRuns(t, e, "/v1/crons/"+cronID+"/runs")
	if len(out.Runs) != 2 {
		t.Fatalf("got %d runs, want 2: %+v", len(out.Runs), out.Runs)
	}
	// Newest first.
	if !out.Runs[0].StartedAt.After(out.Runs[1].StartedAt) {
		t.Errorf("runs not newest-first: %v then %v", out.Runs[0].StartedAt, out.Runs[1].StartedAt)
	}
	for i, r := range out.Runs {
		if r.Outcome != api.CronRunSuccess {
			t.Errorf("run %d outcome = %q, want success", i, r.Outcome)
		}
		if r.DurationMs == nil {
			t.Errorf("run %d duration_ms = nil, want a computed value on a completed run", i)
		}
		if r.CompletedAt == nil {
			t.Errorf("run %d completed_at = nil, want set on a completed run", i)
		}
	}
}

// TestListCronRuns_OutcomeVariants is the load-bearing test for the
// feature: a timeout must be distinguishable from a generic failure,
// which is exactly what the pre-#791 schema could not express. An
// in-flight row reports "running" rather than an empty outcome.
func TestListCronRuns_OutcomeVariants(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "cron-runs-outcomes", "0 */6 * * *")
	base := time.Now().UTC().Add(-3 * time.Hour)
	seedCronRun(t, e, cronID, appID, "failed", base)
	seedCronRun(t, e, cronID, appID, "timeout", base.Add(time.Hour))
	seedCronRun(t, e, cronID, appID, "uncertain", base.Add(2*time.Hour))
	seedCronRun(t, e, cronID, appID, "running", base.Add(3*time.Hour))

	out := getRuns(t, e, "/v1/crons/"+cronID+"/runs")
	if len(out.Runs) != 4 {
		t.Fatalf("got %d runs, want 4", len(out.Runs))
	}
	// Newest first: running, uncertain, timeout, failed.
	want := []api.CronRunOutcome{api.CronRunRunning, api.CronRunUncertain, api.CronRunTimeout, api.CronRunFailed}
	for i, w := range want {
		if out.Runs[i].Outcome != w {
			t.Errorf("run %d outcome = %q, want %q", i, out.Runs[i].Outcome, w)
		}
	}
	// An in-flight run has no duration to report yet.
	if out.Runs[0].DurationMs != nil {
		t.Errorf("running run duration_ms = %v, want nil", *out.Runs[0].DurationMs)
	}
	// The failure text rides along, but outcome is the branchable field.
	if out.Runs[3].Error == "" {
		t.Error("failed run error = empty, want the operator-facing text")
	}
}

// TestListCronRuns_FiltersBySibling: two crons on the same app must
// not see each other's runs. Without the cron_id predicate this
// silently returns the union.
func TestListCronRuns_FiltersBySibling(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronA, appID := seedCron(t, e, "cron-runs-filter", "0 */6 * * *")
	cronB, err := e.store.CreateCron(context.Background(), appID, "0 * * * *", "/other", true)
	if err != nil {
		t.Fatalf("CreateCron B: %v", err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	wantID := seedCronRun(t, e, cronA, appID, "success", base)
	seedCronRun(t, e, cronB.ID, appID, "success", base.Add(time.Minute))
	seedCronRun(t, e, cronB.ID, appID, "failed", base.Add(2*time.Minute))

	out := getRuns(t, e, "/v1/crons/"+cronA+"/runs")
	if len(out.Runs) != 1 {
		t.Fatalf("got %d runs for cron A, want 1 (sibling rows leaked): %+v", len(out.Runs), out.Runs)
	}
	if out.Runs[0].ID != wantID {
		t.Errorf("run id = %s, want %s", out.Runs[0].ID, wantID)
	}
}

// TestListCronRuns_LimitAndCursor walks the page boundary: ?limit
// truncates, and ?before= resumes strictly older than the cursor.
func TestListCronRuns_LimitAndCursor(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, appID := seedCron(t, e, "cron-runs-paging", "0 */6 * * *")
	base := time.Now().UTC().Add(-10 * time.Hour)
	for i := range 5 {
		seedCronRun(t, e, cronID, appID, "success", base.Add(time.Duration(i)*time.Hour))
	}

	first := getRuns(t, e, "/v1/crons/"+cronID+"/runs?limit=2")
	if len(first.Runs) != 2 {
		t.Fatalf("limit=2 returned %d runs", len(first.Runs))
	}
	next := getRuns(t, e, "/v1/crons/"+cronID+"/runs?limit=2&before="+first.Runs[1].ID)
	if len(next.Runs) != 2 {
		t.Fatalf("second page returned %d runs, want 2", len(next.Runs))
	}
	// Pages must not overlap.
	for _, a := range first.Runs {
		for _, b := range next.Runs {
			if a.ID == b.ID {
				t.Fatalf("run %s appeared on both pages", a.ID)
			}
		}
	}
	if !first.Runs[1].StartedAt.After(next.Runs[0].StartedAt) {
		t.Errorf("cursor did not advance: page1 tail %v, page2 head %v",
			first.Runs[1].StartedAt, next.Runs[0].StartedAt)
	}
}

// TestListCronRuns_EmptyIsArray: a cron that has never fired returns
// an empty array, not null. Clients iterate without a nil guard.
func TestListCronRuns_EmptyIsArray(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "cron-runs-empty", "0 */6 * * *")
	rec := e.do(t, "GET", "/v1/crons/"+cronID+"/runs", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["runs"]) != "[]" {
		t.Errorf("runs = %s, want []", raw["runs"])
	}
}

// TestListCronRuns_UnknownID: an id that does not exist is a 404,
// with no hint that it merely belongs to someone else.
func TestListCronRuns_UnknownID(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/crons/nonexistent-id/runs", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

// TestListCronRuns_LimitValidation: a malformed or out-of-range
// limit must surface as a 400 Problem — the previous shape silently
// substituted the default on garbage input, which hid client bugs.
// Mirrors the validation contract of GET /v1/invoices.
func TestListCronRuns_LimitValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "cron-runs-bad-limit", "0 */6 * * *")

	cases := []struct {
		name, q string
	}{
		{"non-numeric", "?limit=abc"},
		{"zero", "?limit=0"},
		{"negative", "?limit=-3"},
		{"over cap", "?limit=101"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, "GET", "/v1/crons/"+cronID+"/runs"+tc.q, nil, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
			var prob map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
				t.Fatalf("unmarshal problem: %v", err)
			}
			// The limit + observed pair from WithLimit should be
			// present so an RFC 7807 client can render "cap 100,
			// you sent N" without reading the message body.
			if _, ok := prob["limit"]; !ok {
				t.Errorf("problem body missing 'limit' field; got %s", rec.Body)
			}
			if _, ok := prob["observed"]; !ok {
				t.Errorf("problem body missing 'observed' field; got %s", rec.Body)
			}
		})
	}
}

// TestListCronRuns_CrossAccount is the IDOR tripwire: a stranger
// holding a valid cron id must get 404, never 403 and never a page.
func TestListCronRuns_CrossAccount(t *testing.T) {
	owner := setup(t, api.PlanPro)
	cronID, appID := seedCron(t, owner, "cron-runs-owned", "0 */6 * * *")
	seedCronRun(t, owner, cronID, appID, "success", time.Now().UTC().Add(-time.Hour))

	foreign := setup(t, api.PlanPro)
	rec := foreign.do(t, "GET", "/v1/crons/"+cronID+"/runs", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account read status = %d, want 404 (IDOR-safe); body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestListCronRuns_AppTransferredAway mirrors
// TestReplayInvocation_AppTransferredAway: checking only the cron row
// is not enough — if the cron's app has moved to another account, the
// read must 404. Pins the AppByID half of the two-step check.
func TestListCronRuns_AppTransferredAway(t *testing.T) {
	e := setup(t, api.PlanPro)
	foreignApp, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: "00000000-0000-0000-0000-000000000099",
		Slug:      "cron-foreign-app",
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp foreign: %v", err)
	}
	c, err := e.store.CreateCron(context.Background(), foreignApp.ID, "0 * * * *", "/cron", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	rec := e.do(t, "GET", "/v1/crons/"+c.ID+"/runs", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("transferred-app read status = %d, want 404; body=%s",
			rec.Code, rec.Body.String())
	}
}
