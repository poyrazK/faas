//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// TestObjectLockPlanCapsControlAPI pins ADR-20261111123000 decision 8 on the
// control API: a bucket lock default past the owner's plan cap, or any
// Object Lock on Free, is refused with the limit and the observed days
// (object storage audit OS-6).
//
// adr: 20261111123000
func TestObjectLockPlanCapsControlAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	s, st, account := *e.S(), *e.Store(), *e.Acct()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("plan refusal reached the provider", r.Method, r.URL)
		}
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
	}))
	defer upstream.Close()
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{Enabled: true}))
	if err := (*s.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(t.Context(), account.ID, hash, "lock-plan", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	put := func(body string) (int, api.Problem) {
		r := httptest.NewRequest("PUT", "/v1/apps/encrypted-journal/buckets/"+b.ID+"/object-lock", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plain)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		var p api.Problem
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		return w.Code, p
	}
	for _, tc := range []struct {
		plan            api.Plan
		body            string
		limit, observed int64
	}{
		{api.PlanHobby, `{"configuration":{"enabled":true,"default_retention":{"mode":"COMPLIANCE","years":100}}}`, 90, 36500},
		{api.PlanHobby, `{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","days":91}}}`, 90, 91},
		{api.PlanFree, `{"configuration":{"enabled":true}}`, 0, 1},
		{api.PlanFree, `{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","days":1}}}`, 0, 1},
	} {
		if err := st.UpdateAccountPlan(t.Context(), account.ID, tc.plan); err != nil {
			t.Fatal(err)
		}
		code, p := put(tc.body)
		if code != 403 || p.Code != "object_lock_plan_limit" || p.Limit == nil || *p.Limit != tc.limit || p.Observed == nil || *p.Observed != tc.observed || p.DocsURL == "" {
			t.Fatalf("%s %s = %d %+v", tc.plan, tc.body, code, p)
		}
	}
	if j, err := st.GetObjectBucketObjectLock(t.Context(), account.ID, b.AppID, b.ID); err == nil && j.DesiredConfiguration != nil {
		t.Fatal("refused configuration was recorded", j)
	}
}
