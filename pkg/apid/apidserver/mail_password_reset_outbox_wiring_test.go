//go:build !no_pg

package apidserver_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func resetFormPost(h http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://untrusted.example.net"+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Forwarded-Proto", "http")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPasswordResetOutboxPublicWiringUniformResponseAndRollback(t *testing.T) {
	for _, mode := range []string{"legacy", "durable", "save_failure", "late_enable"} {
		t.Run(mode, func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server, pool := *env.S(), *env.Pool()
			accountID, email := env.Acct().ID, env.Acct().Email
			if err := (*env.Store()).MarkAccountEmailVerified(t.Context(), accountID); err != nil {
				t.Fatal(err)
			}
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(t.TempDir(), "reset.age")
			if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
				"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-reset-test-credential-at-least-32-bytes",
				mailoutbox.VerificationIdentityEnv: keyPath, mailoutbox.PasswordResetOriginEnv: "https://dashboard.example.test"}
			if mode == "durable" || mode == "save_failure" {
				flags[internalmail.PasswordResetOutboxEnableEnv] = "1"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.PasswordResetOutboxEnableEnv] = "1"
			}
			if mode == "save_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_reset_queue_failure CHECK (template<>'password_reset')"); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			unknown := resetFormPost(handler, "/login/forgot", url.Values{"email": {"unbound-reset@example.net"}})
			known := resetFormPost(handler, "/login/forgot", url.Values{"email": {email}})
			if unknown.Code != http.StatusOK || known.Code != unknown.Code || known.Body.String() != unknown.Body.String() {
				t.Fatal("reset queue outcome exposed account existence", known.Code, unknown.Code)
			}
			var tokens, intents, legacy int
			if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM password_reset_tokens),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_reset'),(SELECT count(*) FROM login_tokens)").Scan(&tokens, &intents, &legacy); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				if tokens != 0 || intents != 0 || legacy != 1 || recorder.calls != 1 {
					t.Fatal("default-off changed inline reset")
				}
				return
			}
			if recorder.calls != 0 || legacy != 0 {
				t.Fatal("durable path sent inline or minted a shared login token")
			}
			if mode != "durable" {
				if tokens != 0 || intents != 0 {
					t.Fatal("failed/unconfigured queue left an orphan reset token")
				}
				return
			}
			if tokens != 1 || intents != 1 {
				t.Fatal("durable reset lost atomic token/intent")
			}
			var origin string
			var cipher []byte
			if err := pool.QueryRow(t.Context(), "SELECT verification_origin,sealed_verification FROM platform_mail_producer_outbox WHERE template='password_reset'").Scan(&origin, &cipher); err != nil || origin != "https://dashboard.example.test" {
				t.Fatal("request authority selected the reset destination", err)
			}
			_, raw, err := secretbox.OpenBytes(identity, cipher)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(raw)
			token := base64.RawURLEncoding.EncodeToString(raw)
			magic := httptest.NewRecorder()
			handler.ServeHTTP(magic, httptest.NewRequest(http.MethodGet, "https://dashboard.example.test/auth/verify?token="+token, nil))
			if magic.Code != http.StatusGone || len(magic.Result().Cookies()) != 0 {
				t.Fatal("reset credential authenticated through magic login", magic.Code)
			}
			// Disable all new issuance/handoff configuration. Redemption must still
			// enforce the saved binding and must not need any encryption material.
			flags[internalmail.PasswordResetOutboxEnableEnv] = "0"
			flags[internalmail.ProducerOutboxEnableEnv] = "0"
			flags[mailoutbox.VerificationIdentityEnv] = "/missing/flag-off-reset.key"
			flags[mailoutbox.PasswordResetOriginEnv] = "http://invalid-while-enabled"
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "ALTER TABLE account_passwords ADD CONSTRAINT injected_reset_write_failure CHECK (false) NOT VALID"); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"token": {token}, "password": {"Reset-Correct-Horse!Battery-Staple-2026"}}
			failed := resetFormPost(handler, "/auth/reset", form)
			if failed.Code != http.StatusInternalServerError {
				t.Fatal("failed credential write did not fail reset", failed.Code)
			}
			var consumed bool
			if err := pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1", api.HashToken(raw)).Scan(&consumed); err != nil || consumed {
				t.Fatal("failed public reset consumed its credential", err)
			}
			if _, err := pool.Exec(t.Context(), "ALTER TABLE account_passwords DROP CONSTRAINT injected_reset_write_failure"); err != nil {
				t.Fatal(err)
			}
			reset := resetFormPost(handler, "/auth/reset", form)
			if reset.Code != http.StatusFound || len(reset.Result().Cookies()) == 0 {
				t.Fatal("issued link failed after flags rolled off", reset.Code)
			}
			phc, err := (*env.Store()).AccountPasswordByAccountID(t.Context(), accountID)
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := auth.Verify(phc, form.Get("password")); err != nil || !ok {
				t.Fatal("replacement password did not commit", err)
			}
			if replay := resetFormPost(handler, "/auth/reset", form); replay.Code != http.StatusGone {
				t.Fatal("reset credential was not single-use", replay.Code)
			}
		})
	}
}
