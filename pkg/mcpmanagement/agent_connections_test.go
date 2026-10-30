// adr: 20261030133644
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// nativeKey is the mcp-login key the fixture's browser sign-in mints.
const nativeKey = "fp_test_alice_remote_key"

func browserService(t *testing.T, options HTTPOptions) (*remoteFixture, *remoteServer) {
	t.Helper()
	options.Onboarding = "browser"
	f := newRemoteFixtureWithOptions(t, false, options)
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	return f, service.remote
}

// signInNatively signs in through the browser, then sends decision.
func signInNatively(f *remoteFixture, decision string) *http.Response {
	flow := f.begin(scopeRead)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	_ = review.Body.Close()
	return f.consent(flow, decision, "", f.server.URL, flow.csrf, false)
}

// A browser sign-in names its client on its key, and a denied one retires
// the key so the account does not list it as connected.
func TestBrowserSignInNamesItsClientAndDenialRetiresItsKey(t *testing.T) {
	f, _ := browserService(t, HTTPOptions{})
	denied := signInNatively(f, "deny")
	_ = denied.Body.Close()
	f.mu.Lock()
	named := f.agentClients[nativeKey]
	f.mu.Unlock()
	if named != (api.AgentClientRequest{ClientName: "test-agent", ClientSite: "client.example"}) {
		t.Fatalf("named %+v, want the registered client at its redirect host", named)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		retired := f.disconnects[nativeKey]
		f.mu.Unlock()
		if retired == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("denied sign-in retired its key %d times, want once", retired)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A connection whose key the customer revoked is dropped on its next
// request; its key is not retired again.
func TestRevokedKeyDropsItsConnection(t *testing.T) {
	f, r := browserService(t, HTTPOptions{})
	approved := signInNatively(f, "approve")
	_ = approved.Body.Close()
	redirect, err := url.Parse(approved.Header.Get("Location"))
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatalf("approval redirect %q", approved.Header.Get("Location"))
	}
	exchanged := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	var tokens tokenResponse
	if err := json.NewDecoder(exchanged.Body).Decode(&tokens); err != nil || tokens.AccessToken == "" {
		t.Fatalf("token exchange = %d %v", exchanged.StatusCode, err)
	}
	_ = exchanged.Body.Close()
	f.mu.Lock()
	delete(f.accounts, nativeKey)
	f.mu.Unlock()

	response := f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", tokens.AccessToken)
	_ = response.Body.Close()
	r.mu.Lock()
	left := len(r.grants)
	r.mu.Unlock()
	f.mu.Lock()
	retired := f.disconnects[nativeKey]
	f.mu.Unlock()
	if response.StatusCode != http.StatusUnauthorized || left != 0 || retired != 0 {
		t.Fatalf("request = %d, %d connections left, key retired %d times; want 401, none, none", response.StatusCode, left, retired)
	}
}

// At the per-account limit, a sign-in first lets go of connections whose
// keys the customer revoked.
func TestSignInAtTheLimitReleasesRevokedConnections(t *testing.T) {
	f, r := browserService(t, HTTPOptions{Hosted: true})
	r.mu.Lock()
	for i := range hostedGrantsPerAccount {
		id := fmt.Sprintf("gmcp_grant_revoked_%d", i)
		r.grants[id] = &oauthGrant{id: id, clientID: f.clientID, accountID: "alice", approved: true, renewable: true, client: r.apiClient("fp_test_revoked"), expires: time.Now().Add(time.Hour), credentialExpires: time.Now().Add(time.Hour)}
	}
	r.mu.Unlock()
	flow := f.begin(scopeRead)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
	_ = start.Body.Close()
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	if page := readPage(t, review); review.StatusCode != http.StatusOK || strings.Contains(page, "agent connections") {
		t.Fatalf("sign-in at the limit = %d %s", review.StatusCode, page)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := r.accountGrantsLocked("alice"); n != 1 {
		t.Fatalf("alice holds %d connections, want only the new one", n)
	}
}
