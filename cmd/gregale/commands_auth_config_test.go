package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
)

// TestAuthTemplateSettingsContract keeps the template's provider names and
// password character classes in step with pkg/authsettings.
func TestAuthTemplateSettingsContract(t *testing.T) {
	config, err := os.ReadFile("templates/auth/config.mjs")
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`export const providers = Object\.freeze\(\[([^\]]*)\]\)`).FindSubmatch(config)
	if list == nil {
		t.Fatal("providers not found in config.mjs")
	}
	var providers []string
	for _, quoted := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(list[1], -1) {
		providers = append(providers, string(quoted[1]))
	}
	if !reflect.DeepEqual(providers, authsettings.Providers) {
		t.Fatalf("template providers %v, Go providers %v", providers, authsettings.Providers)
	}
	for _, class := range authsettings.ClassOrder {
		// The template writes the symbol class's single quote escaped.
		literal := strings.ReplaceAll(authsettings.CharacterClasses[class], `'`, `\'`)
		if !strings.Contains(string(config), class+": '"+literal+"'") {
			t.Errorf("character class %s differs from the template", class)
		}
	}
}

// authSettingsWorld records secret writes and serves a running app's
// public settings.
func authSettingsWorld(t *testing.T, existing []string, running map[string]any) (map[string]string, *[]string, *int) {
	t.Helper()
	sealed, removed, restarts := map[string]string{}, []string{}, 0
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		const prefix = "/v1/apps/shop-auth/secrets"
		switch {
		case r.Method == "GET" && r.URL.Path == prefix:
			list := api.AppSecretListResponse{}
			for _, name := range existing {
				list.Secrets = append(list.Secrets, api.AppSecretResponse{Key: name, Scope: api.DefaultEnvScope})
			}
			writeJSONTest(w, list)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, prefix+"/"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			sealed[strings.TrimPrefix(r.URL.Path, prefix+"/")] = body.Value
			writeJSONTest(w, map[string]any{})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, prefix+"/"):
			name := strings.TrimPrefix(r.URL.Path, prefix+"/")
			removed = append(removed, name)
			if name == "AUTH_EMAIL_FROM" {
				w.WriteHeader(http.StatusNotFound)
				writeJSONTest(w, map[string]any{"status": 404, "code": "not_found"})
				return true
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop-auth/restart":
			restarts++
			writeJSONTest(w, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		default:
			return false
		}
		return true
	})
	old := authAdminHTTPClient
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		if r.URL.String() != "https://shop-auth.gregale.app/settings/v1" {
			t.Errorf("unexpected Auth app request %s", r.URL)
		}
		writeJSONTest(recorder, running)
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient = old })
	return sealed, &removed, &restarts
}

func TestAuthConfigWritesTypedSettingsAndRestarts(t *testing.T) {
	resetJSONEnv(t)
	sealed, removed, restarts := authSettingsWorld(t, nil, nil)
	captureAuthStdout(t)
	args := []string{"config", "shop-auth", "--signup", "off", "--password-min-length", "12", "--password-require", "lower,digit", "--mfa", "off", "--leaked-password-check", "on", "--redirect-urls", "none", "--email-provider", "none"}
	if code := cmdAuth(args); code != 0 {
		t.Fatalf("config = %d", code)
	}
	want := map[string]string{
		"AUTH_DISABLE_SIGNUP":                 "1",
		"AUTH_REDIRECT_URLS":                  "",
		"GOTRUE_PASSWORD_MIN_LENGTH":          "12",
		"GOTRUE_PASSWORD_REQUIRED_CHARACTERS": authsettings.CharacterClasses["lower"] + ":" + authsettings.CharacterClasses["digit"],
		"GOTRUE_PASSWORD_HIBP_ENABLED":        "true",
		"GOTRUE_MFA_TOTP_ENROLL_ENABLED":      "false",
		"GOTRUE_MFA_TOTP_VERIFY_ENABLED":      "true",
	}
	if !reflect.DeepEqual(sealed, want) {
		t.Fatalf("sealed = %v", sealed)
	}
	sort.Strings(*removed)
	// A missing secret is not an error when removing it.
	if !reflect.DeepEqual(*removed, []string{"AUTH_EMAIL_API_KEY", "AUTH_EMAIL_FROM", "AUTH_EMAIL_PROVIDER"}) || *restarts != 1 {
		t.Fatalf("removed = %v restarts = %d", *removed, *restarts)
	}
	for _, bad := range [][]string{
		{"config", "shop-auth", "--signup", "maybe"},
		{"config", "shop-auth", "--password-min-length", "4"},
		{"config", "shop-auth", "--password-require", "lower,emoji"},
		{"config", "shop-auth", "--site-url", "http://shop.example.com"},
		{"config", "shop-auth", "--email-provider", "none", "--email-from", "a@example.com"},
	} {
		if code := cmdAuth(bad); code == 0 {
			t.Errorf("%v accepted", bad)
		}
	}
	if len(sealed) != len(want) || *restarts != 1 {
		t.Fatal("an invalid change wrote settings")
	}
}

func TestAuthConfigShowsTheRunningSettings(t *testing.T) {
	resetJSONEnv(t)
	running := map[string]any{
		"app":           "shop.example.com",
		"site_url":      "https://shop.example.com",
		"redirect_urls": []string{},
		"signup":        map[string]any{"enabled": true, "autoconfirm": false},
		"providers":     []string{"google"},
		"password":      map[string]any{"min_length": 12, "required": []string{"lower"}, "leaked_password_check": true},
		"mfa":           map[string]any{"totp": true},
		"email":         map[string]any{"configured": false},
		"keys":          map[string]any{"signing": "0A1B2C3D", "verifying": []string{"FFFF0001"}},
	}
	sealed, _, restarts := authSettingsWorld(t, nil, running)
	out := captureAuthStdout(t)
	if code := cmdAuth([]string{"config", "shop-auth"}); code != 0 || len(sealed) != 0 || *restarts != 0 {
		t.Fatalf("show = %d, wrote %v", code, sealed)
	}
	for _, line := range []string{"sign-in methods: email, google", "at least 12 characters; must include lower; leaked-password check on", "email delivery:  not configured", "signing key:     0A1B2C3D (1 more verifying)"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("output lacks %q:\n%s", line, out)
		}
	}
	out.Reset()
	jsonOutput = true
	if code := cmdAuth([]string{"config", "shop-auth"}); code != 0 {
		t.Fatal("show --json failed")
	}
	var shown authPublicSettings
	if err := json.Unmarshal(out.Bytes(), &shown); err != nil || shown.Password.MinLength != 12 || shown.Keys.Signing != "0A1B2C3D" {
		t.Fatalf("json = %s %v", out, err)
	}
}

func TestAuthProvidersEnableReplacesAndDisableRemoves(t *testing.T) {
	resetJSONEnv(t)
	existing := []string{"GOTRUE_EXTERNAL_GITLAB_URL", "GOTRUE_EXTERNAL_GITLAB_SECRET", "GOTRUE_EXTERNAL_GOOGLE_SECRET", "AUTH_SITE_URL"}
	sealed, removed, restarts := authSettingsWorld(t, existing, nil)
	out := captureAuthStdout(t)
	oldIn := osStdin
	osStdin = strings.NewReader("gitlab-secret\n")
	t.Cleanup(func() { osStdin = oldIn })
	if code := cmdAuth([]string{"providers", "enable", "shop-auth", "gitlab", "--client-id", "app-id", "--client-secret-stdin"}); code != 0 {
		t.Fatalf("enable = %d", code)
	}
	want := map[string]string{"GOTRUE_EXTERNAL_GITLAB_ENABLED": "true", "GOTRUE_EXTERNAL_GITLAB_CLIENT_ID": "app-id", "GOTRUE_EXTERNAL_GITLAB_SECRET": "gitlab-secret"}
	if !reflect.DeepEqual(sealed, want) || !reflect.DeepEqual(*removed, []string{"GOTRUE_EXTERNAL_GITLAB_URL"}) || *restarts != 1 {
		t.Fatalf("sealed = %v removed = %v", sealed, *removed)
	}
	if !strings.Contains(out.String(), "https://shop-auth.gregale.app/auth/v1/callback") || strings.Contains(out.String(), "gitlab-secret") {
		t.Fatalf("output = %s", out)
	}
	*removed = nil
	if code := cmdAuth([]string{"providers", "disable", "shop-auth", "gitlab"}); code != 0 {
		t.Fatal("disable failed")
	}
	sort.Strings(*removed)
	if !reflect.DeepEqual(*removed, []string{"GOTRUE_EXTERNAL_GITLAB_SECRET", "GOTRUE_EXTERNAL_GITLAB_URL"}) {
		t.Fatalf("disable removed %v", *removed)
	}
	if code := cmdAuth([]string{"providers", "enable", "shop-auth", "myspace", "--client-id", "x"}); code == 0 {
		t.Fatal("enabled an unknown provider")
	}
}
