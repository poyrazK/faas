// adr: 20261028205920
package mcpmanagement

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func readPage(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The hosted bridge sends the browser to sign-in and, once apid sends it back
// to /mcp/oauth/continue, shows the account review in the same tab.
func TestHostedSignInRunsInOneTab(t *testing.T) {
	telemetry := NewTelemetry()
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", Hosted: true, Telemetry: telemetry})
	flow := f.begin(scopeRead)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != http.StatusSeeOther {
		t.Fatalf("start-login = %d, want a redirect to sign-in", start.StatusCode)
	}
	login, err := url.Parse(start.Header.Get("Location"))
	if err != nil || login.Host != "console.example" || login.Path != "/cli-auth" || login.Query().Get("return") != "mcp" || login.Query().Get("code") != "MCPR-fixture" {
		t.Fatalf("sign-in link = %q", start.Header.Get("Location"))
	}

	stranger := *f.client
	unbound, err := stranger.Get(f.server.URL + "/mcp/oauth/continue")
	if err != nil {
		t.Fatal(err)
	}
	if page := readPage(t, unbound); unbound.StatusCode != http.StatusBadRequest || !strings.Contains(page, "start again from your agent") {
		t.Fatalf("continue without the flow cookie = %d %s", unbound.StatusCode, page)
	}

	back, err := flow.client.Get(f.server.URL + "/mcp/oauth/continue")
	if err != nil {
		t.Fatal(err)
	}
	page := readPage(t, back)
	if back.StatusCode != http.StatusOK || !strings.Contains(page, "Account: <strong>alice</strong>") || !strings.Contains(page, "Connect this account") || !strings.Contains(page, "ends after a week unused") {
		t.Fatalf("continue = %d %s", back.StatusCode, page)
	}
	csrf := ""
	for _, match := range hiddenInput.FindAllStringSubmatch(page, -1) {
		if match[1] == "csrf" {
			csrf = match[2]
		}
	}
	if csrf == "" || csrf == flow.csrf {
		t.Fatal("the review page did not get a fresh CSRF token")
	}
	if stale := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false); stale.StatusCode != http.StatusForbidden {
		_ = stale.Body.Close()
		t.Fatalf("the replaced page's token still approved: %d", stale.StatusCode)
	} else {
		_ = stale.Body.Close()
	}
	approved := f.consent(flow, "approve", "", f.server.URL, csrf, false)
	_ = approved.Body.Close()
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve = %d", approved.StatusCode)
	}
	metrics := scrape(t, telemetry)
	for _, step := range []string{"started", "sign_in_started", "signed_in", "approved"} {
		if !strings.Contains(metrics, `gregale_management_mcp_consent_total{step="`+step+`"} 1`) {
			t.Errorf("consent step %s not counted once", step)
		}
	}
	if !strings.Contains(metrics, `gregale_management_mcp_consent_total{step="denied"} 0`) {
		t.Error("denied counted")
	}
}

// A browser back to the first page resubmits it; the flow shows its sign-in
// link and "I authorized" again instead of failing.
func TestStartingSignInTwiceShowsTheLinkAgain(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser"})
	flow := f.begin(scopeRead)
	form := url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}
	first := f.submitConsent(flow, form, f.server.URL)
	_ = first.Body.Close()
	again := f.submitConsent(flow, form, f.server.URL)
	if page := readPage(t, again); again.StatusCode != http.StatusOK || !strings.Contains(page, "I authorized in Gregale") || !strings.Contains(page, "console.example/cli-auth") {
		t.Fatalf("second start-login = %d %s", again.StatusCode, page)
	}
}

// A browser sign-in's grant follows its sliding mcp-login key: a week from
// the last verified use, never past 30 days after the key was issued. A
// pasted key's grant does not slide.
func TestBrowserConnectionsSlideWithTheirKey(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", StateStore: store})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	flow := f.begin(scopeRead)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	_ = review.Body.Close()
	approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	_ = approved.Body.Close()
	r := service.remote
	g := onlyGrant(t, r)
	r.mu.Lock()
	renewable, issued, first := g.renewable, g.credentialIssued, g.expires
	r.mu.Unlock()
	if !renewable || time.Since(issued) > time.Minute || first.After(time.Now().Add(2*time.Hour)) {
		t.Fatalf("browser grant renewable=%v issued=%v expires=%v", renewable, issued, first)
	}

	r.slideCredential(g)
	r.mu.Lock()
	slid := g.expires
	r.mu.Unlock()
	if slid.Before(time.Now().Add(api.MCPLoginKeyIdle - time.Minute)) {
		t.Fatalf("used connection expires %v, want a week out", slid)
	}
	var snapshot oauthSnapshot
	if err := store.read("oauth", &snapshot); err != nil {
		t.Fatal(err)
	}
	if record := snapshot.Grants[g.id]; !record.Renewable || !record.Expires.Equal(slid) {
		t.Fatalf("slid expiry not recorded: %+v", record)
	}

	r.mu.Lock()
	g.credentialIssued = time.Now().Add(-api.MCPLoginKeyMaxAge + 24*time.Hour)
	g.credentialExpires, g.expires = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	r.mu.Unlock()
	r.slideCredential(g)
	r.mu.Lock()
	capped := g.expires
	g.renewable = false
	g.credentialExpires, g.expires = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	pasted := g.expires
	r.mu.Unlock()
	if capped.After(time.Now().Add(25*time.Hour)) || capped.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("a connection 29 days old slid to %v, want 30 days after issue", capped)
	}
	r.slideCredential(g)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !g.expires.Equal(pasted) {
		t.Fatal("a pasted key's connection slid")
	}
}
