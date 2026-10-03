// adr: 427
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExclusivePolicyRetirementAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	deployment := mustSeedDeployment(t, e, "retirement-api")
	policy := exclusivework.Policy{Name: "retirement-api", Scope: "account", MemberAppIDs: []string{deployment.AppID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
	path := "/v1/account/operation-policies/" + policy.Name
	if saved := e.do(t, http.MethodPut, path, policy, nil); saved.Code != http.StatusOK {
		t.Fatalf("save: %d %s", saved.Code, saved.Body)
	}
	op, _, err := e.store.AdmitExclusiveOperation(t.Context(), state.ExclusiveAdmission{
		AccountID: e.acct.ID, AppID: deployment.AppID, PolicyName: policy.Name,
		Key: json.RawMessage(`"lane"`), Request: json.RawMessage(`{"kind":"sync"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRetirementInUse(t, e.do(t, http.MethodDelete, path, nil, nil))
	if err := e.store.CancelExclusiveOperation(t.Context(), e.acct.ID, op.ID); err != nil {
		t.Fatal(err)
	}
	cron, err := e.store.CreateCron(t.Context(), deployment.AppID, "0 0 1 1 *", "/sync", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpsertExclusiveTriggerBinding(t.Context(), state.ExclusiveTriggerBinding{
		AccountID: e.acct.ID, Source: "cron", TriggerID: cron.ID, PolicyName: policy.Name, Key: json.RawMessage(`"lane"`),
	}); err != nil {
		t.Fatal(err)
	}
	assertRetirementInUse(t, e.do(t, http.MethodDelete, path, nil, nil))
	if err := e.store.DeleteExclusiveTriggerBinding(t.Context(), e.acct.ID, "cron", cron.ID); err != nil {
		t.Fatal(err)
	}
	var retired api.ExclusiveWorkPolicyRecord
	for i := 0; i < 2; i++ {
		rec := e.do(t, http.MethodDelete, path, nil, nil)
		var got api.ExclusiveWorkPolicyRecord
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !got.Retired || got.Policy.Name != policy.Name || got.Revision != 2 {
			t.Fatalf("retirement: %d %s", rec.Code, rec.Body)
		}
		if i == 0 {
			retired = got
		} else if !reflect.DeepEqual(retired, got) {
			t.Fatalf("idempotent retirement changed: before=%+v after=%+v", retired, got)
		}
	}
	if list := e.do(t, http.MethodGet, "/v1/account/operation-policies", nil, nil); list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"retired":true`) {
		t.Fatalf("retired history missing: %d %s", list.Code, list.Body)
	}
	if history := e.do(t, http.MethodGet, "/v1/operations/"+op.ID, nil, nil); history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("operation history lost: %d %s", history.Code, history.Body)
	}
	if rec := e.do(t, http.MethodPut, path, policy, nil); rec.Code != http.StatusConflict {
		t.Fatalf("retired name recreated: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		name   string
		status int
	}{{"missing", http.StatusNotFound}, {"Bad_Name", http.StatusBadRequest}} {
		if rec := e.do(t, http.MethodDelete, "/v1/account/operation-policies/"+tc.name, nil, nil); rec.Code != tc.status {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}
}

func assertRetirementInUse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != "operation_policy_in_use" {
		t.Fatalf("in-use policy: %d %s", rec.Code, rec.Body)
	}
}

func TestExclusivePolicyRetirementAuthorization(t *testing.T) {
	path := "/v1/account/operation-policies/unavailable"
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	if rec := e.do(t, http.MethodDelete, path, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only key retirement: %d %s", rec.Code, rec.Body)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated retirement: %d %s", rec.Code, rec.Body)
	}
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	pendingCookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)
	rec = cookieDo(t, h, pendingCookie, http.MethodDelete, path, nil)
	var problem api.Problem
	if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("pending MFA retirement: %d %s", rec.Code, rec.Body)
	}
}
