package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale keys add` printed fp_live_f8470c7e while `gregale keys list`
// showed fp_live_1ec958bff8ed for the same key on production-us: create
// returned the plaintext's first 16 characters, list a hash-derived value.
// The listing now shows the prefix recorded at mint.
func TestKeyListShowsThePrefixPrintedAtCreation(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "match-me", Scopes: []string{api.ScopeAppsRead}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/keys: %d %s", rec.Code, rec.Body)
	}
	var created api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Prefix == "" || !strings.HasPrefix(created.Plaintext, created.Prefix) {
		t.Fatalf("create prefix %q is not a prefix of the minted key", created.Prefix)
	}

	// A key minted before display prefixes were recorded keeps the
	// hash-derived identifier rather than an empty prefix.
	_, legacyHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, legacyHash, "legacy", []string{api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}

	listRec := e.do(t, http.MethodGet, "/v1/keys", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/keys: %d %s", listRec.Code, listRec.Body)
	}
	var listed []api.APIKeyResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, k := range listed {
		got[k.ID] = k.Prefix
	}
	if got[created.ID] != created.Prefix {
		t.Fatalf("listed prefix %q, want %q as printed at creation", got[created.ID], created.Prefix)
	}
	if want := keyPrefixFromHash(legacyHash); got[legacy.ID] != want {
		t.Fatalf("legacy key listed as %q, want hash-derived %q", got[legacy.ID], want)
	}
}
