//go:build !no_pg

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 422
func TestBucketObjectLockAuthorityMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	bucketObjectLockAuthority(t, e.s, e.store, e.acct)
}
func TestBucketObjectLockAuthorityPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	bucketObjectLockAuthority(t, e.s, e.store, e.acct)
}
func bucketObjectLockAuthority(t *testing.T, s *server, st state.Store, account state.Account) {
	var nativeCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
	}))
	defer upstream.Close()
	_, b, _, policy := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{Enabled: true}))
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/encrypted-journal/buckets/" + b.ID + "/object-lock"
	do := func(method, suffix, body, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateAPIKey(t.Context(), account.ID, hash, "lock-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	access := st.(state.ObjectBucketAccessStore)
	for _, permission := range []string{"", state.ObjectBucketPermissionRead} {
		if permission != "" {
			if _, err = access.SetObjectBucketAccessGrant(t.Context(), account.ID, b.ID, key.ID, permission); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct{ method, suffix, body string }{{"GET", "", ""}, {"GET", "-capabilities", ""}, {"PUT", "", `{"configuration":{"enabled":true}}`}} {
			if r := do(tc.method, tc.suffix, tc.body, plain); r.Code != 403 {
				t.Fatal(permission, tc, r.Code, r.Body.String())
			}
		}
	}
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), account.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := do("GET", "-capabilities", "", plain); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, tc := range []struct{ method, suffix, body string }{
		{"GET", "?ignored=true", ""}, {"GET", "", "{}"}, {"GET", "-capabilities", "{}"},
		{"PUT", "", `{"configuration":{"enabled":true,"enabled":false}}`},
		{"PUT", "", `{"configuration":{"enabled":true,"Enabled":false}}`},
		{"PUT", "", `{"configuration":{"enabled":true,"default_retention":null}}`},
		{"PUT", "", `{"configuration":{"enabled":false}}`},
		{"PUT", "", strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)+1)},
	} {
		if r := do(tc.method, tc.suffix, tc.body, plain); r.Code != 400 {
			t.Fatal(tc, r.Code, r.Body.String())
		}
	}
	if r := do("PUT", "", `{"configuration":{"enabled":true,"default_retention":{"mode":"GOVERNANCE","default_event_hold":{"days":3}}}}`, plain); r.Code != 501 {
		t.Fatal("unenrolled event hold", r.Code, r.Body.String())
	}
	other, err := st.CreateAccount(t.Context(), "foreign-lock@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(t.Context(), other.ID, hash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		if r := do(method, "", ``, foreign); r.Code != 404 {
			t.Fatal("foreign enumeration", r.Code, r.Body.String())
		}
	}
	if nativeCalls.Load() != 0 {
		t.Fatal("invalid or unauthorized request contacted native", nativeCalls.Load())
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{}))
	if r := do("PUT", "", `{"configuration":{"enabled":true}}`, plain); r.Code != 501 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := do("GET", "", "", plain); r.Code != 501 {
		t.Fatal("unenrolled discovery contacted native", r.Code, r.Body.String())
	}
	if nativeCalls.Load() != 0 {
		t.Fatal("disabled enrollment contacted native")
	}
	s.WithObjectStorage(objectLockTestRegistry(t, upstream.URL, policy, objectstorage.ObjectLockConfig{Enabled: true}))
	if r := do("GET", "", "", plain); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if nativeCalls.Load() != 1 {
		t.Fatal(nativeCalls.Load())
	}
}
