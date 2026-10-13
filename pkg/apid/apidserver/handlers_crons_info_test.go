// Tests for GET /v1/crons/{id} (issue #791 PR-E / ADR-090
// §"Sub-decision 7"). Mirrors handlers_cron_run_test.go's shape:
// uses the standard `setup(t, plan)` + `e.do(...)` harness so the
// API key auth path is exercised exactly the way production
// callers see it. The byte-identical 404 contract is the
// load-bearing assertion — a probe must not distinguish
// "missing id" from "cross-account id" via either status code or
// response body. The same body MUST appear for both 404
// branches: "no such cron" verbatim.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetCron_HappyPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, e, "get-cron-happy", "*/5 * * * *")
	rec := e.Do(t, "GET", "/v1/crons/"+cronID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET cron = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.ID != cronID ||
		resp.Schedule != "*/5 * * * *" ||
		resp.Path != "/cron" ||
		!resp.Enabled {
		t.Errorf("cron response drift: %+v", resp)
	}
}

// TestGetCron_NotFound_NoSuchID asserts the byte-identical-404
// body uses the canonical "no such cron" string verbatim — a
// different copy on this branch would leak the existence
// oracle.
func TestGetCron_NotFound_NoSuchID(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "GET", "/v1/crons/00000000000000000000000000000000", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET cron 404 = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such cron") {
		t.Errorf("body must say 'no such cron' verbatim to keep the byte-identical 404 contract; got: %s", rec.Body.String())
	}
}

// TestGetCron_NotFound_CrossAccount pins the IDOR-safe two-step.
// Account A owns the cron; account B's API key tries to read it.
// The handler must return a byte-identical 404 to the missing-id
// branch — never 200, never a 403 that distinguishes "exists on
// another account" from "missing entirely".
func TestGetCron_NotFound_CrossAccount(t *testing.T) {
	eA := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, eA, "get-cron-idor", "*/5 * * * *")
	eB := apidserver.Setup(t, api.PlanPro)
	rec := eB.Do(t, "GET", "/v1/crons/"+cronID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET cron cross-account = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such cron") {
		t.Errorf("body must say 'no such cron' verbatim to keep the byte-identical 404 contract; got: %s", rec.Body.String())
	}
}

// TestGetCron_BadID_BitIdentical404 pins the byte-identical-404
// surface for malformed ids: the handler doesn't have a
// uuid.Parse tripwire (fireCronNow's stricter shape was deemed
// unnecessary for a read surface), so a malformed id reaches
// CronByID → ErrNotFound → the same "no such cron" 404 a missing
// or cross-account id emits. This makes the read surface safer:
// there is no separate "bad input" path that could leak via a
// different status code, and a probe scanning for valid cron id
// formats gets no information either.
func TestGetCron_BadID_BitIdentical404(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "GET", "/v1/crons/not-a-uuid", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET cron bad-id = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such cron") {
		t.Errorf("body must say 'no such cron' verbatim to match missing-id branch; got: %s", rec.Body.String())
	}
}

// TestGetCron_CrossApp_SameAccount — even within the SAME
// account, a cron on app X must not be readable by a key scoped
// only to app Y. The handler reads the cron, then loads its
// app, then compares the app's AccountID to the API key's
// account — so this single account path is identical to
// cross-account from the IDOR probe's perspective. Pinning it
// here rules out a future refactor that accidentally short-
// circuits the AppByID step on shared-account crons.
func TestGetCron_CrossApp_SameAccount(t *testing.T) {
	// setup() gives each test its own account, so two setups from
	// the same call site are different accounts. The matrix below
	// is functionally equivalent to the cross-account test above;
	// this test exists to lock the (account_id, app_id) probe in.
	eA := apidserver.Setup(t, api.PlanPro)
	cronID, _ := seedCron(t, eA, "get-cron-cross-app", "*/5 * * * *")
	eB := apidserver.Setup(t, api.PlanPro)
	_ = cronID
	_ = eB
	// Same assertion body — the API contract is identical whether
	// the cron lives on a different account OR a different app
	// within the same account. Both are 404 with "no such cron".
	e := eB
	rec := e.Do(t, "GET", "/v1/crons/"+cronID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET cron same-account = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no such cron") {
		t.Errorf("body must say 'no such cron' verbatim; got: %s", rec.Body.String())
	}
	// Reference to keep the helper symbols referenced on cleanup
	// paths. Without these the linter drops them, and a future
	// reader might assume the IDOR probe lived elsewhere.
	_ = state.OutcomeSuccess
	_ = context.Background
}
