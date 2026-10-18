package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAuthOAuthClientBodyChecksFlagsBeforeTheKeyPrompt(t *testing.T) {
	flags := func(names ...string) map[string]bool {
		given := map[string]bool{}
		for _, name := range names {
			given[name] = true
		}
		return given
	}
	body, err := authOAuthClientBody("add", flags("name", "redirect-uri", "client-uri"), "Shop CLI", []string{"http://127.0.0.1:8976/callback", "shop://oauth"}, "public", "https://shop.example.com", "")
	want := map[string]any{"client_name": "Shop CLI", "redirect_uris": []string{"http://127.0.0.1:8976/callback", "shop://oauth"}, "client_type": "public", "client_uri": "https://shop.example.com"}
	if err != nil || !reflect.DeepEqual(body, want) {
		t.Fatalf("add body = %v, %v", body, err)
	}
	if body, err := authOAuthClientBody("update", flags("name"), "Shop", nil, "public", "", ""); err != nil || !reflect.DeepEqual(body, map[string]any{"client_name": "Shop"}) {
		t.Fatalf("update body = %v, %v", body, err)
	}
	if body, err := authOAuthClientBody("rotate-secret", flags("admin-key-stdin"), "", nil, "public", "", ""); err != nil || body != nil {
		t.Fatalf("rotate body = %v, %v", body, err)
	}
	for name, tc := range map[string]struct {
		command   string
		given     []string
		redirects []string
		kind      string
		uri       string
	}{
		"add without redirects":   {"add", []string{"name"}, nil, "public", ""},
		"plain HTTP redirect":     {"add", []string{"redirect-uri"}, []string{"http://shop.example.com/cb"}, "public", ""},
		"script redirect":         {"add", []string{"redirect-uri"}, []string{"javascript://x/alert(1)"}, "public", ""},
		"redirect with fragment":  {"add", []string{"redirect-uri"}, []string{"https://shop.example.com/cb#x"}, "public", ""},
		"unknown type":            {"add", []string{"redirect-uri", "type"}, []string{"https://shop.example.com/cb"}, "implicit", ""},
		"type change on update":   {"update", []string{"type"}, nil, "confidential", ""},
		"plain HTTP client URI":   {"add", []string{"redirect-uri", "client-uri"}, []string{"https://shop.example.com/cb"}, "public", "http://shop.example.com"},
		"settings on remove":      {"remove", []string{"name"}, nil, "public", ""},
		"empty update":            {"update", nil, nil, "public", ""},
		"eleven redirect targets": {"add", []string{"redirect-uri"}, strings.Split(strings.Repeat("https://a.example/cb,", 11), ",")[:11], "public", ""},
	} {
		if _, err := authOAuthClientBody(tc.command, flags(tc.given...), "Shop", tc.redirects, tc.kind, tc.uri, ""); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAuthOAuthClientsAddPrintsTheSecretAndTheServer(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	var seen []string
	var sent map[string]any
	old := authAdminHTTPClient
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.Path == "/settings/v1" {
			writeJSONTest(recorder, map[string]any{"keys": map[string]any{"signing": "0A1B2C3D"}, "oauth": map[string]any{"enabled": true, "dynamic_registration": false,
				"issuer": "https://shop-auth.gregale.app/auth/v1", "metadata_url": "https://shop-auth.gregale.app/.well-known/oauth-authorization-server/auth/v1"}})
			return recorder.Result(), nil
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			recorder.WriteHeader(http.StatusUnauthorized)
			return recorder.Result(), nil
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		sent = nil
		_ = json.Unmarshal(raw, &sent)
		recorder.WriteHeader(http.StatusCreated)
		writeJSONTest(recorder, map[string]any{"client": map[string]any{"id": "6a1b2c3d-0000-4000-8000-000000000002", "name": "Shop server", "type": "confidential", "registration": "manual",
			"redirect_uris": []string{"https://shop.example.com/cb"}}, "client_secret": "s3cret"})
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient = old })
	oldIn := osStdin
	t.Cleanup(func() { osStdin = oldIn })
	out := captureAuthStdout(t)
	osStdin = strings.NewReader(key + "\n")
	if code := cmdAuth([]string{"oauth-clients", "add", "shop-auth", "--name", "Shop server", "--type", "confidential", "--redirect-uri", "https://shop.example.com/cb", "--admin-key-stdin"}); code != 0 {
		t.Fatalf("add = %d", code)
	}
	if sent["client_type"] != "confidential" || sent["client_name"] != "Shop server" {
		t.Fatalf("sent %v", sent)
	}
	osStdin = strings.NewReader(key + "\n")
	if code := cmdAuth([]string{"oauth-clients", "rotate-secret", "shop-auth", "6a1b2c3d-0000-4000-8000-000000000002", "--admin-key-stdin"}); code != 0 {
		t.Fatalf("rotate = %d", code)
	}
	want := []string{"POST /admin/v1/oauth/clients", "POST /admin/v1/oauth/clients/6a1b2c3d-0000-4000-8000-000000000002/regenerate-secret"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests %v", seen)
	}
	for _, line := range []string{"s3cret", "issuer        https://shop-auth.gregale.app/auth/v1", "metadata URL  https://shop-auth.gregale.app/.well-known/oauth-authorization-server/auth/v1"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("output lacks %q:\n%s", line, out)
		}
	}
	if code := cmdAuth([]string{"oauth-clients", "remove", "shop-auth", "not-a-client-id"}); code == 0 {
		t.Fatal("accepted a malformed client ID")
	}
}

func TestAuthConfigOAuthServer(t *testing.T) {
	resetJSONEnv(t)
	sealed, _, _ := authSettingsWorld(t, nil, nil)
	captureAuthStdout(t)
	if code := cmdAuth([]string{"config", "shop-auth", "--oauth-server", "on", "--oauth-registration", "on"}); code != 0 || sealed["AUTH_OAUTH_SERVER"] != "1" || sealed["AUTH_OAUTH_REGISTRATION"] != "1" {
		t.Fatalf("config = %d, sealed %v", code, sortedKeys(sealed))
	}
	if code := cmdAuth([]string{"config", "shop-auth", "--oauth-server", "off"}); code != 0 || sealed["AUTH_OAUTH_SERVER"] != "0" {
		t.Fatalf("off = %d, sealed %v", code, sortedKeys(sealed))
	}
	if code := cmdAuth([]string{"config", "shop-auth", "--oauth-registration", "maybe"}); code == 0 {
		t.Fatal("accepted an invalid switch")
	}
}
