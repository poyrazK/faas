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

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
)

func mfaAlertRecoverPost(t *testing.T, handler http.Handler, manager *session.Manager, accountID, cookie, code string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	form := map[string]string{"code": code}
	token, err := middleware.IssueForAuthenticated(manager, "mfa_recover", accountID)
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
	r := httptest.NewRequest(http.MethodPost, "https://dashboard.example.test/v1/account/mfa/recover", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "mfa-alert-test")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookie})
	if csrf {
		r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestMFARecoveryAlertOutboxPublicFlowAndFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"legacy", "durable", "save_failure", "late_enable", "csrf_failure", "last_code", "session_failure"} {
		t.Run(mode, func(t *testing.T) {
			// Reuse the existing HMAC/enrollment fixture without adding a second
			// test secret or changing global recovery-key behavior.
			source := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
			codes, _, _ := source.GenerateEnrolledAccount(t)
			mfaAccount, err := (*source.Store()).AccountByID(t.Context(), source.Acct().ID)
			if err != nil {
				t.Fatal(err)
			}
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server, pool, store := *env.S(), *env.Pool(), *env.Store()
			accountID := env.Acct().ID
			hashes := mfaAccount.MFARecoveryCodesHash
			if mode == "last_code" {
				hashes = hashes[:1]
			}
			if err := store.SetMFASecret(t.Context(), accountID, mfaAccount.MFASecretEncrypted, hashes); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkMFAEnrolled(t.Context(), accountID); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkAccountEmailVerified(t.Context(), accountID); err != nil {
				t.Fatal(err)
			}
			manager := *server.Sessions()
			sid := uuid.NewString()
			if _, err := store.CreateSessionWithBinding(t.Context(), sid, accountID, "192.0.2.1", "mfa-alert-test", ""); err != nil {
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
			keyPath := filepath.Join(t.TempDir(), "mfa-alert.age")
			if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
				"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-mfa-alert-test-credential-at-least-32-bytes",
				mailoutbox.VerificationIdentityEnv: keyPath}
			if mode != "legacy" && mode != "late_enable" {
				flags[internalmail.MFARecoveryAlertOutboxEnableEnv] = "1"
			} else {
				flags[mailoutbox.VerificationIdentityEnv] = "/missing/disabled-mfa-alert.key"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.MFARecoveryAlertOutboxEnableEnv] = "1"
			}
			if mode == "save_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_mfa_alert_save_failure CHECK (template<>'mfa_recovery_used')"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "session_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE sessions ADD CONSTRAINT injected_mfa_session_failure CHECK (false) NOT VALID"); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			response := mfaAlertRecoverPost(t, handler, manager, accountID, cookie, codes[0], mode != "csrf_failure")
			wantStatus := http.StatusOK
			wantRemaining, wantIntents := len(hashes)-1, 1
			switch mode {
			case "legacy":
				wantIntents = 0
			case "save_failure", "late_enable":
				wantStatus, wantRemaining, wantIntents = http.StatusServiceUnavailable, len(hashes), 0
			case "csrf_failure":
				wantStatus, wantRemaining, wantIntents = http.StatusForbidden, len(hashes), 0
			case "last_code":
				wantStatus, wantRemaining, wantIntents = http.StatusConflict, 1, 0
			case "session_failure":
				wantStatus = http.StatusServiceUnavailable
			}
			if response.Code != wantStatus {
				t.Fatalf("recovery response=%d, want %d: %s", response.Code, wantStatus, response.Body.String())
			}
			account, err := store.AccountByID(t.Context(), accountID)
			if err != nil || len(account.MFARecoveryCodesHash) != wantRemaining || !account.MFAEnrolled() {
				t.Fatal("recovery changed enrollment or code consumption", err)
			}
			var intents int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_recovery_used'").Scan(&intents); err != nil || intents != wantIntents {
				t.Fatal("security event and alert intent diverged", err, intents)
			}
			wantInline := 0
			if mode == "legacy" {
				wantInline = 1
			}
			if recorder.calls != wantInline {
				t.Fatal("recovery sent a duplicate or fell back inline", recorder.calls)
			}
			if response.Code == http.StatusOK {
				var recovered bool
				for _, c := range response.Result().Cookies() {
					// HTTPS sign-in sets __Host-faas_sid and expires the legacy name (#5522).
					if sessioncookie.IsName(c.Name) && c.Value != "" {
						envelope, err := manager.Verify(c.Value)
						if err != nil || envelope.MfaPending || envelope.Sid != sid || envelope.AccountID != accountID || !envelope.StepUpAt.IsZero() {
							t.Fatal("recovery changed existing MFA session rules", err)
						}
						recovered = true
					}
				}
				if !recovered {
					t.Fatal("successful recovery did not reissue its session")
				}
			} else if len(response.Result().Cookies()) != 0 {
				t.Fatal("failed recovery minted a session proof")
			}
			if wantIntents == 0 {
				return
			}
			var cipher []byte
			if err := pool.QueryRow(t.Context(), "SELECT sealed_security_alert FROM platform_mail_producer_outbox WHERE template='mfa_recovery_used'").Scan(&cipher); err != nil {
				t.Fatal(err)
			}
			_, plain, err := secretbox.OpenBytes(identity, cipher)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(plain)
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(plain, &payload); err != nil || len(payload) != 2 || payload["used_at"] == nil || string(payload["remaining"]) != "9" {
				t.Fatal("security event saved more than its timestamp and remaining count", err)
			}
			if bytes.Contains(cipher, []byte(codes[0])) || bytes.Contains(plain, hashes[0]) || bytes.Contains(response.Body.Bytes(), []byte(codes[0])) {
				t.Fatal("recovery code entered notification state or response")
			}
			if mode == "durable" {
				replay := mfaAlertRecoverPost(t, handler, manager, accountID, cookie, codes[0], true)
				if replay.Code != http.StatusUnauthorized {
					t.Fatal("used recovery code authenticated again", replay.Code)
				}
			}
		})
	}
}
