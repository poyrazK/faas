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
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 561
func TestBucketDefaultEncryptionAuthorityMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	bucketDefaultEncryptionAuthority(t, e.s, e.store, e.acct)
}
func TestBucketDefaultEncryptionAuthorityPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	bucketDefaultEncryptionAuthority(t, e.s, e.store, e.acct)
}

func bucketDefaultEncryptionAuthority(t *testing.T, s *server, st state.Store, account state.Account) {
	var nativeCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
	}))
	defer upstream.Close()
	_, b, backend, _ := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/encrypted-journal/buckets/" + b.ID + "/encryption"
	do := func(method, suffix, body, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	body := `{"encryption":{"algorithm":"aws:kms","key_id":"` + backend.Encryption.Keys[0].Reference + `"}}`
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateAPIKey(t.Context(), account.ID, hash, "default-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
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
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			input := ""
			if method == "PUT" {
				input = body
			}
			if r := do(method, "", input, plain); r.Code != 403 {
				t.Fatal(permission, method, r.Code, r.Body.String())
			}
		}
	}
	if nativeCalls.Load() != 0 {
		t.Fatal("ungranted manager contacted native storage")
	}
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), account.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := do("GET", "", "", plain); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, tc := range []struct{ method, suffix, body string }{
		{"GET", "?ignored=true", ""}, {"GET", "", "{}"}, {"DELETE", "", "{}"},
		{"PUT", "", `{"encryption":{"algorithm":"AES256","algorithm":"AES256"}}`},
		{"PUT", "", `{"encryption":{"Algorithm":"AES256"}}`},
		{"PUT", "", `{"encryption":{"algorithm":"aws:kms","key_id":"native-key"}}`},
		{"PUT", "", strings.Repeat(" ", int(api.MaxObjectBucketEncryptionBodyBytes)+1) + body},
	} {
		if r := do(tc.method, tc.suffix, tc.body, plain); r.Code != 400 {
			t.Fatal(tc.method, r.Code, r.Body.String())
		}
	}
	other, err := st.CreateAccount(t.Context(), "foreign-default@example.test", api.PlanPro)
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
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		input := ""
		if method == "PUT" {
			input = body
		}
		if r := do(method, "", input, foreign); r.Code != 404 {
			t.Fatal("foreign enumeration", method, r.Code, r.Body.String())
		}
	}
	if nativeCalls.Load() != 1 {
		t.Fatal("invalid or foreign requests contacted native storage", nativeCalls.Load())
	}
	j, err := st.(state.ObjectBucketEncryptionStore).GetObjectBucketEncryption(t.Context(), account.ID, b.AppID, b.ID)
	if err != nil || j.Revision != 0 {
		t.Fatal("invalid request persisted a policy", j, err)
	}
}
