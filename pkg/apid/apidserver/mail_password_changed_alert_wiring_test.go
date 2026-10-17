//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
)

func changedPasswordPost(t *testing.T, h http.Handler, manager *session.Manager, id, cookie, current string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"password": {"Replacement!Password-For-Gregale-2026"}, "current_password": {current}}
	token, err := middleware.IssueForAuthenticated(manager, "set_password", id)
	if err != nil {
		t.Fatal(err)
	}
	if csrf {
		form.Set("csrf_token", token)
	}
	r := httptest.NewRequest(http.MethodPost, "https://dashboard.example.test/dashboard/account/set-password", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", "password-changed-test")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
	if csrf {
		r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPasswordChangedAlertPublicPathsAndAtomicFailures(t *testing.T) {
	for _, method := range []string{"dashboard", "durable_reset", "legacy_reset"} {
		modes := []string{"off", "atomic", "save_failure", "write_failure", "late_enable"}
		if method == "dashboard" {
			modes = append(modes, "csrf_failure", "bad_proof", "mfa_pending", "mfa_enrolled", "step_up", "first_password")
		}
		for _, mode := range modes {
			t.Run(method+"/"+mode, func(t *testing.T) {
				env := apidserver.SetupPGHandler(t, api.PlanPro)
				server, pool, store := *env.S(), *env.Pool(), *env.Store()
				id, email := env.Acct().ID, env.Acct().Email
				if err := store.MarkAccountEmailVerified(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				const oldPassword = "Original!Password-For-Gregale-2026"
				phc, err := auth.Encode(oldPassword)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetAccountPassword(t.Context(), id, phc); err != nil {
					t.Fatal(err)
				}
				if mode == "mfa_pending" {
					if _, err := store.SetMFARequired(t.Context(), id, true); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "mfa_enrolled" || mode == "step_up" {
					if err := store.SetMFASecret(t.Context(), id, []byte("factor"), nil); err != nil {
						t.Fatal(err)
					}
					if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "first_password" {
					if err := store.DeleteAccountPassword(t.Context(), id); err != nil {
						t.Fatal(err)
					}
				}
				manager := *server.Sessions()
				sid := uuid.NewString()
				if _, err := store.CreateSessionWithBinding(t.Context(), sid, id, "192.0.2.1", "password-changed-test", ""); err != nil {
					t.Fatal(err)
				}
				otherSID := uuid.NewString()
				if _, err := store.CreateSessionWithBinding(t.Context(), otherSID, id, "192.0.2.2", "other", ""); err != nil {
					t.Fatal(err)
				}
				cookie, err := manager.IssueWithSession(sid, id, false)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "step_up" {
					cookie, err = manager.IssueWithSessionAndBindingHashAndStepUp(sid, id, "", time.Now(), false)
					if err != nil {
						t.Fatal(err)
					}
				}
				identity, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "password-alert.age")
				if err := os.WriteFile(path, []byte(identity.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-password-alert-test-credential-32-bytes", mailoutbox.VerificationIdentityEnv: path}
				if mode != "off" && mode != "late_enable" {
					flags[internalmail.PasswordChangedAlertOutboxEnableEnv] = "1"
				}
				if method == "durable_reset" {
					flags[internalmail.PasswordResetOutboxEnableEnv] = "1"
					flags[mailoutbox.PasswordResetOriginEnv] = "https://dashboard.example.test"
				}
				if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
				recorder := &outboxInlineRecorder{}
				*server.Mailer() = recorder
				handler := server.Handler()
				raw := bytes.Repeat([]byte{77}, 32)
				defer clear(raw)
				if method == "durable_reset" {
					issued := resetFormPost(handler, "/login/forgot", url.Values{"email": {email}})
					if issued.Code != http.StatusOK {
						t.Fatal("reset issue failed", issued.Code)
					}
					var cipher []byte
					if err := pool.QueryRow(t.Context(), "SELECT sealed_verification FROM platform_mail_producer_outbox WHERE template='password_reset'").Scan(&cipher); err != nil {
						t.Fatal(err)
					}
					_, token, err := secretbox.OpenBytes(identity, cipher)
					if err != nil {
						t.Fatal(err)
					}
					copy(raw, token)
					clear(token)
					// Alert redemption remains independent of reset issuance and its origin.
					flags[internalmail.PasswordResetOutboxEnableEnv] = "0"
					flags[mailoutbox.PasswordResetOriginEnv] = "http://invalid-while-off"
				} else if method == "legacy_reset" {
					if err := store.IssueLoginToken(t.Context(), api.HashToken(raw), id, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "late_enable" {
					flags[internalmail.PasswordChangedAlertOutboxEnableEnv] = "1"
				}
				if mode == "save_failure" {
					if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_password_alert_failure CHECK (template<>'password_changed')"); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "write_failure" {
					if _, err := pool.Exec(t.Context(), "ALTER TABLE account_passwords ADD CONSTRAINT injected_password_update_failure CHECK (false) NOT VALID"); err != nil {
						t.Fatal(err)
					}
				}
				var response *httptest.ResponseRecorder
				if method == "dashboard" {
					current := oldPassword
					if mode == "bad_proof" {
						current = "wrong-password"
					}
					response = changedPasswordPost(t, handler, manager, id, cookie, current, mode != "csrf_failure")
				} else {
					response = resetFormPost(handler, "/auth/reset", url.Values{"token": {base64.RawURLEncoding.EncodeToString(raw)}, "password": {"Replacement!Password-For-Gregale-2026"}})
				}
				success := mode == "off" || mode == "atomic" || mode == "step_up" || mode == "first_password"
				wantStatus := http.StatusInternalServerError
				if success {
					wantStatus = http.StatusFound
				}
				switch mode {
				case "csrf_failure":
					wantStatus = http.StatusBadRequest
				case "bad_proof":
					wantStatus = http.StatusUnauthorized
				case "mfa_pending", "mfa_enrolled":
					wantStatus = http.StatusForbidden
				}
				if response.Code != wantStatus {
					t.Fatal("unexpected response", response.Code, response.Body.String())
				}
				got, err := store.AccountPasswordByAccountID(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				expected := oldPassword
				if success {
					expected = "Replacement!Password-For-Gregale-2026"
				}
				if matched, err := auth.Verify(got, expected); err != nil || !matched {
					t.Fatal("password and response disagree", err)
				}
				var events int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_changed'").Scan(&events); err != nil {
					t.Fatal(err)
				}
				wantEvents := 0
				if success && mode != "off" {
					wantEvents = 1
				}
				if events != wantEvents || recorder.calls != 0 {
					t.Fatal("alert missing, duplicated or sent inline", events, recorder.calls)
				}
				var revoked, currentRevoked bool
				if err := pool.QueryRow(t.Context(), "SELECT (SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$1),(SELECT revoked_at IS NOT NULL FROM sessions WHERE id=$2)", otherSID, sid).Scan(&revoked, &currentRevoked); err != nil || revoked != (success && mode != "first_password") || currentRevoked != (success && method != "dashboard") {
					t.Fatal("session revocation behavior changed", err)
				}
				if method != "dashboard" {
					query := "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1"
					if method == "legacy_reset" {
						query = "SELECT consumed_at IS NOT NULL FROM login_tokens WHERE token_hash=$1"
					}
					var consumed bool
					if err := pool.QueryRow(t.Context(), query, api.HashToken(raw)).Scan(&consumed); err != nil || consumed != success {
						t.Fatal("reset token not atomic with password and alert", err)
					}
					if success && len(response.Result().Cookies()) == 0 {
						t.Fatal("reset no longer issues a session")
					}
					if !success && len(response.Result().Cookies()) != 0 {
						t.Fatal("failed reset granted a session")
					}
				}
				if mode == "atomic" {
					var cipher []byte
					if err := pool.QueryRow(t.Context(), "SELECT sealed_security_alert FROM platform_mail_producer_outbox WHERE template='password_changed'").Scan(&cipher); err != nil {
						t.Fatal(err)
					}
					_, plain, err := secretbox.OpenBytes(identity, cipher)
					defer clear(plain)
					var payload map[string]any
					if err != nil || json.Unmarshal(plain, &payload) != nil || len(payload) != 2 || payload["changed_at"] == nil {
						t.Fatal("alert stores unexpected data", err)
					}
					want := "password_reset"
					if method == "dashboard" {
						want = "dashboard"
					}
					if payload["method"] != want {
						t.Fatal("wrong event source")
					}
				}
			})
		}
	}
}
