//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestVerificationOutboxSignupWiringPreservesResponseAndAtomicIntent(t *testing.T) {
	for _, mode := range []string{"legacy", "durable", "durable_save_failure"} {
		t.Run(mode, func(t *testing.T) {
			env := apidserver.SetupPGHandler(t, api.PlanPro)
			server, pool := *env.S(), *env.Pool()
			identity, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			keyPath := filepath.Join(t.TempDir(), "verification.age")
			if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
				"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-verification-test-credential-32-bytes",
				mailoutbox.VerificationIdentityEnv: keyPath, mailoutbox.VerificationOriginEnv: "https://auth.example.test"}
			if mode != "legacy" {
				flags[internalmail.VerificationOutboxEnableEnv] = "1"
			} else {
				flags[mailoutbox.VerificationIdentityEnv] = "/missing/default-off-verification.key"
				flags[mailoutbox.VerificationOriginEnv] = "http://invalid-when-enabled"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if mode == "durable_save_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_verification_failure CHECK (template<>'email_verification')"); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			request := httptest.NewRequest(http.MethodPost, "https://untrusted.example.net/v1/auth/signup", strings.NewReader(`{"email":"new-verification@example.net","password":"Correct-Horse!Battery-Staple-2026"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Forwarded-Proto", "http")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatal("verification handoff changed signup response", response.Code)
			}
			var tokens, intents int
			if err := pool.QueryRow(t.Context(), `SELECT
                (SELECT count(*) FROM email_verification_tokens t JOIN accounts a ON a.id=t.account_id WHERE a.email='new-verification@example.net'),
                (SELECT count(*) FROM platform_mail_producer_outbox WHERE template='email_verification')`).Scan(&tokens, &intents); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "legacy":
				if tokens != 1 || intents != 0 || recorder.calls != 1 {
					t.Fatal("default-off changed inline verification")
				}
			case "durable_save_failure":
				if tokens != 0 || intents != 0 || recorder.calls != 0 {
					t.Fatal("failed durable save left an orphan token or inline fallback")
				}
			case "durable":
				if tokens != 1 || intents != 1 || recorder.calls != 0 {
					t.Fatal("durable path lost intent or sent an inline duplicate")
				}
				var origin string
				var cipher []byte
				if err := pool.QueryRow(t.Context(), "SELECT verification_origin,sealed_verification FROM platform_mail_producer_outbox WHERE template='email_verification'").Scan(&origin, &cipher); err != nil || origin != "https://auth.example.test" {
					t.Fatal("request authority selected verification origin", err)
				}
				_, raw, err := secretbox.OpenBytes(identity, cipher)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(raw)
				if bytes.Contains(cipher, raw) || strings.Contains(response.Body.String(), base64.RawURLEncoding.EncodeToString(raw)) {
					t.Fatal("verification credential leaked into durable storage or auth response")
				}
				url := "https://auth.example.test/v1/auth/verify-email?token=" + base64.RawURLEncoding.EncodeToString(raw)
				verified := httptest.NewRecorder()
				handler.ServeHTTP(verified, httptest.NewRequest(http.MethodGet, url, nil))
				if verified.Code != http.StatusOK {
					t.Fatal("durable token was not accepted by the existing verification endpoint", verified.Code)
				}
				replayed := httptest.NewRecorder()
				handler.ServeHTTP(replayed, httptest.NewRequest(http.MethodGet, url, nil))
				if replayed.Code == http.StatusOK {
					t.Fatal("durable token could be consumed twice")
				}
			}
			for _, private := range []string{"sealed_verification", "verification_token_hash", "verification_origin", "verify-email:"} {
				if strings.Contains(response.Body.String(), private) {
					t.Fatal("public signup exposed private outbox metadata")
				}
			}
		})
	}
}
