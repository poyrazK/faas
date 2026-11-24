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
		access     string
		wantWrite  bool
		wantPurpos string
	}{
		{accessDeploy, true, api.CliAuthPurposeMCPWrite},
		{"inspect", false, api.CliAuthPurposeMCPRead},
	} {
		t.Run(tc.access, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", StateStore: store})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(service.Handler())
			flow := f.begin(scopeRead)
			start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "access": {tc.access}, "toolsets": {"core"}}, f.server.URL)
			_ = start.Body.Close()
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
			if len(mints) != 1 || mints[0].Purpose != tc.wantPurpos {
				t.Fatalf("sign-in code purposes = %+v, want %s", mints, tc.wantPurpos)
			}
			r := service.remote
			g := onlyGrant(t, r)
			r.mu.Lock()
			scopes, toolsets := slices.Clone(g.scopes), slices.Clone(g.toolsets)
			r.mu.Unlock()
			if slices.Contains(scopes, scopeWrite) != tc.wantWrite {
				t.Fatalf("grant scopes = %v", scopes)
			}
			if wantDeployTools := tc.wantWrite; slices.Contains(toolsets, "release") != wantDeployTools {
				t.Fatalf("grant toolsets = %v", toolsets)
			}
		})
	}
}

func TestConsentPageAccessChoice(t *testing.T) {
	offered := consentPage(consentView{Native: true, OfferWrites: true})
	if !strings.Contains(offered, `name="access" value="deploy" form="gregale-connect" checked`) || !strings.Contains(offered, `value="inspect"`) || !strings.Contains(offered, `value="env:write"`) {
		t.Fatalf("access choice missing or not preselected: %s", offered)
	}
	if plain := consentPage(consentView{Native: true}); strings.Contains(plain, `name="access"`) {
		t.Fatal("access choice shown without OfferWrites")
	}
	if got := deployToolsets("core", []string{ToolsetAll}); got != deployToolsetSpec {
		t.Fatalf("default core upgraded to %q", got)
	}
	if got := deployToolsets("core,diagnose,operate", []string{ToolsetAll}); got != "core,diagnose,operate" {
		t.Fatalf("explicit selection replaced with %q", got)
	}
	if got := deployToolsets("", []string{ToolsetCore}); got != "" {
		t.Fatalf("deploy tools chosen beyond the bridge maximum: %q", got)
	}
}
