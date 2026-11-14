package apidserver

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

func mintLoopbackCodeForTest(t *testing.T, srv http.Handler, port int) api.CliAuthCodeResponse {
	t.Helper()
	raw, _ := json.Marshal(api.CliAuthCodeRequest{Device: true, LoopbackPort: port})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint loopback code: %d %s", rec.Code, rec.Body)
	}
	var out api.CliAuthCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func exchangeDeviceCodeForTest(t *testing.T, srv http.Handler, deviceCode string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(api.CliAuthExchangeRequest{DeviceCode: deviceCode})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)
	return rec
}

// The one-click login (ADR-20261111170000): the page needs only the button,
// the approval hands a fresh device code to the browser's own loopback, and
// the device code the minting terminal holds stops working. A login link a
// phisher sends therefore authorizes nothing on the phisher's terminal.
func TestCliAuthLoopback_OneClickHandsDeviceCodeToBrowserMachine(t *testing.T) {
	srv, store := newCliAuthTestServer(t)
	acct, err := store.CreateAccount(t.Context(), "one-click@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	minted := mintLoopbackCodeForTest(t, srv, 49152)
	if !minted.Loopback || len(minted.DeviceCode) != 64 {
		t.Fatalf("mint = %+v, want loopback with a device code", minted)
	}
	u, err := url.Parse(minted.URL)
	if err != nil || u.Query().Get("port") != "49152" || u.Query().Get("code") != minted.Code {
		t.Fatalf("URL = %q, want code and port=49152", minted.URL)
	}

	// The page: one button, no code to type.
	session := srv.issueBrowserSessionForTest(t, acct.Email)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cli-auth?"+u.RawQuery, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	srv.ServeHTTP(rec, r)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Authorize") {
		t.Fatalf("render: %d %s", rec.Code, body)
	}
	if strings.Contains(body, `placeholder="ABCD-1234"`) {
		t.Fatalf("one-click page asks to type the code: %s", body)
	}
	normalized := strings.ToUpper(strings.ReplaceAll(minted.Code, "-", ""))
	if !strings.Contains(body, `name="code" value="`+normalized+`"`) || !strings.Contains(body, `name="port" value="49152"`) {
		t.Fatalf("one-click form lacks the hidden code and port: %s", body)
	}
	var csrfCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			csrfCookie = c.Value
		}
	}
	m := regexp.MustCompile(`name="csrf_token"\s+value="([^"]+)"`).FindStringSubmatch(body)
	if csrfCookie == "" || len(m) != 2 {
		t.Fatalf("render lacks CSRF cookie/token: %s", body)
	}

	// The click.
	form := url.Values{"code": {normalized}, "port": {"49152"}, middleware.FormFieldName: {m[1]}}
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	cb := regexp.MustCompile(`href="(http://127\.0\.0\.1:49152/callback\?[^"]+)"`).FindStringSubmatch(rec.Body.String())
	if len(cb) != 2 {
		t.Fatalf("approval page has no loopback link: %s", rec.Body)
	}
	callback, err := url.Parse(html.UnescapeString(cb[1]))
	if err != nil {
		t.Fatal(err)
	}
	fresh := callback.Query().Get("device_code")
	if callback.Query().Get("code") != normalized || len(fresh) != 64 || fresh == minted.DeviceCode {
		t.Fatalf("callback = %q, want this code and a new 64-hex device code", callback)
	}

	// The terminal that minted the code (a phisher's, say) gets nothing.
	if rec := exchangeDeviceCodeForTest(t, srv, minted.DeviceCode); rec.Code != http.StatusGone {
		t.Fatalf("exchange with the minting device code: %d %s, want 410", rec.Code, rec.Body)
	}
	// The browser's machine does.
	rec = exchangeDeviceCodeForTest(t, srv, fresh)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange with the loopback device code: %d %s", rec.Code, rec.Body)
	}
	var out api.CliAuthExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Plaintext == "" || out.Account.ID != acct.ID {
		t.Fatalf("exchange = %+v, %v", out, err)
	}
}

// A port is honoured only on a device-bound login code: never on MCP codes
// or code-only legacy codes, and never outside 1024-65535.
func TestCliAuthLoopbackPort(t *testing.T) {
	for _, tc := range []struct {
		port, code string
		want       int
	}{
		{"49152", "ABCD1234", 49152},
		{"49152", "ABCD-1234", 49152},
		{"", "ABCD1234", 0},
		{"80", "ABCD1234", 0},
		{"70000", "ABCD1234", 0},
		{"49152x", "ABCD1234", 0},
		{"49152", "ABCD1234ABCD1234", 0},
		{"49152", "MCPR" + strings.Repeat("00", 32), 0},
	} {
		if got := cliAuthLoopbackPort(tc.port, tc.code); got != tc.want {
			t.Errorf("cliAuthLoopbackPort(%q, %q) = %d, want %d", tc.port, tc.code, got, tc.want)
		}
	}
	srv, _ := newCliAuthTestServer(t)
	for _, req := range []api.CliAuthCodeRequest{{LoopbackPort: 49152}, {Device: true, LoopbackPort: 22}} {
		raw, _ := json.Marshal(req)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("mint %+v: %d, want 400", req, rec.Code)
		}
	}
}
