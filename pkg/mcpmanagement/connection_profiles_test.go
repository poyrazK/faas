package mcpmanagement

import (
	"encoding/json"
	"net/url"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestConnectionToolsetsCannotExpandTheServer(t *testing.T) {
	if _, err := connectionToolsets("core,diagnose", []string{"core"}); err == nil {
		t.Fatal("connection expanded the server's catalog")
	}
	if _, err := connectionToolsets("all,unknown", nil); err == nil {
		t.Fatal("all bypassed toolset validation")
	}
	sets, err := connectionToolsets("core,operate", nil)
	if err != nil || !slices.Equal(sets, []string{"core", "operate"}) {
		t.Fatalf("sets=%v error=%v", sets, err)
	}
	if got := effectiveToolsets([]string{"diagnose"}, []string{"core"}); got == nil || len(got) != 0 {
		t.Fatalf("empty intersection became all tools: %v", got)
	}
}

func TestReadOnlyCostConsentDoesNotMintDeploymentScope(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", StateStore: store})
	flow := f.begin(scopeRead)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "capability": {api.ScopeUsageRead}, "toolsets": {"core,operate"}}, f.server.URL)
	defer start.Body.Close()
	if start.StatusCode != 200 {
		t.Fatalf("start=%d", start.StatusCode)
	}
	f.mu.Lock()
	mint := f.mints[len(f.mints)-1]
	f.mu.Unlock()
	if !slices.Equal(mint.Scopes, []string{api.ScopeUsageRead}) {
		t.Fatalf("read-only cost consent gained scopes: %v", mint.Scopes)
	}
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	review.Body.Close()
	if review.StatusCode != 200 {
		t.Fatalf("review=%d", review.StatusCode)
	}
	approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	approved.Body.Close()
	if approved.StatusCode != 303 {
		t.Fatalf("approve=%d", approved.StatusCode)
	}
	redirect, err := url.Parse(approved.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var tokens tokenResponse
	reply := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	decodeResponse(t, reply, &tokens)
	reply.Body.Close()
	check := func() {
		cs, ctx := f.connect(tokens, "2026-07-28")
		defer cs.Close()
		out := callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
		var sets []string
		if err := json.Unmarshal(out["toolsets"], &sets); err != nil || !slices.Equal(sets, []string{"core", "operate"}) {
			t.Fatalf("profile=%v error=%v", sets, err)
		}
		tools := listedTools(t, ctx, cs)
		if !tools["gregale_get_config"] || tools["gregale_get_trace"] {
			t.Fatal("profile was ignored")
		}
	}
	check()
	h, err := NewHTTPHandler(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(h)
	check()
}
