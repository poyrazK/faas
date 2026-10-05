//go:build !no_pg

package main

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObjectEncryptionDiscoveryMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	objectEncryptionDiscovery(t, e.s, e.store, e.acct, func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		return e.do(t, method, path, body, headers)
	})
}
func TestObjectEncryptionDiscoveryPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	objectEncryptionDiscovery(t, e.s, e.store, e.acct, func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		return e.do(t, method, path, body, headers)
	})
}
func objectEncryptionDiscovery(t *testing.T, s *server, st state.Store, account state.Account, do func(string, string, any, map[string]string) *httptest.ResponseRecorder) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("discovery contacted a native provider")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	_, b, backend, _ := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	path := "/v1/apps/encrypted-journal/buckets/" + b.ID + "/encryption-capabilities"
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateAPIKey(t.Context(), account.ID, hash, "encryption-writer", []string{api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	if r := do("GET", path, nil, headers); r.Code != 403 {
		t.Fatal("ungranted writer", r.Code, r.Body.String())
	}
	access := st.(state.ObjectBucketAccessStore)
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), account.ID, b.ID, key.ID, state.ObjectBucketPermissionRead); err != nil {
		t.Fatal(err)
	}
	if r := do("GET", path, nil, headers); r.Code != 403 {
		t.Fatal("read grant exposed write enrollment", r.Code)
	}
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), account.ID, b.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	r := do("GET", path, nil, headers)
	var out api.ObjectEncryptionCapabilities
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || len(out.KeyIDs) != 1 || out.KeyIDs[0] != backend.Encryption.Keys[0].Reference || len(out.Algorithms) != 1 || out.Algorithms[0] != "aws:kms" || strings.Contains(r.Body.String(), journalNativeKMSKey) || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("owned discovery", r.Code, r.Body.String())
	}
	if r = do("GET", path+"?unknown=true", nil, headers); r.Code != 400 {
		t.Fatal("query ignored", r.Code)
	}
	other, err := st.CreateAccount(t.Context(), "foreign-encryption@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err = api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateAPIKey(t.Context(), other.ID, hash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if r = do("GET", path, nil, map[string]string{"Authorization": "Bearer " + plain}); r.Code != 404 {
		t.Fatal("foreign bucket enumeration", r.Code, r.Body.String())
	}
}
