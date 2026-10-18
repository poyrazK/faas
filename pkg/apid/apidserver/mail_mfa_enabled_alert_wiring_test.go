//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/pquerna/otp/totp"
)

func enabledMFAPost(t *testing.T, h http.Handler, manager *session.Manager, id, cookie, path, code string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	form := map[string]string{"totp": code}
	token, err := middleware.IssueForAuthenticated(manager, "mfa_confirm", id)
	if err != nil {
		t.Fatal(err)
	}
	if csrf {
		form["csrf_token"] = token
	}
	body, err := json.Marshal(form)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://dashboard.example.test"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "mfa-enabled-test")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
	if csrf {
		r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMFAEnabledAlertPublicConfirmationAndFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"off", "atomic", "intent_failure", "mark_failure", "late_enable", "bad_code", "csrf_failure", "missing_setup", "missing_host", "wrong_host", "cookie_failure"} {
		t.Run(mode, func(t *testing.T) {
			// Wires isolated host readers and the recovery-code HMAC through the
			// existing fixture, with cleanup restoring every accessor after this test.
			_ = apidserver.SetupWithMFA(t, api.PlanPro, false, false)
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server, pool, store := *env.S(), *env.Pool(), *env.Store()
			id := env.Acct().ID
			if _, err := store.SetMFARequired(t.Context(), id, true); err != nil {
				t.Fatal(err)
			}
			manager := *server.Sessions()
			sid := uuid.NewString()
			if _, err := store.CreateSessionWithBinding(t.Context(), sid, id, "192.0.2.1", "mfa-enabled-test", ""); err != nil {
				t.Fatal(err)
			}
			cookie, err := manager.IssueWithSession(sid, id, true)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "mfa-enabled-alert.age")
			if err := os.WriteFile(path, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-mfa-enabled-alert-test-credential-32-bytes", mailoutbox.VerificationIdentityEnv: path}
			if mode != "off" && mode != "late_enable" {
				flags[internalmail.MFAEnabledAlertOutboxEnableEnv] = "1"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			var enrollment api.MFAEnrollResponse
			if mode != "missing_setup" {
				enrolled := enabledMFAPost(t, handler, manager, id, cookie, "/v1/account/mfa/enroll", "", false)
				if enrolled.Code != http.StatusOK || json.Unmarshal(enrolled.Body.Bytes(), &enrollment) != nil || len(enrollment.RecoveryCodes) != authcode.RecoveryCodeCount || enrollment.Secret == "" {
					t.Fatal("pending enrollment failed", enrolled.Code, enrolled.Body.String())
				}
			}
			var beforeEvents int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&beforeEvents); err != nil || beforeEvents != 0 {
				t.Fatal("preparation emitted an activation alert", err)
			}
			if mode == "late_enable" {
				flags[internalmail.MFAEnabledAlertOutboxEnableEnv] = "1"
			}
			if mode == "intent_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_enabled_alert_failure CHECK (template<>'mfa_enabled')"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "mark_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE accounts ADD CONSTRAINT injected_enabled_mark_failure CHECK (mfa_enrolled_at IS NULL)"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cookie_failure" {
				if _, err := pool.Exec(t.Context(), `CREATE FUNCTION revoke_enabled_alert_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE sessions SET revoked_at=clock_timestamp() WHERE account_id=NEW.account_id; RETURN NEW; END $$;
CREATE TRIGGER revoke_enabled_alert_session AFTER INSERT ON platform_mail_producer_outbox FOR EACH ROW WHEN (NEW.template='mfa_enabled') EXECUTE FUNCTION revoke_enabled_alert_session()`); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "missing_host":
				apidserver.SetMFAIdentities(func() []*age.X25519Identity { return nil })
				apidserver.SetMFAIdentity(func() *age.X25519Identity { return nil })
			case "wrong_host":
				wrong, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				apidserver.SetMFAIdentities(func() []*age.X25519Identity { return []*age.X25519Identity{wrong} })
			}
			code := "not-a-code"
			if enrollment.Secret != "" && mode != "bad_code" {
				code, err = totp.GenerateCode(enrollment.Secret, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
			}
			response := enabledMFAPost(t, handler, manager, id, cookie, "/v1/account/mfa/confirm", code, mode != "csrf_failure")
			success := mode == "off" || mode == "atomic" || mode == "cookie_failure"
			want := http.StatusServiceUnavailable
			switch mode {
			case "off", "atomic", "cookie_failure":
				want = http.StatusOK
			case "bad_code":
				want = http.StatusUnauthorized
			case "csrf_failure":
				want = http.StatusForbidden
			case "missing_setup":
				want = http.StatusNotFound
			}
			if response.Code != want {
				t.Fatal("unexpected confirmation response", response.Code, response.Body.String())
			}
			var enrolled, required bool
			var codes, events int
			if err := pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL,mfa_required,coalesce(cardinality(mfa_recovery_codes_hash),0),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled') FROM accounts WHERE id=$1", id).Scan(&enrolled, &required, &codes, &events); err != nil {
				t.Fatal(err)
			}
			wantEvents := 0
			if mode == "atomic" || mode == "cookie_failure" {
				wantEvents = 1
			}
			expectedCodes := authcode.RecoveryCodeCount
			if mode == "missing_setup" {
				expectedCodes = 0
			}
			if enrolled != success || required == success || codes != expectedCodes || events != wantEvents || recorder.calls != 0 {
				t.Fatal("confirmation, policy, recovery codes or alert changed incorrectly", enrolled, required, codes, events, recorder.calls)
			}
			cookieCount := 0
			fresh := ""
			for _, c := range response.Result().Cookies() {
				if c.Name == "faas_sid" {
					cookieCount++
					fresh = c.Value
					envelope, err := manager.Verify(c.Value)
					if err != nil || envelope.MfaPending || envelope.Sid != sid || envelope.AccountID != id || !envelope.StepUpAt.IsZero() {
						t.Fatal("confirmation changed session proof rules", err)
					}
				}
			}
			wantCookies := 0
			if success && mode != "cookie_failure" {
				wantCookies = 1
			}
			if cookieCount != wantCookies {
				t.Fatal("failed confirmation granted a session, or success lost its cookie", cookieCount)
			}
			var sessions int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE account_id=$1", id).Scan(&sessions); err != nil || sessions != 1 {
				t.Fatal("confirmation created another session", err)
			}
			if wantEvents == 0 {
				return
			}
			var cipher []byte
			if err := pool.QueryRow(t.Context(), "SELECT sealed_security_alert FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&cipher); err != nil {
				t.Fatal(err)
			}
			_, plain, err := secretbox.OpenBytes(identity, cipher)
			defer clear(plain)
			var payload map[string]any
			if err != nil || json.Unmarshal(plain, &payload) != nil || len(payload) != 1 || payload["enabled_at"] == nil || bytes.Contains(plain, []byte(enrollment.Secret)) {
				t.Fatal("activation alert contains unexpected data", err)
			}
			if mode == "atomic" {
				repeated := enabledMFAPost(t, handler, manager, id, fresh, "/v1/account/mfa/confirm", code, true)
				if repeated.Code != http.StatusOK {
					t.Fatal("valid repeated confirmation failed", repeated.Code, repeated.Body.String())
				}
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&events); err != nil || events != 1 {
					t.Fatal("repeat confirmation duplicated alert", err)
				}
			}
		})
	}
}
