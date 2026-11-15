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

func mintLoopbackCodeForTest(t *testing.T, srv http.Handler, port int, challenge string) api.CliAuthCodeResponse {
	t.Helper()
	raw, _ := json.Marshal(api.CliAuthCodeRequest{Device: true, LoopbackPort: port, CodeChallenge: challenge})
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

func exchangeDeviceCodeForTest(t *testing.T, srv http.Handler, deviceCode string, verifier ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := api.CliAuthExchangeRequest{DeviceCode: deviceCode}
	if len(verifier) > 0 {
		req.CodeVerifier = verifier[0]
	}
	raw, _ := json.Marshal(req)
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
	// The CLI keeps a verifier and sends only its SHA-256 (account audit
	// F10-3); a malformed challenge or one without a device code is refused.
	verifier, challenge, err := api.NewCliAuthVerifier()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []api.CliAuthCodeRequest{
		{Device: true, CodeChallenge: "not-hex"},
		{Device: true, CodeChallenge: challenge[:62]},
		{CodeChallenge: challenge},
	} {
		raw, _ := json.Marshal(bad)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mint %+v: %d %s, want 400", bad, rec.Code, rec.Body)
		}
	}
	minted := mintLoopbackCodeForTest(t, srv, 49152, challenge)
	if !minted.Loopback || len(minted.DeviceCode) != 64 {
		t.Fatalf("mint = %+v, want loopback with a device code", minted)
	}
	u, err := url.Parse(minted.URL)
	if err != nil || u.Query().Get("port") != "49152" || u.Query().Get("code") != minted.Code {
		t.Fatalf("URL = %q, want code and port=49152", minted.URL)
	}

	// The page names where the code came from (account audit F10): a
	// browser elsewhere sees both addresses.
	session := srv.issueBrowserSessionForTest(t, acct.Email)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cli-auth?"+u.RawQuery, nil)
	r.RemoteAddr = "198.51.100.7:4000"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	srv.ServeHTTP(rec, r)
	if body := rec.Body.String(); !strings.Contains(body, "from <code>192.0.2.1</code>; this browser is at <code>198.51.100.7</code>") {
		t.Fatalf("render from another address lacks the requester line: %s", body)
	}

	// The page: one button, no code to type.
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/cli-auth?"+u.RawQuery, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	srv.ServeHTTP(rec, r)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Authorize") {
		t.Fatalf("render: %d %s", rec.Code, body)
	}
	if !strings.Contains(body, "from <code>192.0.2.1</code> (this network)") {
		t.Fatalf("render lacks the same-network requester line: %s", body)
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
	// The device code alone, as browser history or a local server's log
	// would hold it, gets nothing, nor does it with someone else's verifier,
	// and neither attempt spends it.
	other, _, _ := api.NewCliAuthVerifier()
	if rec := exchangeDeviceCodeForTest(t, srv, fresh); rec.Code != http.StatusGone {
		t.Fatalf("exchange without the verifier: %d %s, want 410", rec.Code, rec.Body)
	}
	if rec := exchangeDeviceCodeForTest(t, srv, fresh, other); rec.Code != http.StatusGone {
		t.Fatalf("exchange with another verifier: %d %s, want 410", rec.Code, rec.Body)
	}
	if rec := exchangeDeviceCodeForTest(t, srv, fresh, "zz"); rec.Code != http.StatusBadRequest {
		t.Fatalf("exchange with a malformed verifier: %d %s, want 400", rec.Code, rec.Body)
	}
	// The CLI on the browser's machine, which holds the verifier, does.
	rec = exchangeDeviceCodeForTest(t, srv, fresh, verifier)
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
