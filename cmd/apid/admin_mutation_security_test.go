package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestAdminMutationRoutesRejectBearerKeys is a route-table regression guard.
// All provider-admin writes must require a verified operator session with a
// fresh step-up; a valid admin-scoped bearer key is not sufficient proof.
func TestAdminMutationRoutesRejectBearerKeys(t *testing.T) {
	e := setup(t, api.PlanPro)
	accountID := "00000000-0000-0000-0000-000000000001"
	instanceID := "00000000-0000-0000-0000-000000000002"
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/admin/accounts/" + accountID + "/credits"},
		{http.MethodPost, "/v1/admin/accounts/" + accountID + "/refunds"},
		{http.MethodPost, "/v1/admin/github-webhook-secrets"},
		{http.MethodPost, "/v1/admin/billing-paddle-catalog/sync"},
		{http.MethodDelete, "/v1/admin/billing-paddle-catalog"},
		{http.MethodPost, "/v1/admin/billing-reconcile/" + accountID},
		{http.MethodPost, "/v1/admin/instances/" + instanceID + "/force-park"},
		{http.MethodPost, "/v1/admin/apps/security-test/force-cold-boot"},
		{http.MethodPost, "/v1/admin/instances/" + instanceID + "/force-restart"},
		{http.MethodPost, "/v1/admin/builds/sweep-stuck"},
		{http.MethodPost, "/v1/admin/ops/accounts/" + accountID + "/suspend"},
		{http.MethodPost, "/v1/admin/ops/accounts/" + accountID + "/restore"},
		{http.MethodPost, "/v1/admin/ops/accounts/" + accountID + "/revoke-sessions"},
		{http.MethodPost, "/v1/admin/ops/nodes/security-test/drain"},
		{http.MethodPost, "/v1/admin/ops/nodes/security-test/force-drain"},
		{http.MethodPost, "/v1/admin/ops/nodes/security-test/activate"},
		{http.MethodPatch, "/v1/admin/config/data_placement_enabled"},
		{http.MethodPost, "/v1/admin/config/data_placement_enabled/rollback"},
	}

	for _, tc := range paths {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := e.do(t, tc.method, tc.path, nil, nil)
			assertProblem(t, rec, http.StatusForbidden, api.CodeStepUpRequired)
		})
	}
}

func TestAdminMutationRejectsCrossOriginBrowserRequest(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m", nil)
	e.addAdminSession(t, req)
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
}

func TestAdminMutationRequiresIdempotencyKey(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m", nil)
	e.addAdminSession(t, req)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestAdminMutationAllowsOperationsOrigin(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s = e.s.WithAdminAllowlist(e.acct.Email)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/builds/sweep-stuck?confirm=true&older_than=15m", nil)
	e.addAdminSession(t, req)
	req.Header.Set("Origin", "https://operations.gregale.dev")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Idempotency-Key", "test-admin-operations-origin")
	rec := httptest.NewRecorder()
	e.h = e.s.handler()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
