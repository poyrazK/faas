package mcpmanagement

import "testing"

func TestCodexLoopbackCallbackPolicyIsNarrow(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{RedirectURIs: []string{"http://127.0.0.1/callback/*"}}}
	for _, test := range []struct {
		uri     string
		allowed bool
	}{
		{"http://127.0.0.1:43210/callback/abc_123-X", true},
		{"http://127.0.0.1:43210/callback", false},
		{"http://127.0.0.1:43210/callback/", false},
		{"http://127.0.0.1:43210/callback/a/b", false},
		{"http://127.0.0.1:43210/callback/%2Fa", false},
		{"http://127.0.0.1:43210/callback/../elsewhere", false},
		{"http://127.0.0.1:43210/callback/a?redirect=elsewhere", false},
		{"http://localhost:43210/callback/abc", false},
		{"https://127.0.0.1:43210/callback/abc", false},
		{"http://attacker.example:43210/callback/abc", false},
	} {
		if got := r.redirectAllowed(test.uri); got != test.allowed {
			t.Errorf("redirect %s: got %v", test.uri, got)
		}
	}
	for _, uri := range []string{"https://client.example/callback/*", "http://127.0.0.1/*", "http://127.0.0.1/callback/*?q=*"} {
		if validConfiguredRedirectURI(uri) {
			t.Errorf("broad callback rule accepted: %s", uri)
		}
	}
	if validRedirectURI("http://127.0.0.1:43210/callback/*") {
		t.Fatal("client registered a wildcard instead of its exact callback")
	}
}
