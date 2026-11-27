// adr: 20261127160000
package mcpmanagement

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Deploy access asks apid for the Connect grant, and the review page says
// what it means; an apid that predates the grant refuses that code, and the
// sign-in goes on without it.
func TestDeployAccessAsksForTheConnectGrant(t *testing.T) {
	for _, tc := range []struct {
		name      string
		refuse    bool
		wantMints [][]string
		wantGrant bool
	}{
		{"current apid", false, [][]string{{api.ScopeDeployWrite, api.ScopeConnectApproval}}, true},
		{"apid without the grant", true, [][]string{{api.ScopeDeployWrite, api.ScopeConnectApproval}, nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _ := testStateStore(t)
			f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", StateStore: store})
			f.refuseConnect = tc.refuse
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(service.Handler())
			flow := f.begin(scopeRead)
			form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "access": {accessDeploy}}
			start := f.submitConsent(flow, form, f.server.URL)
			_ = start.Body.Close()
			if start.StatusCode >= 400 {
				t.Fatalf("start-login = %d", start.StatusCode)
			}
			f.mu.Lock()
			mints := slices.Clone(f.mints)
			f.mu.Unlock()
			if len(mints) != len(tc.wantMints) {
				t.Fatalf("sign-in codes = %+v, want %d", mints, len(tc.wantMints))
			}
			for i, want := range tc.wantMints {
				if !slices.Equal(mints[i].Scopes, want) {
					t.Fatalf("sign-in code %d scopes = %v, want %v", i, mints[i].Scopes, want)
				}
			}
			if tc.refuse && mints[1].Purpose != api.CliAuthPurposeMCPWrite {
				t.Fatalf("fallback code purpose = %q, want %q", mints[1].Purpose, api.CliAuthPurposeMCPWrite)
			}
			review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
			page := readPage(t, review)
			_ = review.Body.Close()
			if got := strings.Contains(page, "without Gregale asking you again"); got != tc.wantGrant {
				t.Fatalf("review names the Connect grant = %v, want %v: %s", got, tc.wantGrant, page)
			}
			approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, true)
			_ = approved.Body.Close()
			if approved.StatusCode != http.StatusSeeOther {
				t.Fatalf("approve = %d", approved.StatusCode)
			}
			g := onlyGrant(t, service.remote)
			service.remote.mu.Lock()
			scopes := slices.Clone(g.scopes)
			service.remote.mu.Unlock()
			if !slices.Contains(scopes, scopeWrite) {
				t.Fatalf("grant scopes = %v; want deploy access either way", scopes)
			}
		})
	}
}
