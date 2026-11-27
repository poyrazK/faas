package mcpmanagement

import "testing"

// `gregale mcp connect` configured Cursor and VS Code for OAuth sign-in, but
// the hosted bridge refused their callbacks at registration: only HTTPS and
// loopback /callback URIs passed, and a private-use scheme never could.
// This pins the packaged policy that admits them, and what it still refuses.
func TestPackagedRedirectPolicyAdmitsNativeClients(t *testing.T) {
	policy := &remoteServer{opts: HTTPOptions{RedirectURIs: packagedBridgeRedirects(t)}}
	for _, redirect := range []string{
		"cursor://anysphere.cursor-mcp/oauth/callback",
		"https://vscode.dev/redirect",
		"http://127.0.0.1:33418/",
		"http://127.0.0.1:33418",
		"http://localhost:7777/callback",
		"http://127.0.0.1:51234/oauth/callback",
		"http://localhost:51234/oauth/callback",
		"http://localhost:33418/",
		"http://localhost:8787/callback",
		"https://claude.ai/api/mcp/auth_callback",
	} {
		if !policy.redirectAllowed(redirect) {
			t.Errorf("refused %s", redirect)
		}
	}
	for _, redirect := range []string{
		"cursor://anysphere.cursor-mcp/oauth/other",
		"cursor://evil.example/oauth/callback",
		"javascript://anysphere.cursor-mcp/oauth/callback",
		"https://evil.example/redirect",
		"http://evil.example/",
		"http://127.0.0.1:33418/other",
		"http://127.0.0.1:51234/oauth/callback/extra",
		"http://evil.example:51234/oauth/callback",
		"http://localhost:51234/oauth/callback/extra",
		"http://localhost.evil.example:51234/oauth/callback",
		"https://vscode.dev/redirect/extra",
	} {
		if policy.redirectAllowed(redirect) {
			t.Errorf("allowed %s", redirect)
		}
	}
}

func TestPrivateUseSchemesAreOperatorListedOnly(t *testing.T) {
	for _, value := range []string{"cursor://anysphere.cursor-mcp/oauth/callback", "com.example.app://host/cb"} {
		if !validConfiguredRedirectURI(value) {
			t.Errorf("operator cannot list %s", value)
		}
	}
	for _, value := range []string{"javascript://x/y", "data://x/y", "file://host/etc/passwd", "intent://x/y", "cursor:/no-host", "cursor://host/cb#frag", "cursor://user@host/cb"} {
		if validRedirectURI(value) {
			t.Errorf("accepted %s", value)
		}
	}
	policy := &remoteServer{opts: HTTPOptions{RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}}}
	if policy.redirectAllowed("cursor://anysphere.cursor-mcp/oauth/callback") {
		t.Fatal("a private-use scheme passed without being listed")
	}
}
