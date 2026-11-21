package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261112194947: `gregale ai policy` sets the semantic cache and keeps
// it when other flags change.
func TestAIPolicySemanticCacheFlags(t *testing.T) {
	main.ResetJSONOut(t)
	const integrationID = "11111111-2222-3333-4444-555555555555"
	const embedderID = "99999999-2222-3333-4444-555555555555"
	current := api.OutboundIntegrationOffer{ID: integrationID, AIProvider: api.OutboundAIProviderOpenAI}
	var policies []api.PutOutboundAIPolicyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/outbound/integrations":
			main.WriteJSONTest(w, api.OutboundIntegrationOfferList{Items: []api.OutboundIntegrationOffer{current}})
		case "PUT /v1/outbound/integrations/" + integrationID + "/ai":
			var policy api.PutOutboundAIPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				t.Error(err)
			}
			policies = append(policies, policy)
			current.AISemanticCache = policy.SemanticCache
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdAI([]string{"policy", integrationID, "--semantic-cache", "on", "--semantic-embedding-integration", embedderID,
		"--semantic-embedding-model", "text-embedding-3-small", "--semantic-threshold", "0.97"}); code != 0 {
		t.Fatalf("ai policy exit = %d", code)
	}
	if got := policies[0].SemanticCache; got == nil || !got.Enabled || got.ThresholdBasisPoints != 9700 || got.EmbeddingIntegrationID != embedderID ||
		got.EmbeddingModel != "text-embedding-3-small" || got.ShareAcrossUsers {
		t.Fatalf("semantic cache = %+v", got)
	}
	if code := main.CmdAI([]string{"policy", integrationID, "--daily-tokens", "1000"}); code != 0 {
		t.Fatalf("ai policy exit = %d", code)
	}
	if kept := policies[1].SemanticCache; kept == nil || kept.ThresholdBasisPoints != 9700 {
		t.Fatalf("an unrelated change dropped the semantic cache: %+v", kept)
	}
	if code := main.CmdAI([]string{"policy", integrationID, "--semantic-share-users", "on"}); code != 0 || !policies[2].SemanticCache.ShareAcrossUsers {
		t.Fatalf("share users exit = %d, %+v", code, policies[2].SemanticCache)
	}
	for _, args := range [][]string{
		{"--semantic-threshold", "0.5"},
		{"--semantic-cache", "maybe"},
		{"--semantic-cache", "off", "--semantic-threshold", "0.9"},
	} {
		if code := main.CmdAI(append([]string{"policy", integrationID}, args...)); code == 0 {
			t.Fatalf("%v was accepted", args)
		}
	}
	if code := main.CmdAI([]string{"policy", integrationID, "--semantic-cache", "off"}); code != 0 || policies[len(policies)-1].SemanticCache != nil {
		t.Fatalf("off exit = %d, %+v", code, policies[len(policies)-1].SemanticCache)
	}
}
