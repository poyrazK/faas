package apidserver_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/pquerna/otp/totp"
)

func accountSecurityJSON(t *testing.T, h http.Handler, cookie *http.Cookie, method, path string, body any, extra ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for _, c := range extra {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func accountSecurityCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if sessioncookie.IsName(c.Name) {
			return c
		}
	}
	return nil
}

func accountSecurityCSRF(t *testing.T, h http.Handler, cookie *http.Cookie, action string) (string, []*http.Cookie) {
	t.Helper()
	rec := accountSecurityJSON(t, h, cookie, "GET", "/v1/auth/csrf?action="+action, nil)
	var token api.CSRFTokenResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &token) != nil || token.CSRFToken == "" {
		t.Fatalf("CSRF for %s: %d %s", action, rec.Code, rec.Body)
	}
	return token.CSRFToken, rec.Result().Cookies()
}

func TestAccountMFASetupRequiresExistingPassword(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, true, false)
	st, acct, h, cookie := *e.Store(), *e.Acct(), *e.H(), *e.Cookie()
	const password = "owner-password-long-123!"
	old, err := auth.Encode(password)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetAccountPassword(t.Context(), acct.ID, old); err != nil {
		t.Fatal(err)
	}
	page := accountSecurityJSON(t, h, cookie, "GET", "/dashboard/account/mfa", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `name="current_password"`) {
		t.Fatalf("password enrollment form: %d", page.Code)
	}
	enroll := accountSecurityJSON(t, h, cookie, "POST", "/v1/account/mfa/enroll", api.MFAEnrollRequest{})
	var material api.MFAEnrollResponse
	if enroll.Code != 200 || json.Unmarshal(enroll.Body.Bytes(), &material) != nil {
		t.Fatalf("enroll: %d %s", enroll.Code, enroll.Body)
	}
	code, err := totp.GenerateCode(material.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	verify := accountSecurityJSON(t, h, cookie, "POST", "/v1/account/mfa/verify", api.MFAVerifyRequest{Totp: code})
	if verify.Code != 409 || accountSecurityCookie(verify) != nil {
		t.Fatalf("unconfirmed verification: %d %s", verify.Code, verify.Body)
	}
	recoveryToken, recoveryCookies := accountSecurityCSRF(t, h, cookie, "mfa_recover")
	recover := accountSecurityJSON(t, h, cookie, "POST", "/v1/account/mfa/recover", api.MFARecoverRequest{Code: material.RecoveryCodes[0], CSRFToken: recoveryToken}, recoveryCookies...)
	if recover.Code != 409 || accountSecurityCookie(recover) != nil {
		t.Fatalf("unconfirmed recovery: %d %s", recover.Code, recover.Body)
	}
	// The HTML challenge must enforce the same enrollment boundary as the API.
	challenge := accountSecurityJSON(t, h, cookie, "GET", "/dashboard/mfa", nil)
	challengeToken := ""
	for _, c := range challenge.Result().Cookies() {
		if c.Name == "faas_csrf_mfa" {
			challengeToken = c.Value
		}
	}
	if challengeToken == "" {
		t.Fatal("missing dashboard challenge CSRF")
	}
	form := url.Values{"code": {code}, "csrf_token": {challengeToken}}
	req := httptest.NewRequest("POST", "/dashboard/mfa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	for _, c := range challenge.Result().Cookies() {
		req.AddCookie(c)
	}
	challengeResult := httptest.NewRecorder()
	h.ServeHTTP(challengeResult, req)
	if challengeResult.Code != 409 {
		t.Fatalf("unconfirmed HTML challenge: %d", challengeResult.Code)
	}
	for _, proof := range []string{"", "incorrect-password"} {
		token, cookies := accountSecurityCSRF(t, h, cookie, "mfa_confirm")
		rec := accountSecurityJSON(t, h, cookie, "POST", "/v1/account/mfa/confirm", api.MFAConfirmRequest{Totp: code, CurrentPassword: proof, CSRFToken: token}, cookies...)
		if rec.Code != 401 || accountSecurityCookie(rec) != nil {
			t.Fatalf("unauthorized confirmation: %d %s", rec.Code, rec.Body)
		}
	}
	before, err := st.AccountByID(t.Context(), acct.ID)
	if err != nil || before.MFAEnrolled() || !before.MFARequired || len(before.MFARecoveryCodesHash) != len(material.RecoveryCodes) {
		t.Fatalf("rejected setup changed account: %v", err)
	}
	token, cookies := accountSecurityCSRF(t, h, cookie, "mfa_confirm")
	confirmed := accountSecurityJSON(t, h, cookie, "POST", "/v1/account/mfa/confirm", api.MFAConfirmRequest{Totp: code, CurrentPassword: password, CSRFToken: token}, cookies...)
	if confirmed.Code != 200 || accountSecurityCookie(confirmed) == nil {
		t.Fatalf("authorized confirmation: %d %s", confirmed.Code, confirmed.Body)
	}
	after, err := st.AccountByID(t.Context(), acct.ID)
	if err != nil || !after.MFAEnrolled() {
		t.Fatalf("enrollment missing: %v", err)
	}
	stored, err := st.AccountPasswordByAccountID(t.Context(), acct.ID)
	if err != nil || stored != old {
		t.Fatal("enrollment changed the account password")
	}
}

func TestAccountOwnershipClaimUsesFreshMFAState(t *testing.T) {
	for _, channel := range []string{"magic link", "OAuth"} {
		t.Run(channel, func(t *testing.T) {
			srv, _, st := apidserver.V1AuthTestHarness(t)
			signup := accountSecurityJSON(t, srv.Handler(), nil, "POST", "/v1/auth/signup", map[string]string{"email": "claim-security@example.com", "password": "signup-password-long-123!"})
			if signup.Code != 200 {
				t.Fatalf("signup: %d", signup.Code)
			}
			acct, err := st.AccountByEmail(t.Context(), "claim-security@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if err = st.SetMFASecret(t.Context(), acct.ID, []byte("pre-verification-secret"), nil); err != nil {
				t.Fatal(err)
			}
			if err = st.MarkMFAEnrolled(t.Context(), acct.ID); err != nil {
				t.Fatal(err)
			}
			if channel == "OAuth" {
				acct, err = st.AccountByID(t.Context(), acct.ID)
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := srv.VerifyOAuthAccountEmail(t.Context(), acct)
				if err != nil || !claimed.EmailVerified() || claimed.MFAEnrolled() || len(claimed.MFASecretEncrypted) != 0 {
					t.Fatalf("stale OAuth claim: %+v %v", claimed, err)
				}
				return
			}
			raw := bytes.Repeat([]byte{71}, 32)
			if err = st.IssueLoginToken(t.Context(), api.HashToken(raw), acct.ID, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			open := httptest.NewRequest(http.MethodGet, "/auth/verify?token="+base64.RawURLEncoding.EncodeToString(raw), nil)
			open.Header.Set("Sec-Fetch-Site", "none")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, open)
			c := accountSecurityCookie(rec)
			if rec.Code != 302 || c == nil {
				t.Fatalf("claim: %d", rec.Code)
			}
			env, err := (*srv.Sessions()).Verify(c.Value)
			if err != nil || session.IsMFAPending(env) {
				t.Fatalf("stale pending session: %v", err)
			}
			if dashboard := accountSecurityJSON(t, srv.Handler(), c, "GET", "/dashboard/", nil); dashboard.Code != 200 {
				t.Fatalf("owner dashboard: %d", dashboard.Code)
			}
		})
	}
}

func TestAccountSessionRejectsRequestFingerprintReplay(t *testing.T) {
	for _, path := range []string{"/v1/account", "/dashboard/"} {
		for _, tc := range []struct {
			name, ip, ua, forwarded string
			allowed                 bool
		}{
			{"same client", "198.51.100.10:1234", "Mozilla/5.0 Chrome/132.0", "", true},
			// The console reaches apid through Vercel, whose egress
			// address changes between requests: an IP change alone
			// must never revoke (rc.321 signed every console user out).
			{"changed IP, same browser", "203.0.113.81:9876", "Mozilla/5.0 Chrome/131.0", "", true},
			{"changed browser", "198.51.100.10:1234", "curl/8.0", "", false},
			{"changed IP and browser", "203.0.113.81:9876", "curl/8.0", "", false},
			{"trusted proxy", "127.0.0.1:1234", "Mozilla/5.0 Chrome/131.0", "198.51.100.10", true},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				srv, _, st := apidserver.V1AuthTestHarness(t)
				acct, err := st.CreateAccount(t.Context(), "fingerprint-security@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				phc, err := auth.Encode("fingerprint-password-123!")
				if err != nil {
					t.Fatal(err)
				}
				if err = st.SetAccountPassword(t.Context(), acct.ID, phc); err != nil {
					t.Fatal(err)
				}
				login := httptest.NewRequest("POST", "/login", strings.NewReader(`{"email":"`+acct.Email+`","password":"fingerprint-password-123!"}`))
				login.Header.Set("Content-Type", "application/json")
				login.RemoteAddr = "198.51.100.10:1234"
				login.Header.Set("User-Agent", "Mozilla/5.0 Chrome/131.0")
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, login)
				cookie := accountSecurityCookie(rec)
				if rec.Code != 200 || cookie == nil {
					t.Fatalf("login: %d", rec.Code)
				}
				env, err := (*srv.Sessions()).Verify(cookie.Value)
				if err != nil || env.BindingHash == "" {
					t.Fatal("binding not armed")
				}
				replay := httptest.NewRequest("GET", path, nil)
				replay.AddCookie(cookie)
				replay.RemoteAddr = tc.ip
				replay.Header.Set("User-Agent", tc.ua)
				replay.Header.Set("X-Forwarded-For", tc.forwarded)
				result := httptest.NewRecorder()
				srv.Handler().ServeHTTP(result, replay)
				want := 401
				if path == "/dashboard/" {
					want = 302
				}
				if tc.allowed {
					want = 200
				}
				if result.Code != want {
					t.Fatalf("fingerprint check: %d %s", result.Code, result.Body)
				}
				row, err := st.GetSession(t.Context(), env.Sid)
				if err != nil || (row.RevokedAt == nil) != tc.allowed {
					t.Fatalf("wrong revocation state: %v", err)
				}
			})
		}
	}
}
