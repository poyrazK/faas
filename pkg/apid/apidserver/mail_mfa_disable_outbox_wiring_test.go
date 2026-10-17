//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"context"
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
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
)

type mfaDisableInlineRecorder struct{ messages []apidserver.Message }

func (r *mfaDisableInlineRecorder) Send(_ context.Context, message apidserver.Message) error {
	r.messages = append(r.messages, message)
	return nil
}

func TestMFADisableOutboxPageDefaultOff(t *testing.T) {
	env := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	handler := (*env.Srv()).Handler()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "https://dashboard.example.test/auth/reset/mfa?token=invalid", nil))
		if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
			t.Fatal("disabled page performed authentication or exposed a method", method, w.Code)
		}
	}
}

func mfaDisableEmailPost(t *testing.T, handler http.Handler, manager *session.Manager, accountID, cookie, token string, confirm, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	path, action := "/v1/account/mfa/disable-email", "mfa_disable_email"
	form := map[string]string{}
	if confirm {
		path += "/confirm"
		action += "_confirm"
		form["token"] = token
	}
	csrfToken, err := middleware.IssueForAuthenticated(manager, action, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if csrf {
		form["csrf_token"] = csrfToken
	}
	body, err := json.Marshal(form)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://untrusted-request.example.test"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "mfa-disable-test")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
	if csrf {
		r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfToken})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestMFADisableOutboxPublicIssuanceConfirmationAndRollback(t *testing.T) {
	for _, mode := range []string{"legacy", "durable", "save_failure", "late_enable", "csrf_request", "csrf_confirm", "consume_failure", "password_changed", "factor_changed", "expired", "pruned", "recovery_held", "unenrolled"} {
		t.Run(mode, func(t *testing.T) {
			source := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
			_, _, _ = source.GenerateEnrolledAccount(t)
			mfaAccount, err := (*source.Store()).AccountByID(t.Context(), source.Acct().ID)
			if err != nil {
				t.Fatal(err)
			}
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server, pool, store := *env.S(), *env.Pool(), *env.Store()
			accountID := env.Acct().ID
			if mode != "unenrolled" {
				if err := store.SetMFASecret(t.Context(), accountID, mfaAccount.MFASecretEncrypted, mfaAccount.MFARecoveryCodesHash); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkMFAEnrolled(t.Context(), accountID); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.MarkAccountEmailVerified(t.Context(), accountID); err != nil {
				t.Fatal(err)
			}
			manager := *server.Sessions()
			sid := uuid.NewString()
			if _, err := store.CreateSessionWithBinding(t.Context(), sid, accountID, "192.0.2.1", "mfa-disable-test", ""); err != nil {
				t.Fatal(err)
			}
			cookie, err := manager.IssueWithSession(sid, accountID, true)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(t.TempDir(), "disable.age")
			if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
				"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-mfa-disable-test-credential-at-least-32-bytes",
				mailoutbox.VerificationIdentityEnv: keyPath, mailoutbox.MFADisableOriginEnv: "https://dashboard.example.test"}
			if mode != "legacy" && mode != "late_enable" {
				flags[internalmail.MFADisableOutboxEnableEnv] = "1"
			} else {
				flags[mailoutbox.VerificationIdentityEnv] = "/missing/disabled-key.age"
				flags[mailoutbox.MFADisableOriginEnv] = "http://invalid-while-off"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.MFADisableOutboxEnableEnv] = "1"
			}
			if mode == "save_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_disable_failure CHECK (template<>'mfa_disable_request')"); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &mfaDisableInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			response := mfaDisableEmailPost(t, handler, manager, accountID, cookie, "", false, mode != "csrf_request")
			wantStatus, wantTokens, wantIntents := http.StatusOK, 1, 1
			switch mode {
			case "legacy":
				wantTokens, wantIntents = 0, 0
			case "save_failure", "late_enable", "unenrolled":
				wantStatus, wantTokens, wantIntents = http.StatusServiceUnavailable, 0, 0
			case "csrf_request":
				wantStatus, wantTokens, wantIntents = http.StatusForbidden, 0, 0
			}
			if response.Code != wantStatus {
				t.Fatalf("request=%d want=%d: %s", response.Code, wantStatus, response.Body.String())
			}
			var tokens, intents, legacy int
			if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM mfa_disable_tokens),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_disable_request'),(SELECT count(*) FROM mfa_disable_requests)").Scan(&tokens, &intents, &legacy); err != nil || tokens != wantTokens || intents != wantIntents {
				t.Fatal("request and mail intent diverged", err)
			}
			if mode == "legacy" {
				if len(recorder.messages) != 1 || legacy != 1 {
					t.Fatal("default-off changed inline issuance")
				}
				var token string
				for _, field := range strings.Fields(recorder.messages[0].TextBody) {
					if strings.Contains(field, "/disable-email/confirm?token=") {
						link, err := url.Parse(field)
						if err != nil {
							t.Fatal(err)
						}
						token = link.Query().Get("token")
					}
				}
				if token == "" || mfaDisableEmailPost(t, handler, manager, accountID, cookie, token, true, true).Code != http.StatusTooEarly {
					t.Fatal("legacy cooldown changed")
				}
				if _, err := pool.Exec(t.Context(), "UPDATE mfa_disable_requests SET requested_at=requested_at-interval '25 hours'"); err != nil {
					t.Fatal(err)
				}
				if result := mfaDisableEmailPost(t, handler, manager, accountID, cookie, token, true, true); result.Code != http.StatusOK {
					t.Fatal("legacy confirmation failed", result.Code)
				}
				return
			}
			if len(recorder.messages) != 0 || legacy != 0 {
				t.Fatal("durable issuance fell back to inline mail or legacy credentials")
			}
			if wantTokens == 0 {
				return
			}
			var origin string
			var cipher, hash []byte
			if err := pool.QueryRow(t.Context(), "SELECT verification_origin,sealed_verification,verification_token_hash FROM platform_mail_producer_outbox WHERE account_id=$1", accountID).Scan(&origin, &cipher, &hash); err != nil || origin != "https://dashboard.example.test" {
				t.Fatal("request authority selected the confirmation link", err)
			}
			_, raw, err := secretbox.OpenBytes(identity, cipher)
			if err != nil || len(raw) != api.MFADisableTokenBytes || !bytes.Equal(api.HashToken(raw), hash) {
				t.Fatal("durable request lost its encrypted token", err)
			}
			defer clear(raw)
			token := base64.RawURLEncoding.EncodeToString(raw)
			pageRequest := httptest.NewRequest(http.MethodGet, "https://dashboard.example.test/auth/reset/mfa?token="+token, nil)
			pageRequest.Header.Set("User-Agent", "mfa-disable-test")
			pageRequest.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
			page := httptest.NewRecorder()
			handler.ServeHTTP(page, pageRequest)
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Disable MFA") || !strings.Contains(page.Body.String(), token) || page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" || page.Header().Get("Content-Security-Policy") == "" || len(page.Result().Cookies()) != 0 {
				t.Fatal("email link did not render a private, inert confirmation page", page.Code)
			}
			var prefetched bool
			if err := pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM mfa_disable_tokens WHERE token_hash=$1", hash).Scan(&prefetched); err != nil || prefetched {
				t.Fatal("GET or email prefetch consumed a token", err)
			}
			// Purpose isolation is enforced even after all mail configuration is
			// withdrawn. No MFA host key is needed for a saved durable link.
			flags[internalmail.MFADisableOutboxEnableEnv] = "0"
			flags[internalmail.ProducerOutboxEnableEnv] = "0"
			flags[mailoutbox.VerificationIdentityEnv] = "/missing/rollback-key.age"
			flags[mailoutbox.MFADisableOriginEnv] = "http://invalid-while-off"
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			hidden := httptest.NewRecorder()
			handler.ServeHTTP(hidden, pageRequest.Clone(t.Context()))
			if hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), token) {
				t.Fatal("rollback exposed the new confirmation page")
			}
			apidserver.SetMFAIdentity(func() *age.X25519Identity { return nil })
			apidserver.SetMFAIdentities(func() []*age.X25519Identity { return nil })
			wrongPurpose := resetFormPost(handler, "/auth/reset", url.Values{"token": {token}, "password": {"Wrong-Purpose!Horse-Battery-Staple-2026"}})
			if wrongPurpose.Code != http.StatusGone || len(wrongPurpose.Result().Cookies()) != 0 {
				t.Fatal("MFA disable token reset a password")
			}
			if early := mfaDisableEmailPost(t, handler, manager, accountID, cookie, token, true, true); early.Code != http.StatusTooEarly || early.Header().Get("Retry-After") == "" || len(early.Result().Cookies()) != 0 {
				t.Fatal("cooldown or session boundary changed", early.Code)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '25 hours',ready_at=ready_at-interval '25 hours',expires_at=expires_at-interval '25 hours'"); err != nil {
				t.Fatal(err)
			}
			wantConfirm := http.StatusOK
			switch mode {
			case "csrf_confirm":
				wantConfirm = http.StatusForbidden
			case "consume_failure":
				if _, err := pool.Exec(t.Context(), "ALTER TABLE mfa_disable_tokens ADD CONSTRAINT injected_consume_failure CHECK (consumed_at IS NULL)"); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusServiceUnavailable
			case "password_changed":
				if err := store.SetAccountPassword(t.Context(), accountID, "new-password-phc"); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusUnauthorized
			case "factor_changed":
				if err := store.SetMFASecret(t.Context(), accountID, mfaAccount.MFASecretEncrypted, mfaAccount.MFARecoveryCodesHash); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusUnauthorized
			case "expired":
				if _, err := pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '24 hours',ready_at=ready_at-interval '24 hours',expires_at=expires_at-interval '24 hours'"); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusUnauthorized
			case "pruned":
				if _, err := pool.Exec(t.Context(), "DELETE FROM mfa_disable_tokens"); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusUnauthorized
			case "recovery_held":
				if _, err := pool.Exec(t.Context(), "WITH run AS (INSERT INTO internal_mail_recovery_runs(namespace,recovery_id) VALUES('platform',$1) RETURNING namespace,recovery_id) INSERT INTO internal_mail_recovery_state(namespace,recovery_id,revision,held) SELECT namespace,recovery_id,1,true FROM run", uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				wantConfirm = http.StatusUnauthorized
			}
			confirmed := mfaDisableEmailPost(t, handler, manager, accountID, cookie, token, true, mode != "csrf_confirm")
			if confirmed.Code != wantConfirm || len(confirmed.Result().Cookies()) != 0 {
				t.Fatalf("confirm=%d want=%d: %s", confirmed.Code, wantConfirm, confirmed.Body.String())
			}
			account, err := store.AccountByID(t.Context(), accountID)
			if err != nil || account.MFAEnrolled() != (wantConfirm != http.StatusOK) {
				t.Fatal("confirmation changed the wrong MFA state", err)
			}
			var consumed bool
			if mode != "pruned" {
				if err := pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM mfa_disable_tokens WHERE token_hash=$1", hash).Scan(&consumed); err != nil || consumed != (wantConfirm == http.StatusOK) {
					t.Fatal("confirmation and token consumption diverged", err)
				}
			}
			if wantConfirm == http.StatusOK && mfaDisableEmailPost(t, handler, manager, accountID, cookie, token, true, true).Code != http.StatusUnauthorized {
				t.Fatal("confirmed token replayed")
			}
		})
	}
}
