package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

const trustTestUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// A browser that passes the sign-in authenticator check and asks to be
// remembered starts later sessions without the check; another browser
// family, a forgotten browser or a re-enrollment asks again.
func TestMFARememberBrowser(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.GenerateEnrolledAccount(t)
	pending := e.MfaIssueWithPending(t, true)
	h := *e.H()
	srv := *e.Srv()
	acctID := e.Acct().ID

	page := trustRequest(t, h, "GET", "/dashboard/mfa?next=/dashboard/account", nil, pending)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Remember this browser") {
		t.Fatalf("challenge = %d, offers remember: %v", page.Code, strings.Contains(page.Body.String(), "Remember this browser"))
	}
	csrf := responseCookie(page, "faas_csrf_mfa")
	if csrf == nil {
		t.Fatal("missing MFA CSRF cookie")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"code": {code}, "csrf_token": {csrf.Value}, "next": {"/dashboard/account"}, "remember": {"1"}}
	verified := trustRequest(t, h, "POST", "/dashboard/mfa", form, pending, csrf)
	if verified.Code != 303 {
		t.Fatalf("verify = %d %s", verified.Code, verified.Body)
	}
	trust := responseCookie(verified, apidserver.MFATrustCookie)
	if trust == nil || trust.MaxAge != int(api.MFATrustedBrowserTTL.Seconds()) || !trust.HttpOnly {
		t.Fatalf("trust cookie = %+v", trust)
	}

	signIn := func(ua string, cookies ...*http.Cookie) bool {
		t.Helper()
		req := httptest.NewRequest("POST", "/login", nil)
		req.Header.Set("User-Agent", ua)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		value, err := srv.IssueDashboardSession(req, acctID, true)
		if err != nil {
			t.Fatal(err)
		}
		env, err := (*srv.Sessions()).Verify(value)
		if err != nil {
			t.Fatal(err)
		}
		return env.MfaPending
	}
	if !signIn(trustTestUA) {
		t.Fatal("a browser without the cookie skipped the code")
	}
	if signIn(trustTestUA, trust) {
		t.Fatal("remembered browser still asked for the code")
	}
	if !signIn("curl/8.0", trust) {
		t.Fatal("a copied cookie in another browser family skipped the code")
	}
	if !signIn(trustTestUA, &http.Cookie{Name: apidserver.MFATrustCookie, Value: strings.Repeat("A", 43)}) {
		t.Fatal("an unknown token skipped the code")
	}

	store := *e.Store()
	browsers, err := store.ListMFATrustedBrowsers(t.Context(), acctID)
	if err != nil || len(browsers) != 1 || browsers[0].LastUsedAt == nil {
		t.Fatalf("browsers = %+v err=%v", browsers, err)
	}
	if n, err := store.RevokeAllMFATrustedBrowsers(t.Context(), acctID); err != nil || n != 1 {
		t.Fatalf("forget all = %d %v", n, err)
	}
	if !signIn(trustTestUA, trust) {
		t.Fatal("a forgotten browser skipped the code")
	}
}

// Sign-in trust is checked against the current enrollment, the cap and
// the account page.
func TestMFARememberBrowserStore(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "trust@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	hash := func(b byte) []byte { return api.HashToken([]byte{b}) }
	expires := time.Now().Add(time.Hour)
	if err := store.IssueMFATrustedBrowser(ctx, hash(0), acct.ID, "Chrome", "", expires, 2); err == nil {
		t.Fatal("remembered a browser for an account without a factor")
	}
	if err := store.MarkMFAEnrolled(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	for i := byte(1); i <= 3; i++ {
		if err := store.IssueMFATrustedBrowser(ctx, hash(i), acct.ID, "Chrome", "192.0.2.1", expires, 2); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	list, _ := store.ListMFATrustedBrowsers(ctx, acct.ID)
	if len(list) != 2 {
		t.Fatalf("cap kept %d browsers", len(list))
	}
	if ok, _ := store.UseMFATrustedBrowser(ctx, hash(1), acct.ID, "Chrome"); ok {
		t.Fatal("the browser past the cap is still trusted")
	}
	if ok, _ := store.UseMFATrustedBrowser(ctx, hash(3), acct.ID, "Chrome"); !ok {
		t.Fatal("newest browser not trusted")
	}
	if err := store.RevokeMFATrustedBrowser(ctx, acct.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.UseMFATrustedBrowser(ctx, hash(3), acct.ID, "Chrome"); ok {
		t.Fatal("forgotten browser still trusted")
	}
	// Turning MFA off and on again starts a new enrollment.
	time.Sleep(time.Millisecond)
	if err := store.ClearMFA(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMFAEnrolled(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.UseMFATrustedBrowser(ctx, hash(2), acct.ID, "Chrome"); ok {
		t.Fatal("a browser remembered before re-enrollment is trusted")
	}
}

func trustRequest(t *testing.T, h http.Handler, method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("User-Agent", trustTestUA)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
