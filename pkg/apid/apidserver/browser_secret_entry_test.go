// adr: 20261026174727
package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const secretEntryValue = "sk_live_entered_by_the_owner"

type secretEntryFixture struct {
	server       *cliAuthTestServer
	account      state.Account
	app          state.App
	session, key string
}

func newSecretEntryFixture(t *testing.T) secretEntryFixture {
	t.Helper()
	t.Cleanup(withTestRecipient(t))
	srv, store := newCliAuthTestServer(t)
	acct, err := store.CreateAccount(t.Context(), "secret-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "billing-api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "bridge", []string{api.ScopeAppsRead, api.ScopeSecretsWrite}); err != nil {
		t.Fatal(err)
	}
	return secretEntryFixture{server: srv, account: acct, app: app, session: srv.issueBrowserSessionForTest(t, acct.Email), key: key}
}

func (f secretEntryFixture) apiCall(t *testing.T, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/apps/billing-api/"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.key)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func (f secretEntryFixture) request(t *testing.T) api.SecretEntryResponse {
	t.Helper()
	w := f.apiCall(t, "secret-requests", api.SecretEntryRequest{Key: "STRIPE_KEY", Reason: "Payments need <script>alert('agent')</script>"})
	if w.Code != 201 {
		t.Fatalf("request=%d %s", w.Code, w.Body.String())
	}
	var out api.SecretEntryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.EntryURL, "/cli-auth?secret=") || len(out.ExchangeCode) != 43 || strings.Contains(out.EntryURL, out.ExchangeCode) {
		t.Fatalf("response=%+v", out)
	}
	return out
}

func (f secretEntryFixture) status(t *testing.T, req api.SecretEntryResponse) (int, api.SecretEntryStatus) {
	t.Helper()
	w := f.apiCall(t, "secret-requests/status", api.SecretEntryStatusRequest{Ticket: req.Ticket, ExchangeCode: req.ExchangeCode})
	var out api.SecretEntryStatus
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if strings.Contains(w.Body.String(), secretEntryValue) {
		t.Fatal("status returned the secret value")
	}
	return w.Code, out
}

func (f secretEntryFixture) page(t *testing.T, req api.SecretEntryResponse) (string, string) {
	t.Helper()
	r := httptest.NewRequest("GET", req.EntryURL, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session})
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	body := w.Body.String()
	// The page names the key that asked (bug hunt 2026-10-28 H54).
	if w.Code != 200 || !strings.Contains(body, "STRIPE_KEY") || !strings.Contains(body, "billing-api") || !strings.Contains(body, "API key &#34;bridge&#34;") || strings.Contains(body, "<script>alert") || strings.Contains(body, req.ExchangeCode) || !strings.Contains(body, `type="password"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe or missing entry page=%d %s", w.Code, body)
	}
	csrf := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("entry page lacks a CSRF field")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == middleware.CookieNameAuthenticated {
			return cookie.Value, csrf[1]
		}
	}
	t.Fatal("entry page lacks a CSRF cookie")
	return "", ""
}

func (f secretEntryFixture) submit(req api.SecretEntryResponse, cookie, csrf, session, value string) *httptest.ResponseRecorder {
	form := url.Values{"secret": {req.Ticket}, "csrf_token": {csrf}, "value": {value}}
	r := httptest.NewRequest("POST", "/cli-auth", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: cookie})
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func TestMCPSecretEntryStoresTheValueOnlyThroughTheOwnersSession(t *testing.T) {
	f := newSecretEntryFixture(t)
	req := f.request(t)
	if code, status := f.status(t, req); code != 200 || status.Status != api.SecretEntryPending || status.Key != "STRIPE_KEY" || status.Scope != "default" {
		t.Fatalf("initial status=%d %+v", code, status)
	}
	cookie, csrf := f.page(t, req)
	if missing := f.submit(req, "", csrf, f.session, secretEntryValue); missing.Code != 403 {
		t.Fatal("missing browser CSRF accepted")
	}
	other := f.server.issueBrowserSessionForTest(t, "someone-else@example.com")
	if cross := f.submit(req, cookie, csrf, other, secretEntryValue); cross.Code != 403 {
		t.Fatal("another account set the secret")
	}
	if swapped := f.submit(f.request(t), cookie, csrf, f.session, secretEntryValue); swapped.Code != 403 {
		t.Fatal("CSRF authorized a different request")
	}
	if empty := f.submit(req, cookie, csrf, f.session, ""); empty.Code != 400 {
		t.Fatalf("empty value=%d", empty.Code)
	}
	if code, status := f.status(t, req); code != 200 || status.Status != api.SecretEntryPending {
		t.Fatal("a refused value consumed the request")
	}
	saved := f.submit(req, cookie, csrf, f.session, secretEntryValue)
	if saved.Code != 200 || !strings.Contains(saved.Body.String(), "Saved") || strings.Contains(saved.Body.String(), secretEntryValue) {
		t.Fatalf("save=%d %s", saved.Code, saved.Body.String())
	}
	if code, status := f.status(t, req); code != 200 || status.Status != api.SecretEntrySet {
		t.Fatalf("status after save=%d %+v", code, status)
	}
	stored, err := f.server.store.GetAppSecret(t.Context(), f.account.ID, f.app.ID, "STRIPE_KEY")
	if err != nil || stored == nil || len(stored.Ciphertext) == 0 || bytes.Contains(stored.Ciphertext, []byte(secretEntryValue)) {
		t.Fatalf("stored secret=%+v err=%v", stored, err)
	}
	if replay := f.submit(req, cookie, csrf, f.session, "sk_live_replayed"); replay.Code != 409 {
		t.Fatal("a used request accepted another value")
	}
}

func TestMCPSecretEntryRejectsBadRequestsAndStaleTickets(t *testing.T) {
	f := newSecretEntryFixture(t)
	for _, input := range []api.SecretEntryRequest{{Key: "", Reason: "x"}, {Key: "OK_KEY", Reason: ""}, {Key: "OK_KEY", Reason: strings.Repeat("r", 281)}, {Key: "OK_KEY", Reason: "x", Scope: "Bad Scope"}} {
		if w := f.apiCall(t, "secret-requests", input); w.Code != 400 {
			t.Errorf("input %+v accepted: %d", input, w.Code)
		}
	}
	req := f.request(t)
	if w := f.apiCall(t, "secret-requests/status", api.SecretEntryStatusRequest{Ticket: req.Ticket, ExchangeCode: strings.Repeat("x", 43)}); w.Code != 410 {
		t.Fatal("the entry link alone could read the status")
	}
	if w := f.apiCall(t, "secret-requests/status", api.SecretEntryStatusRequest{Ticket: "broken" + req.Ticket, ExchangeCode: req.ExchangeCode}); w.Code != 410 {
		t.Fatal("a tampered ticket was accepted")
	}
	unsigned := httptest.NewRecorder()
	f.server.ServeHTTP(unsigned, httptest.NewRequest("GET", req.EntryURL, nil))
	if unsigned.Code != 302 || !strings.HasPrefix(unsigned.Header().Get("Location"), "/login?next=") {
		t.Fatal("the entry page did not require sign-in")
	}
	ticket, err := f.server.apid.openSecretEntryTicket(req.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	ticket.ExpiresAt = time.Now().Add(-time.Minute)
	data, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.server.apid.sessions.SealForCSRF(data)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.apiCall(t, "secret-requests/status", api.SecretEntryStatusRequest{Ticket: sealed, ExchangeCode: req.ExchangeCode}); w.Code != 410 {
		t.Fatal("an expired request reported a status")
	}
	if _, valid := normalizeCliAuthCode(req.ExchangeCode); valid {
		t.Fatal("the exchange code entered the CLI login domain")
	}
	cookie, csrf := f.page(t, req)
	if err := f.server.store.DeleteApp(t.Context(), f.app.ID); err != nil {
		t.Fatal(err)
	}
	if gone := f.submit(req, cookie, csrf, f.session, secretEntryValue); gone.Code != 409 {
		t.Fatalf("a deleted app's request stored a value: %d", gone.Code)
	}
}
