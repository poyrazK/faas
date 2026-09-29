package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func doAbuseHold(t *testing.T, e testEnv, method, targetID, body, idem string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/admin/accounts/"+targetID+"/abuse-hold", bytes.NewBufferString(body))
	e.addAdminSession(t, req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idem)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// adr: 361 — an operator places and releases the account abuse hold; the
// held account sees it on GET /v1/account and cannot deploy or wake.
func TestAdminAccountAbuseHold_PlaceAndRelease(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	ctx := context.Background()
	target, err := e.store.CreateAccount(ctx, "held@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	rec := doAbuseHold(t, e, http.MethodPost, target.ID, `{"note":"scanning reported by provider"}`, "hold-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("place: status %d body %s", rec.Code, rec.Body)
	}
	var out api.AccountAbuseHoldActionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Changed || out.AbuseHold == nil || out.AbuseHold.Reason != state.AccountAbuseHoldOperator {
		t.Fatalf("place response = %+v, want a new operator hold", out)
	}
	held, _ := e.store.AccountByID(ctx, target.ID)
	if held.Active() || held.MayDeploy() {
		t.Fatal("held account is still active")
	}
	if got := held.DeployBlockedProblem().Code; got != api.CodeAccountAbuseHold {
		t.Fatalf("deploy problem = %s, want %s", got, api.CodeAccountAbuseHold)
	}
	if view := accountAbuseHoldView(held); view == nil || view.Reason != state.AccountAbuseHoldOperator {
		t.Fatalf("account view hold = %+v", view)
	}

	rec = doAbuseHold(t, e, http.MethodDelete, target.ID, `{"note":"reviewed, false positive"}`, "release-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("release: status %d body %s", rec.Code, rec.Body)
	}
	out = api.AccountAbuseHoldActionResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !out.Changed || out.AbuseHold != nil {
		t.Fatalf("release response = %+v, want the hold cleared", out)
	}
	if released, _ := e.store.AccountByID(ctx, target.ID); !released.Active() {
		t.Fatal("released account is not active")
	}

	rec = doAbuseHold(t, e, http.MethodDelete, target.ID, `{"note":"second release"}`, "release-2")
	out = api.AccountAbuseHoldActionResponse{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Changed {
		t.Fatalf("second release: status %d changed %v, want a no-op", rec.Code, out.Changed)
	}
}

func TestAdminAccountAbuseHold_Validation(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	for _, tc := range []struct {
		name, target, body string
		want               int
		code               string
	}{
		{"bad uuid", "not-a-uuid", `{"note":"abc"}`, http.StatusBadRequest, api.CodeValidation},
		{"short note", "00000000-0000-4000-8000-000000000001", `{"note":"x"}`, http.StatusBadRequest, api.CodeValidation},
		{"unknown account", "00000000-0000-4000-8000-000000000001", `{"note":"scanning"}`, http.StatusNotFound, api.CodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doAbuseHold(t, e, http.MethodPost, tc.target, tc.body, "v-"+tc.name)
			assertProblem(t, rec, tc.want, tc.code)
		})
	}
}

// A customer with an admin-scoped key who is not an operator cannot hold or
// release anyone, including themselves.
func TestAdminAccountAbuseHold_NonOperatorForbidden(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "allowed@example.com", "intruder@example.com")
	req := httptest.NewRequest(http.MethodDelete, "/v1/admin/accounts/"+e.acct.ID+"/abuse-hold",
		bytes.NewBufferString(`{"note":"let me out"}`))
	rec := httptest.NewRecorder()
	e.s.requireOperator(e.s.releaseAccountAbuseHold)(rec, req, e.acct)
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}
