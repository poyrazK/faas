// adr: 20261030065746
package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientMetadataURLs(t *testing.T) {
	for id, want := range map[string]bool{
		"https://client.example/oauth/client.json":   true,
		"https://client.example/mcp?app=1":           true,
		"https://client.example":                     false,
		"https://client.example/":                    false,
		"http://client.example/client.json":          false,
		"https://user:pw@client.example/client.json": false,
		"https://client.example/a/../client.json":    false,
		"https://client.example/client.json#frag":    false,
		"gmcp_client_abc":                            false,
	} {
		if got := isClientMetadataURL(id); got != want {
			t.Errorf("isClientMetadataURL(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestClientMetadataTTL(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"":                     clientMetadataDefaultTTL,
		"max-age=600":          10 * time.Minute,
		"public, max-age=10":   clientMetadataMinTTL,
		"max-age=999999":       clientMetadataMaxTTL,
		"no-store":             clientMetadataMinTTL,
		"max-age=not-a-number": clientMetadataDefaultTTL,
	} {
		if got := clientMetadataTTL(header); got != want {
			t.Errorf("clientMetadataTTL(%q) = %v, want %v", header, got, want)
		}
	}
}

// metadataDocumentServer serves one client document and counts fetches.
func metadataDocumentServer(t *testing.T, doc func(id string) map[string]any) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var fetches atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=600")
		_ = json.NewEncoder(w).Encode(doc(server.URL + req.URL.Path))
	}))
	t.Cleanup(server.Close)
	return server, &fetches
}

func authorizeURL(f *remoteFixture, clientID string) string {
	sum := sha256.Sum256([]byte(testVerifier))
	query := url.Values{"client_id": {clientID}, "redirect_uri": {testRedirect}, "response_type": {"code"}, "resource": {f.resource}, "scope": {scopeRead}, "state": {"client-state"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	return f.server.URL + f.prefix + "/oauth/authorize?" + query.Encode()
}

// A client identified by its metadata document connects without registering:
// consent names the site it is published at, only the operator's allowed
// redirects survive, and the document is cached.
func TestMetadataDocumentClientsConnect(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	docs, fetches := metadataDocumentServer(t, func(id string) map[string]any {
		return map[string]any{"client_id": id, "client_name": "Doc Agent", "redirect_uris": []string{testRedirect, "https://elsewhere.example/callback"}, "token_endpoint_auth_method": "none"}
	})
	service.remote.metadataClient = docs.Client()
	clientID := docs.URL + "/oauth/client.json"

	flow := f.beginAt(context.Background(), authorizeURL(f, clientID))
	if flow.requestID == "" {
		t.Fatal("no consent flow")
	}
	service.remote.mu.Lock()
	c := service.remote.clients[clientID]
	service.remote.mu.Unlock()
	if c == nil || !c.Metadata || c.Name != "Doc Agent" || strings.Join(c.Redirects, ",") != testRedirect || time.Until(c.Refresh) < 9*time.Minute {
		t.Fatalf("metadata client = %+v", c)
	}
	f.beginAt(context.Background(), authorizeURL(f, clientID))
	if fetches.Load() != 1 {
		t.Fatalf("document fetched %d times, want once while cached", fetches.Load())
	}
	page, err := f.client.Get(authorizeURL(f, clientID))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	host, _ := url.Parse(docs.URL)
	if !strings.Contains(string(body), "Published by <strong>"+host.Host+"</strong>") {
		t.Fatalf("consent does not name the client's site: %s", body)
	}
}

func TestMetadataDocumentsThatDoNotFitAreRefused(t *testing.T) {
	for name, doc := range map[string]func(id string) map[string]any{
		"client_id mismatch": func(string) map[string]any {
			return map[string]any{"client_id": "https://other.example/c.json", "redirect_uris": []string{testRedirect}}
		},
		"no allowed redirect": func(id string) map[string]any {
			return map[string]any{"client_id": id, "redirect_uris": []string{"https://elsewhere.example/cb"}}
		},
		"confidential client": func(id string) map[string]any {
			return map[string]any{"client_id": id, "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "private_key_jwt"}
		},
		"implicit response type": func(id string) map[string]any {
			return map[string]any{"client_id": id, "redirect_uris": []string{testRedirect}, "response_types": []string{"token"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", ClientMetadataDocuments: true})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			f.setHandler(service.Handler())
			docs, _ := metadataDocumentServer(t, doc)
			service.remote.metadataClient = docs.Client()
			response, err := f.client.Get(authorizeURL(f, docs.URL+"/client.json"))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid_client") {
				t.Fatalf("authorize = %d %s", response.StatusCode, body)
			}
		})
	}
}

// Every authorization response names the issuer (RFC 9207), and the server
// metadata says so; documents are advertised only where they are accepted.
func TestAuthorizationResponsesNameTheIssuer(t *testing.T) {
	f := newRemoteFixture(t, false)
	flow := f.begin(scopeRead)
	response := f.consent(flow, "verify", "fp_test_alice_remote_key", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	response = f.consent(flow, "approve", "", f.server.URL, flow.csrf, false)
	_ = response.Body.Close()
	redirect, err := url.Parse(response.Header.Get("Location"))
	if err != nil || redirect.Query().Get("iss") != f.server.URL || redirect.Query().Get("code") == "" {
		t.Fatalf("approval redirect %q lacks iss", response.Header.Get("Location"))
	}
	denied := f.begin(scopeRead)
	response = f.consent(denied, "deny", "", f.server.URL, denied.csrf, false)
	_ = response.Body.Close()
	if redirect, _ := url.Parse(response.Header.Get("Location")); redirect.Query().Get("iss") != f.server.URL || redirect.Query().Get("error") != "access_denied" {
		t.Fatalf("denial redirect %q lacks iss", response.Header.Get("Location"))
	}
	var metadata map[string]any
	decodeResponse(t, f.request(http.MethodGet, "/.well-known/oauth-authorization-server", nil, "", ""), &metadata)
	if metadata["authorization_response_iss_parameter_supported"] != true || metadata["client_id_metadata_document_supported"] != nil {
		t.Fatalf("metadata = %v", metadata)
	}
}

// A client stays registered as long as a grant it holds, so a restart after a
// day keeps a sliding connection (ADR-20261030065746).
func TestClientsLastAsLongAsTheirGrants(t *testing.T) {
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	f.authorizeAccount("fp_test_alice_remote_key", false)
	r := service.remote
	g := onlyGrant(t, r)
	r.mu.Lock()
	g.expires = time.Now().Add(20 * 24 * time.Hour)
	r.holdClientLocked(g)
	clientExpires := r.clients[g.clientID].Expires
	// The client's own registration would have lapsed: only the grant holds it.
	r.clients[g.clientID].Expires = time.Now().Add(-time.Hour)
	if err := r.opts.StateStore.update("oauth", r.snapshotLocked()); err != nil {
		r.mu.Unlock()
		t.Fatal(err)
	}
	r.mu.Unlock()
	if !clientExpires.Equal(g.expires) {
		t.Fatalf("client expires %v, want the grant's %v", clientExpires, g.expires)
	}
	restarted, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	restarted.remote.mu.Lock()
	defer restarted.remote.mu.Unlock()
	if len(restarted.remote.grants) != 1 || restarted.remote.clients[g.clientID] == nil {
		t.Fatalf("restart kept %d grants and client %v", len(restarted.remote.grants), restarted.remote.clients[g.clientID] != nil)
	}
}
