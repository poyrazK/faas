package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAuthSSOBodyChecksFlagsBeforeTheKeyPrompt(t *testing.T) {
	flags := func(names ...string) map[string]bool {
		given := map[string]bool{}
		for _, name := range names {
			given[name] = true
		}
		return given
	}
	body, err := authSSOBody("add", flags("metadata-url", "domains", "org"), "https://idp.example.com/m", "", "Acme.com, acme.io ,", "acme", "member")
	want := map[string]any{"metadata_url": "https://idp.example.com/m", "domains": []string{"acme.com", "acme.io"}, "organization": "acme", "role": "member"}
	if err != nil || !reflect.DeepEqual(body, want) {
		t.Fatalf("add body = %v, %v", body, err)
	}
	body, err = authSSOBody("update", flags("org"), "", "", "", "none", "member")
	if err != nil || !reflect.DeepEqual(body, map[string]any{"organization": nil}) {
		t.Fatalf("unlink body = %v, %v", body, err)
	}
	metadata := filepath.Join(t.TempDir(), "metadata.xml")
	if err := os.WriteFile(metadata, []byte("<EntityDescriptor/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := authSSOBody("add", flags("metadata-file", "domains"), "", metadata, "acme.com", "", "member"); err != nil || body["metadata_xml"] != "<EntityDescriptor/>" {
		t.Fatalf("metadata file body = %v, %v", body, err)
	}
	for name, tc := range map[string]struct {
		command string
		given   []string
		url     string
		domains string
		org     string
		role    string
	}{
		"add without metadata":  {"add", []string{"domains"}, "", "acme.com", "", "member"},
		"add without domains":   {"add", []string{"metadata-url"}, "https://idp.example.com/m", "", "", "member"},
		"plain HTTP metadata":   {"add", []string{"metadata-url", "domains"}, "http://idp.example.com/m", "acme.com", "", "member"},
		"owner role":            {"add", []string{"metadata-url", "domains", "org", "role"}, "https://idp.example.com/m", "acme.com", "acme", "owner"},
		"role without org":      {"update", []string{"role"}, "", "", "", "admin"},
		"role when unlinking":   {"update", []string{"org", "role"}, "", "", "none", "admin"},
		"empty update":          {"update", nil, "", "", "", "member"},
		"settings on remove":    {"remove", []string{"domains"}, "", "acme.com", "", "member"},
		"both metadata sources": {"add", []string{"metadata-url", "metadata-file", "domains"}, "https://idp.example.com/m", "acme.com", "", "member"},
	} {
		if _, err := authSSOBody(tc.command, flags(tc.given...), tc.url, metadata, tc.domains, tc.org, tc.role); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAuthSSOAddCallsTheAdminAPIAndPrintsTheServiceProvider(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	var request struct {
		method, path string
		body         map[string]any
	}
	old := authAdminHTTPClient
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.Path == "/settings/v1" {
			writeJSONTest(recorder, map[string]any{"keys": map[string]any{"signing": "0A1B2C3D"}, "sso": map[string]any{"enabled": true,
				"entity_id": "https://shop-auth.gregale.app/auth/v1/sso/saml/metadata", "acs_url": "https://shop-auth.gregale.app/auth/v1/sso/saml/acs"}})
			return recorder.Result(), nil
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			recorder.WriteHeader(http.StatusUnauthorized)
			return recorder.Result(), nil
		}
		raw, _ := io.ReadAll(r.Body)
		request.method, request.path, request.body = r.Method, r.URL.Path, nil
		_ = json.Unmarshal(raw, &request.body)
		recorder.WriteHeader(http.StatusCreated)
		writeJSONTest(recorder, map[string]any{"provider": map[string]any{"id": "6a1b2c3d-0000-4000-8000-000000000001", "domains": []string{"acme.com"},
			"organization": map[string]any{"id": "o1", "slug": "acme", "role": "member"}}, "members_added": 2})
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient = old })
	oldIn := osStdin
	osStdin = strings.NewReader(key + "\n")
	t.Cleanup(func() { osStdin = oldIn })
	out := captureAuthStdout(t)
	if code := cmdAuth([]string{"sso", "add", "shop-auth", "--metadata-url", "https://idp.example.com/m", "--domains", "acme.com", "--org", "acme", "--admin-key-stdin"}); code != 0 {
		t.Fatalf("sso add = %d", code)
	}
	if request.method != http.MethodPost || request.path != "/admin/v1/sso" || request.body["organization"] != "acme" || request.body["metadata_url"] != "https://idp.example.com/m" {
		t.Fatalf("request = %+v", request)
	}
	for _, want := range []string{"users join acme as member", "2 existing users", "entity ID (metadata URL)  https://shop-auth.gregale.app/auth/v1/sso/saml/metadata", "ACS URL                   https://shop-auth.gregale.app/auth/v1/sso/saml/acs"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if code := cmdAuth([]string{"sso", "remove", "shop-auth", "not-a-provider-id"}); code == 0 {
		t.Fatal("accepted a malformed provider ID")
	}
}

func TestAuthConfigSSOCreatesTheSAMLKeyOnce(t *testing.T) {
	resetJSONEnv(t)
	sealed, _, _ := authSettingsWorld(t, nil, nil)
	captureAuthStdout(t)
	if code := cmdAuth([]string{"config", "shop-auth", "--sso", "on"}); code != 0 {
		t.Fatalf("config = %d", code)
	}
	if sealed["AUTH_SSO"] != "1" || len(sealed["AUTH_SAML_PRIVATE_KEY"]) < 1000 {
		t.Fatalf("sealed = %v", sortedKeys(sealed))
	}
	delete(sealed, "AUTH_SAML_PRIVATE_KEY")
	if code := cmdAuth([]string{"config", "shop-auth", "--sso", "off"}); code != 0 || sealed["AUTH_SSO"] != "0" || sealed["AUTH_SAML_PRIVATE_KEY"] != "" {
		t.Fatalf("off = %d, sealed %v", code, sortedKeys(sealed))
	}
}

func TestAuthConfigSSOKeepsAnExistingSAMLKey(t *testing.T) {
	resetJSONEnv(t)
	sealed, _, _ := authSettingsWorld(t, []string{"AUTH_SAML_PRIVATE_KEY"}, nil)
	captureAuthStdout(t)
	if code := cmdAuth([]string{"config", "shop-auth", "--sso", "on"}); code != 0 || sealed["AUTH_SSO"] != "1" {
		t.Fatalf("config = %d, sealed %v", code, sortedKeys(sealed))
	}
	if _, replaced := sealed["AUTH_SAML_PRIVATE_KEY"]; replaced {
		t.Fatal("an existing SAML key was replaced, which breaks every identity provider's trust")
	}
}
