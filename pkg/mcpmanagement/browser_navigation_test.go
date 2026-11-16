// adr: 20261116170000
package mcpmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

type htmlNavigationTransport struct{ http.RoundTripper }

func (tr htmlNavigationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	return tr.RoundTripper.RoundTrip(r)
}

// Read the document's real navigation and its fallback, rather than treating a
// cross-origin Location as proof a browser can leave a strict-CSP form.
func browserNavigationTarget(t *testing.T, response *http.Response) *url.URL {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" {
		t.Fatalf("browser POST did not finish with an HTML document: %d", response.StatusCode)
	}
	page, err := html.Parse(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var refresh, fallback, style string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		attrs := map[string]string{}
		for _, attr := range n.Attr {
			attrs[attr.Key] = attr.Val
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "meta":
				if strings.EqualFold(attrs["http-equiv"], "refresh") {
					refresh = strings.TrimPrefix(attrs["content"], "0;url=")
				}
			case "a":
				fallback = attrs["href"]
			case "style":
				if n.FirstChild != nil {
					style = n.FirstChild.Data
				}
			case "script":
				t.Error("OAuth handoff unexpectedly needs a script")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(page)
	target, err := url.Parse(refresh)
	if err != nil || target.Host == "" || refresh != fallback {
		t.Fatal("handoff must navigate to exactly the fallback link")
	}
	policy := response.Header.Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(style))
	if !strings.Contains(policy, privateContentSecurityPolicy) || style == "" || !strings.Contains(policy, "style-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || strings.Contains(policy, "unsafe-inline") {
		t.Fatal("handoff must keep scripts, other styles and cross-origin forms blocked")
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("OAuth handoff must not cache or leak its URL through referrers")
	}
	return target
}

func TestAntigravityBrowserSignInAndCallback(t *testing.T) {
	for _, decision := range []string{"approve", "deny"} {
		t.Run(decision, func(t *testing.T) {
			f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", Hosted: true, ClientMetadataDocuments: true, RedirectURIs: append(packagedBridgeRedirects(t), testRedirect)})
			service, err := NewHTTPService(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			service.remote.metadataClient = &http.Client{Transport: antigravityMetadataTransport{t}}
			f.setHandler(service.Handler())
			authorize, _ := url.Parse(authorizeURL(f, antigravityClientID))
			query := authorize.Query()
			query.Set("redirect_uri", antigravityCallback)
			authorize.RawQuery = query.Encode()
			flow := f.beginAt(context.Background(), authorize.String())
			flow.client.Transport = htmlNavigationTransport{flow.client.Transport}

			start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}}, f.server.URL)
			login := browserNavigationTarget(t, start)
			if login.Scheme != "https" || login.Host != "console.example" || login.Path != "/cli-auth" || login.Query().Get("return") != "mcp" {
				t.Fatal("browser did not get the native sign-in destination")
			}
			// The native fixture approves the synthetic code. Continue rotates
			// the CSRF token before the user reviews account/app consent.
			response, err := flow.client.Get(f.server.URL + "/mcp/oauth/continue")
			if err != nil {
				t.Fatal(err)
			}
			page := readPage(t, response)
			if response.StatusCode != http.StatusOK || strings.Contains(page, f.opts.APIBase) || !strings.Contains(page, f.resource) {
				t.Fatal("hosted consent must show its public server, never apid's internal listener")
			}
			csrf := ""
			for _, match := range hiddenInput.FindAllStringSubmatch(page, -1) {
				if match[1] == "csrf" {
					csrf = match[2]
				}
			}
			if csrf == "" || csrf == flow.csrf {
				t.Fatal("review did not rotate CSRF")
			}
			callback := browserNavigationTarget(t, f.consent(flow, decision, "", f.server.URL, csrf, false))
			if callback.Scheme+"://"+callback.Host+callback.Path != antigravityCallback || callback.Query().Get("state") != "client-state" || callback.Query().Get("iss") != f.resource {
				t.Fatal("browser callback lost its registered destination, state or issuer")
			}
			if decision == "deny" {
				if callback.Query().Get("error") != "access_denied" || callback.Query().Get("code") != "" {
					t.Fatal("denial issued an authorization code")
				}
				return
			}
			form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "code_verifier": {testVerifier}, "resource": {f.resource}, "client_id": {antigravityClientID}, "redirect_uri": {antigravityCallback}}
			response = f.request(http.MethodPost, f.prefix+"/oauth/token", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "")
			var tokens tokenResponse
			decodeResponse(t, response, &tokens)
			if response.StatusCode != http.StatusOK || tokens.AccessToken == "" {
				t.Fatalf("token exchange: HTTP %d", response.StatusCode)
			}
			client, ctx := f.connect(tokens, "2026-07-28")
			defer client.Close()
			catalog, err := client.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) == 0 {
				t.Fatalf("authenticated MCP tool discovery: %v", err)
			}
		})
	}
}

func TestOAuthNavigationEscapesDestinationAndKeepsNonBrowserRedirect(t *testing.T) {
	target := `https://client.example/callback?state=a&code=b%22%3E%3Cscript%3E&label='quoted'`
	for _, accept := range []string{"text/html", "application/json", "text/html;q=0", "*/*"} {
		t.Run(accept, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://bridge.example/oauth/consent", nil)
			req.Header.Set("Accept", accept)
			response := httptest.NewRecorder()
			setPrivateHeaders(response)
			redirectOAuthBrowser(response, req, target, `Return to <untrusted>`)
			if accept == "text/html" {
				if got := browserNavigationTarget(t, response.Result()).String(); got != target {
					t.Fatal("HTML encoding changed the navigation target")
				}
				if strings.Contains(response.Body.String(), "<untrusted>") {
					t.Fatal("unescaped handoff title")
				}
			} else if response.Code != http.StatusSeeOther || response.Header().Get("Location") != target {
				t.Fatal("non-browser OAuth redirect changed")
			}
		})
	}
}

func TestHostedConsentRecoveryStaysInTheSameTab(t *testing.T) {
	page := consentPage(consentView{Native: true, SameTab: true, LoginURL: "https://console.example/cli-auth?code=fixture", RequestID: "r", CSRF: "c"})
	if strings.Contains(page, `target="_blank"`) || !strings.Contains(page, `href="https://console.example/cli-auth?code=fixture"`) {
		t.Fatal("hosted recovery should not open a second sign-in tab")
	}
}
