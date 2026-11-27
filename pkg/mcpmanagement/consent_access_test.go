// adr: 20261028205920
package mcpmanagement

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Clients request the scope the 401 challenge names, which is inspection, so
// the page before sign-in offers deploy access, preselected; the person can
// still keep the connection read-only (ADR-20261028205920 amendment 1).
func TestConsentOffersDeployAccessToAnInspectOnlyClient(t *testing.T) {
	for _, tc := range []struct {
		name, access, toolsets string
		capabilities           []string
		wantWrite              bool
		wantPurpose            string
		wantScopes             []string
		wantDeployTools        bool
	}{
		// Deploy access asks for the Connect grant (ADR-20261127160000).
		{"deploy with default tools", accessDeploy, "", nil, true, api.CliAuthPurposeMCP, []string{api.ScopeDeployWrite, api.ScopeConnectApproval}, true},
		{"deploy with core tools kept", accessDeploy, "core", nil, true, api.CliAuthPurposeMCP, []string{api.ScopeDeployWrite, api.ScopeConnectApproval}, false},
		// A write-only extra ticked before choosing "Inspect only" is dropped,
		// not a failed connection.
		{"inspect", "inspect", "", []string{api.ScopeStorageManage}, false, api.CliAuthPurposeMCPRead, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", StateStore: store})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(service.Handler())
			flow := f.begin(scopeRead)
			form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "access": {tc.access}, "toolsets": {tc.toolsets}, "capability": tc.capabilities}
			start := f.submitConsent(flow, form, f.server.URL)
			_ = start.Body.Close()
			if start.StatusCode >= 400 {
				t.Fatalf("start-login = %d", start.StatusCode)
			}
			review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
			defer review.Body.Close()
			page := readPage(t, review)
			if got := strings.Contains(page, `name="allow_writes"`); got != tc.wantWrite {
				t.Fatalf("review offers writes = %v, want %v: %s", got, tc.wantWrite, page)
			}
			if tc.wantWrite && !strings.Contains(page, `name="default_write" value="yes" checked`) {
				t.Fatal("default-environment writes not preselected")
			}
			approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, tc.wantWrite)
			_ = approved.Body.Close()
			if approved.StatusCode != http.StatusSeeOther {
				t.Fatalf("approve = %d", approved.StatusCode)
			}
			f.mu.Lock()
			mints := slices.Clone(f.mints)
			f.mu.Unlock()
			if len(mints) != 1 || mints[0].Purpose != tc.wantPurpose || !slices.Equal(mints[0].Scopes, tc.wantScopes) {
				t.Fatalf("sign-in codes = %+v, want %s %v", mints, tc.wantPurpose, tc.wantScopes)
			}
			r := service.remote
			g := onlyGrant(t, r)
			r.mu.Lock()
			scopes, toolsets, capabilities := slices.Clone(g.scopes), slices.Clone(g.toolsets), slices.Clone(g.capabilities)
			r.mu.Unlock()
			if slices.Contains(scopes, scopeWrite) != tc.wantWrite {
				t.Fatalf("grant scopes = %v", scopes)
			}
			if slices.Contains(toolsets, "release") != tc.wantDeployTools {
				t.Fatalf("grant toolsets = %v", toolsets)
			}
			if !tc.wantWrite && len(capabilities) != 0 {
				t.Fatalf("read-only grant kept write capabilities %v", capabilities)
			}
		})
	}
}

func TestConsentPageAccessChoice(t *testing.T) {
	offered := consentPage(consentView{Native: true, OfferWrites: true})
	if !strings.Contains(offered, `name="access" value="deploy" form="gregale-connect" checked`) || !strings.Contains(offered, `value="inspect"`) || !strings.Contains(offered, `value="env:write"`) {
		t.Fatalf("access choice missing or not preselected: %s", offered)
	}
	if !strings.Contains(offered, `<option value="" selected>Default for the access you choose</option>`) || strings.Contains(offered, "Core tools (default)") {
		t.Fatalf("tool default does not follow the access choice: %s", offered)
	}
	plain := consentPage(consentView{Native: true})
	if strings.Contains(plain, `name="access"`) || !strings.Contains(plain, "Core tools (default)") {
		t.Fatal("access choice shown without OfferWrites, or core no longer the default")
	}
	if got := deployToolsets("", []string{ToolsetAll}); got != deployToolsetSpec {
		t.Fatalf("default selection became %q", got)
	}
	if got := deployToolsets("core", []string{ToolsetAll}); got != "core" {
		t.Fatalf("explicit core replaced with %q", got)
	}
	if got := deployToolsets("", []string{ToolsetCore}); got != "" {
		t.Fatalf("deploy tools chosen beyond the bridge maximum: %q", got)
	}
}
