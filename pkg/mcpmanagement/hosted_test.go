// adr: 20261026195952
package mcpmanagement

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHostedBridgeIsItsOwnPathIssuer(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	var handler http.Handler
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	resource := server.URL + "/mcp"
	opts := HTTPOptions{APIBase: backend.URL, ResourceURL: resource, RedirectURIs: []string{testRedirect}, Hosted: true}
	var err error
	if handler, err = NewHTTPHandler(opts); err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string) (int, map[string]any) {
		t.Helper()
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var doc map[string]any
		_ = json.NewDecoder(response.Body).Decode(&doc)
		return response.StatusCode, doc
	}

	if status, doc := get("/.well-known/oauth-protected-resource/mcp"); status != 200 || doc["authorization_servers"].([]any)[0] != resource {
		t.Fatalf("resource metadata %d %v", status, doc)
	}
	for _, path := range []string{"/.well-known/oauth-authorization-server/mcp", "/.well-known/openid-configuration/mcp"} {
		status, doc := get(path)
		if status != 200 || doc["issuer"] != resource || doc["token_endpoint"] != resource+"/oauth/token" || doc["registration_endpoint"] != resource+"/oauth/register" || doc["authorization_endpoint"] != resource+"/oauth/authorize" {
			t.Fatalf("%s: %d %v", path, status, doc)
		}
	}
	// The host's root metadata and /oauth/* belong to the API.
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource"} {
		if status, _ := get(path); status != 404 {
			t.Fatalf("%s served by the hosted bridge: %d", path, status)
		}
	}
	register := `{"client_name":"hosted-agent","redirect_uris":["` + testRedirect + `"],"grant_types":["authorization_code","refresh_token"],"token_endpoint_auth_method":"none"}`
	root, err := client.Post(server.URL+"/oauth/register", "application/json", strings.NewReader(register))
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Body.Close()
	if root.StatusCode != 404 {
		t.Fatalf("root registration = %d", root.StatusCode)
	}
	response, err := client.Post(resource+"/oauth/register", "application/json", strings.NewReader(register))
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("registration: %v %v", response, err)
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	decodeResponse(t, response, &registered)

	sum := sha256.Sum256([]byte(testVerifier))
	query := url.Values{"client_id": {registered.ClientID}, "redirect_uri": {testRedirect}, "response_type": {"code"}, "resource": {resource}, "scope": {scopeRead}, "state": {"s"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	page, err := client.Get(resource + "/oauth/authorize?" + query.Encode())
	if err != nil || page.StatusCode != 200 {
		t.Fatalf("authorize: %v %v", page, err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if !strings.Contains(string(body), `action="`+resource+`/oauth/consent"`) {
		t.Fatalf("consent form does not post under /mcp: %s", body)
	}

	opts.Onboarding = "api-key"
	if _, err := NewHTTPHandler(opts); err == nil {
		t.Fatal("the hosted bridge accepted API-key onboarding")
	}

	// Registration is rate-limited per forwarded client address.
	registerFrom := func(address string) int {
		req, err := http.NewRequest(http.MethodPost, resource+"/oauth/register", strings.NewReader(register))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", address)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	for i := range hostedRegistrationLimit {
		if status := registerFrom("203.0.113.7"); status != 201 {
			t.Fatalf("registration %d = %d", i, status)
		}
	}
	if status := registerFrom("203.0.113.7"); status != 429 {
		t.Fatalf("registration over the limit = %d", status)
	}
	if status := registerFrom("203.0.113.8"); status != 201 {
		t.Fatalf("another address was limited: %d", status)
	}
}

func TestHostedBridgeCountsOnlyUsableGrantsPerAccount(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{Hosted: true}, grants: map[string]*oauthGrant{}}
	live := time.Now().Add(time.Hour)
	for i := range hostedGrantsPerAccount {
		r.grants[fmt.Sprint("live", i)] = &oauthGrant{accountID: "acct", credentialExpires: live}
	}
	r.grants["revoked"] = &oauthGrant{accountID: "acct", credentialExpires: live, revoked: true}
	r.grants["expired"] = &oauthGrant{accountID: "acct", credentialExpires: time.Now().Add(-time.Minute)}
	r.grants["other"] = &oauthGrant{accountID: "other", credentialExpires: live}
	if got := r.accountGrantsLocked("acct"); got != hostedGrantsPerAccount {
		t.Fatalf("usable grants = %d", got)
	}
	if r.capacity() != hostedOAuthEntries || (&remoteServer{}).capacity() != maxOAuthEntries {
		t.Fatal("capacity does not follow hosted mode")
	}
}
