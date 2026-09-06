package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_AppEnvSecrets(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "config-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, "default", "LOG_LEVEL", "debug"); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), acct.ID, app.ID, "staging", "FEATURE_X", "enabled"); err != nil {
		t.Fatalf("seed staging env: %v", err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(t.Context(), acct.ID, app.ID, "default", "DATABASE_URL", "kid-1", "deadbeef12345678", []byte("sealed-ciphertext")); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/config-app/env?scope=__all__", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Environment &amp; secrets: <code>config-app</code>",
		"LOG_LEVEL",
		"debug",
		"FEATURE_X",
		"DATABASE_URL",
		"deadbeef…",
		"write-only",
		"all scopes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "sealed-ciphertext") {
		t.Errorf("secret ciphertext leaked into dashboard body")
	}
}

func TestDashboardHandler_AppEnvSetUsesCSRFAndExistingHandler(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "config-write", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/dashboard/apps/config-write/env", nil)
	getReq.AddCookie(cookie)
	h.ServeHTTP(get, getReq)
	if get.Code != http.StatusOK {
		t.Fatalf("GET code = %d, want 200\nbody = %s", get.Code, get.Body.String())
	}
	token := dashboardInputToken(get.Body.String())
	if token == "" {
		t.Fatal("GET page missing env CSRF token")
	}
	csrfCookie := findDashboardCookie(get.Result().Cookies(), dashboardEnvCSRFCookie)
	if csrfCookie == nil {
		t.Fatal("GET page missing env CSRF cookie")
	}

	form := url.Values{"csrf_token": {token}, "scope": {"staging"}, "key": {"FEATURE_FLAG"}, "value": {"on"}}
	post := httptest.NewRecorder()
	postReq := httptest.NewRequest(http.MethodPost, "/dashboard/apps/config-write/env", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.AddCookie(cookie)
	postReq.AddCookie(csrfCookie)
	h.ServeHTTP(post, postReq)
	if post.Code != http.StatusFound {
		t.Fatalf("POST code = %d, want 302\nbody = %s", post.Code, post.Body.String())
	}
	rows, err := store.ListAppEnvInScope(t.Context(), acct.ID, app.ID, "staging")
	if err != nil || len(rows) != 1 || rows[0].Key != "FEATURE_FLAG" || rows[0].Value != "on" {
		t.Fatalf("staging env rows = %+v, err=%v", rows, err)
	}

	bad := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, "/dashboard/apps/config-write/env", strings.NewReader(form.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.AddCookie(cookie)
	h.ServeHTTP(bad, badReq)
	if bad.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(bad.Body.String()), "csrf") {
		t.Fatalf("bad CSRF response = %d %s", bad.Code, bad.Body.String())
	}
}

func TestParseAppEnvSecretsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "config-app/env", want: "config-app", ok: true},
		{path: "config-app/secrets/", want: "config-app", ok: true},
		{path: "config-app", ok: false},
		{path: "config-app/env/KEY", ok: false},
	} {
		got, ok := parseAppEnvSecretsPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAppEnvSecretsPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func dashboardInputToken(body string) string {
	const marker = `name="csrf_token" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.IndexByte(body[start:], '"')
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

func findDashboardCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
