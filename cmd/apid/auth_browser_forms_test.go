package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #8 (H8-28): the API host's sign-in page linked to
// GET /signup and GET /login/forgot (both 405), and its HTML form received the
// JSON body meant for the web console.

const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func browserFormServer(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	store := state.NewMemStore()
	const email, password = "browser@example.com", "correct-horse-battery-staple"
	acct, err := store.CreateAccount(context.Background(), email, api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	phc, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountPassword(context.Background(), acct.ID, phc); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	return srv.handler(), email, password
}

func browserPost(h http.Handler, path string, form url.Values, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBrowserLoginRedirectsToSanitizedNext(t *testing.T) {
	h, email, password := browserFormServer(t)
	for _, tc := range []struct{ next, want string }{
		{next: "/dashboard/apps", want: "/dashboard/apps"},
		{next: "https://evil.example/", want: "/dashboard/"},
		{next: "", want: "/dashboard/"},
	} {
		rec := browserPost(h, "/login", url.Values{"email": {email}, "password": {password}, "next": {tc.next}}, browserAccept)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.want {
			t.Fatalf("next %q: status %d location %q, want 303 %q", tc.next, rec.Code, rec.Header().Get("Location"), tc.want)
		}
		if !strings.Contains(rec.Header().Get("Set-Cookie"), sessionCookie+"=") {
			t.Fatalf("next %q: no session cookie forwarded", tc.next)
		}
	}
}

func TestBrowserLoginFailureRerendersForm(t *testing.T) {
	h, email, _ := browserFormServer(t)
	rec := browserPost(h, "/login", url.Values{"email": {email}, "password": {"wrong-password-123"}, "next": {"/dashboard/apps"}}, browserAccept)
	body := rec.Body.String()
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status %d content-type %q, want 401 html", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(body, "Email or password is incorrect") || !strings.Contains(body, `name="next" value="/dashboard/apps"`) {
		t.Fatalf("form did not explain the failure or keep next:\n%s", body)
	}
}

func TestAPIClientFormLoginKeepsJSON(t *testing.T) {
	h, email, password := browserFormServer(t)
	rec := browserPost(h, "/login", url.Values{"email": {email}, "password": {password}}, "*/*")
	var body map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["account_id"] == nil {
		t.Fatalf("API form login = %d %s; want the JSON contract", rec.Code, rec.Body.String())
	}
}

func TestSignInPageLinksResolve(t *testing.T) {
	h, _, _ := browserFormServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login?next=%2Fdashboard%2Fapps", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `href="https://gregale.dev/signup"`) || !strings.Contains(body, `name="next" value="/dashboard/apps"`) {
		t.Fatalf("sign-in page links/next:\n%s", body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login/forgot", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/login/forgot"`) {
		t.Fatalf("GET /login/forgot = %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestBrowserForgotPasswordConfirmsWithoutEnumeration(t *testing.T) {
	h, email, _ := browserFormServer(t)
	for _, address := range []string{email, "nobody@example.com"} {
		rec := browserPost(h, "/login/forgot", url.Values{"email": {address}}, browserAccept)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "If that address belongs to a verified account") {
			t.Fatalf("%s: status %d body %s", address, rec.Code, rec.Body.String())
		}
	}
}
