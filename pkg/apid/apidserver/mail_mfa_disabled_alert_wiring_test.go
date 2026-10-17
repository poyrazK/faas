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

func disabledAlertPost(t *testing.T, h http.Handler, manager *session.Manager, id, cookie string, form map[string]string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	token, err := middleware.IssueForAuthenticated(manager, "mfa_disable", id)
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
	r := httptest.NewRequest(http.MethodPost, "https://dashboard.example.test/v1/account/mfa/disable", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "mfa-disable-test")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
	if csrf {
		r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMFADisabledAlertPublicPathsAndFailureBoundaries(t *testing.T) {
	for _, method := range []string{"password", "recovery_code", "durable_email", "legacy_email"} {
		for _, mode := range []string{"off", "atomic", "save_failure", "csrf_failure", "late_enable", "pending_password", "bad_proof"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				if mode == "pending_password" && method != "password" || mode == "bad_proof" && strings.HasSuffix(method, "email") {
					return
				}
				source := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
				codes, _, _ := source.GenerateEnrolledAccount(t)
				mfaAccount, err := (*source.Store()).AccountByID(t.Context(), source.Acct().ID)
				if err != nil {
					t.Fatal(err)
				}
				env := apidserver.SetupPGHandler(t, api.PlanPro)
				server, pool, store := *env.S(), *env.Pool(), *env.Store()
				id := env.Acct().ID
				if err := store.SetMFASecret(t.Context(), id, mfaAccount.MFASecretEncrypted, mfaAccount.MFARecoveryCodesHash[:1]); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkAccountEmailVerified(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				const password = "Correct!Horse-Battery-Staple-2026"
				if method == "password" {
					// The source fixture has a valid password-free account; use the ordinary
					// password encoder through the public test helper's existing library.
					phc, err := auth.Encode(password)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.SetAccountPassword(t.Context(), id, phc); err != nil {
						t.Fatal(err)
					}
				}
				manager := *server.Sessions()
				sid := uuid.NewString()
				if _, err := store.CreateSessionWithBinding(t.Context(), sid, id, "192.0.2.1", "mfa-disable-test", ""); err != nil {
					t.Fatal(err)
				}
				cookie, err := manager.IssueWithSession(sid, id, method != "password" || mode == "pending_password")
				if err != nil {
					t.Fatal(err)
				}
				identity, err := age.GenerateX25519Identity()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "disabled-alert.age")
				if err := os.WriteFile(path, []byte(identity.String()), 0o600); err != nil {
					t.Fatal(err)
				}
				flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", "FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-disabled-alert-test-credential-32-bytes", mailoutbox.VerificationIdentityEnv: path}
				if mode != "off" && mode != "late_enable" {
					flags[internalmail.MFADisabledAlertOutboxEnableEnv] = "1"
				}
				if method == "durable_email" {
					flags[internalmail.MFADisableOutboxEnableEnv] = "1"
					flags[mailoutbox.MFADisableOriginEnv] = "https://dashboard.example.test"
				}
				if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
					t.Fatal(err)
				}
				recorder := &mfaDisableInlineRecorder{}
				*server.Mailer() = recorder
				handler := server.Handler()
				var hash []byte
				token := ""
				if strings.HasSuffix(method, "email") {
					issued := mfaDisableEmailPost(t, handler, manager, id, cookie, "", false, true)
					if issued.Code != http.StatusOK {
						t.Fatal("issue failed", issued.Code, issued.Body.String())
					}
					if method == "durable_email" {
						var cipher []byte
						if err := pool.QueryRow(t.Context(), "SELECT sealed_verification,verification_token_hash FROM platform_mail_producer_outbox WHERE template='mfa_disable_request'").Scan(&cipher, &hash); err != nil {
							t.Fatal(err)
						}
						_, raw, err := secretbox.OpenBytes(identity, cipher)
						if err != nil {
							t.Fatal(err)
						}
						token = base64.RawURLEncoding.EncodeToString(raw)
						clear(raw)
						if _, err := pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '25 hours',ready_at=ready_at-interval '25 hours',expires_at=expires_at-interval '25 hours'"); err != nil {
							t.Fatal(err)
						}
					} else {
						for _, field := range strings.Fields(recorder.messages[0].TextBody) {
							if strings.Contains(field, "/disable-email/confirm?token=") {
								u, err := url.Parse(field)
								if err != nil {
									t.Fatal(err)
								}
								token = u.Query().Get("token")
							}
						}
						if token == "" {
							t.Fatal("inline fixture has no token")
						}
						if err := pool.QueryRow(t.Context(), "SELECT token_hash FROM mfa_disable_requests").Scan(&hash); err != nil {
							t.Fatal(err)
						}
						if _, err := pool.Exec(t.Context(), "UPDATE mfa_disable_requests SET requested_at=requested_at-interval '25 hours'"); err != nil {
							t.Fatal(err)
						}
						if _, err := pool.Exec(t.Context(), "UPDATE accounts SET mfa_enrolled_at=mfa_enrolled_at-interval '25 hours' WHERE id=$1", id); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "late_enable" {
					flags[internalmail.MFADisabledAlertOutboxEnableEnv] = "1"
				}
				if mode == "save_failure" {
					if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_disabled_alert_failure CHECK (template<>'mfa_disabled')"); err != nil {
						t.Fatal(err)
					}
				}
				var response *httptest.ResponseRecorder
				if strings.HasSuffix(method, "email") {
					response = mfaDisableEmailPost(t, handler, manager, id, cookie, token, true, mode != "csrf_failure")
				} else {
					form := map[string]string{"recovery_code": codes[0]}
					if method == "password" {
						form = map[string]string{"password": password}
					}
					if mode == "bad_proof" {
						if method == "password" {
							form["password"] = "Wrong!Horse-Battery-Staple-2026"
						} else {
							form["recovery_code"] = codes[1]
						}
					}
					response = disabledAlertPost(t, handler, manager, id, cookie, form, mode != "csrf_failure")
				}
				want := http.StatusOK
				if mode == "save_failure" || mode == "late_enable" {
					want = http.StatusServiceUnavailable
				}
				if mode == "bad_proof" {
					want = http.StatusUnauthorized
				}
				if mode == "pending_password" {
					want = http.StatusForbidden
				}
				if mode == "csrf_failure" {
					want = http.StatusForbidden
				}
				if response.Code != want || len(response.Result().Cookies()) != 0 {
					t.Fatalf("disable=%d want=%d: %s", response.Code, want, response.Body.String())
				}
				account, err := store.AccountByID(t.Context(), id)
				if err != nil || account.MFAEnrolled() != (want != http.StatusOK) {
					t.Fatal("MFA state diverged from paired alert", err)
				}
				var alerts, burns int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FILTER(WHERE template='mfa_disabled'),count(*) FILTER(WHERE template='mfa_recovery_used') FROM platform_mail_producer_outbox").Scan(&alerts, &burns); err != nil {
					t.Fatal(err)
				}
				expected := 0
				if mode == "atomic" {
					expected = 1
				}
				if alerts != expected || burns != 0 {
					t.Fatal("missing or duplicate security notification", alerts, burns)
				}
				expectedInline := 0
				if method == "legacy_email" {
					expectedInline = 1
				}
				if len(recorder.messages) != expectedInline {
					t.Fatal("disabled alert sent inline or duplicated")
				}
				if strings.HasSuffix(method, "email") {
					query := "SELECT consumed_at IS NOT NULL FROM mfa_disable_tokens WHERE token_hash=$1"
					if method == "legacy_email" {
						query = "SELECT consumed_at IS NOT NULL FROM mfa_disable_requests WHERE token_hash=$1"
					}
					var consumed bool
					if err := pool.QueryRow(t.Context(), query, hash).Scan(&consumed); err != nil || consumed != (want == http.StatusOK) {
						t.Fatal("email proof consumed without successful paired removal", err)
					}
				}
			})
		}
	}
}
