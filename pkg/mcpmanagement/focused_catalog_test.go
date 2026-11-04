// adr: 20261103193000
package mcpmanagement

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestFocusedCatalogSelectionNeverWidensTheCeiling(t *testing.T) {
	for _, tc := range []struct {
		spec          string
		maximum, want []string
	}{
		{"", nil, []string{ToolsetCore}},
		{"", []string{ToolsetAll}, []string{ToolsetCore}},
		{"", []string{ToolsetCore, ToolsetOperate}, []string{ToolsetCore}},
		{"", []string{ToolsetDiagnose}, []string{ToolsetDiagnose}},
		{"", []string{}, []string{}},
		{"all", []string{ToolsetCore}, []string{ToolsetCore}},
		{"all", nil, []string{ToolsetAll}},
	} {
		got, err := connectionToolsets(tc.spec, tc.maximum)
		if err != nil || !slices.Equal(got, tc.want) || (got == nil) != (tc.want == nil) {
			t.Fatalf("spec=%q maximum=%v got=%v err=%v", tc.spec, tc.maximum, got, err)
		}
	}
	if _, err := connectionToolsets("core,diagnose", []string{ToolsetCore}); err == nil {
		t.Fatal("explicit selection expanded the operator ceiling")
	}
	for _, selection := range [][]string{nil, {}, {ToolsetAll}, {ToolsetCore}} {
		raw, err := json.Marshal(storedGrant{Toolsets: selection})
		if err != nil {
			t.Fatal(err)
		}
		var restored storedGrant
		if err := json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		got := effectiveToolsets(restored.Toolsets, nil)
		want := selection
		if want == nil {
			want = []string{ToolsetCore}
		}
		if !slices.Equal(got, want) || got == nil {
			t.Fatalf("journal changed selection %v to %v", selection, got)
		}
	}
}

func TestFocusedCatalogOAuthDefaultsAndExplicitAllSurviveRestart(t *testing.T) {
	for _, mode := range []string{"api-key", "browser"} {
		for _, selection := range []string{"", "all"} {
			t.Run(mode+"/"+map[bool]string{true: "default", false: "explicit_all"}[selection == ""], func(t *testing.T) {
				store, _, _ := testStateStore(t)
				f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: mode, StateStore: store, Toolsets: []string{ToolsetAll}})
				flow := f.begin(scopeRead)
				form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "toolsets": {selection}}
				if mode == "browser" {
					form.Set("decision", "start-login")
				} else {
					form.Set("decision", "verify")
					form.Set("api_key", "fp_test_alice_remote_key")
				}
				review := f.submitConsent(flow, form, f.server.URL)
				if review.StatusCode != 200 {
					t.Fatalf("review=%d", review.StatusCode)
				}
				review.Body.Close()
				if mode == "browser" {
					response := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
					if response.StatusCode != 200 {
						t.Fatalf("login=%d", response.StatusCode)
					}
					response.Body.Close()
				}
				approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
				if approved.StatusCode != 303 {
					t.Fatalf("approve=%d", approved.StatusCode)
				}
				redirect, err := url.Parse(approved.Header.Get("Location"))
				approved.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				var tokens tokenResponse
				response := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
				decodeResponse(t, response, &tokens)
				response.Body.Close()
				check := func() {
					client, ctx := f.connect(tokens, "2026-07-28")
					defer client.Close()
					out := callTool(t, ctx, client, "gregale_context", map[string]any{}, false)
					var sets []string
					if err := json.Unmarshal(out["toolsets"], &sets); err != nil {
						t.Fatal(err)
					}
					want := []string{ToolsetCore}
					if selection == "all" {
						want = []string{ToolsetAll}
					}
					if !slices.Equal(sets, want) {
						t.Fatalf("catalog=%v want=%v", sets, want)
					}
				}
				check()
				handler, err := NewHTTPHandler(f.opts)
				if err != nil {
					t.Fatal(err)
				}
				f.setHandler(handler)
				check()
			})
		}
	}
}

func TestFocusedCatalogConsentStartsWithCore(t *testing.T) {
	page := consentPage(consentView{Native: true})
	if strings.Index(page, `value="core"`) > strings.Index(page, `value="all"`) || !strings.Contains(page, "Core tools (default)") {
		t.Fatal("browser selection did not default to core")
	}
	limited := consentPage(consentView{Native: true, MaximumToolsets: []string{ToolsetCore}})
	if strings.Contains(limited, `value="core,diagnose,operate"`) {
		t.Fatal("browser offered an unavailable catalog")
	}
}
