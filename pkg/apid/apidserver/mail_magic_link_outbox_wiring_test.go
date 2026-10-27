//go:build !no_pg

package apidserver_test

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMagicLinkOutboxPublicWiringUniformResponseAndRollback(t *testing.T) {
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
			keyPath := filepath.Join(t.TempDir(), "magic.age")
			if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1",
				"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-magic-link-test-credential-at-least-32-bytes",
				mailoutbox.VerificationIdentityEnv: keyPath, mailoutbox.MagicLinkOriginEnv: "https://dashboard.example.test"}
			if mode == "durable" || mode == "save_failure" {
				flags[internalmail.MagicLinkOutboxEnableEnv] = "1"
			} else {
				flags[mailoutbox.MagicLinkOriginEnv] = "http://invalid-while-disabled"
			}
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if mode == "late_enable" {
				flags[internalmail.MagicLinkOutboxEnableEnv] = "1"
			}
			if mode == "save_failure" {
				if _, err := pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_magic_queue_failure CHECK (template<>'magic_link')"); err != nil {
					t.Fatal(err)
				}
			}
			recorder := &outboxInlineRecorder{}
			*server.Mailer() = recorder
			handler := server.Handler()
			fresh := resetFormPost(handler, "/v1/auth/signup/magic-link", url.Values{"email": {"fresh-magic@example.net"}})
			known := resetFormPost(handler, "/v1/auth/signup/magic-link", url.Values{"email": {email}})
			invalid := resetFormPost(handler, "/v1/auth/signup/magic-link", url.Values{"email": {"invalid"}})
			if fresh.Code != http.StatusOK || known.Code != fresh.Code || known.Body.String() != fresh.Body.String() || invalid.Code != known.Code || invalid.Body.String() != known.Body.String() {
				t.Fatal("magic-link queue outcome exposed account existence", fresh.Code, known.Code, invalid.Code)
			}
			var tokens, intents, legacy int
			if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM magic_link_tokens),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='magic_link'),(SELECT count(*) FROM login_tokens)").Scan(&tokens, &intents, &legacy); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				if tokens != 0 || intents != 0 || legacy != 2 || recorder.calls != 2 {
					t.Fatal("default-off changed inline magic links")
				}
				return
			}
			if recorder.calls != 0 || legacy != 0 {
				t.Fatal("durable path sent inline or minted shared-purpose tokens")
			}
			if mode != "durable" {
				if tokens != 0 || intents != 0 {
					t.Fatal("failed/unconfigured queue left an orphan credential")
				}
				return
			}
			if tokens != 2 || intents != 2 {
				t.Fatal("magic links lost atomic credentials and intents")
			}
			var origin string
			var cipher []byte
			if err := pool.QueryRow(t.Context(), "SELECT verification_origin,sealed_verification FROM platform_mail_producer_outbox WHERE template='magic_link' AND account_id=$1", accountID).Scan(&origin, &cipher); err != nil || origin != "https://dashboard.example.test" {
				t.Fatal("request authority selected the sign-in destination", err)
			}
			_, raw, err := secretbox.OpenBytes(identity, cipher)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(raw)
			token := base64.RawURLEncoding.EncodeToString(raw)
			wrongPurpose := resetFormPost(handler, "/auth/reset", url.Values{"token": {token}, "password": {"Wrong-Purpose!Horse-Battery-Staple-2026"}})
			if wrongPurpose.Code != http.StatusGone || len(wrongPurpose.Result().Cookies()) != 0 {
				t.Fatal("sign-in link reset a password", wrongPurpose.Code)
			}
			// Billing grace periods still allow sign-in to resolve a past-due bill.
			if _, err := pool.Exec(t.Context(), "UPDATE accounts SET status='past_due',past_due_at=clock_timestamp() WHERE id=$1", accountID); err != nil {
				t.Fatal(err)
			}
			// Roll back every issuance/handoff setting, then require current MFA
			// policy even though the link was issued before policy changed.
			flags[internalmail.MagicLinkOutboxEnableEnv] = "0"
			flags[internalmail.ProducerOutboxEnableEnv] = "0"
			flags[mailoutbox.VerificationIdentityEnv] = "/missing/flag-off-magic.key"
			flags[mailoutbox.MagicLinkOriginEnv] = "http://invalid-while-disabled"
			if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
				t.Fatal(err)
			}
			if _, err := (*env.Store()).SetMFARequired(t.Context(), accountID, true); err != nil {
				t.Fatal(err)
			}
			verify := httptest.NewRecorder()
			handler.ServeHTTP(verify, httptest.NewRequest(http.MethodGet, "https://dashboard.example.test/auth/verify?token="+token, nil))
			if verify.Code != http.StatusFound || verify.Header().Get("Location") != "/dashboard/" {
				t.Fatal("issued sign-in link failed after flag rollback", verify.Code)
			}
			var cookie *http.Cookie
			for _, c := range verify.Result().Cookies() {
				// HTTPS sign-in sets __Host-faas_sid and expires the legacy name (#5522).
				if sessioncookie.IsName(c.Name) && c.Value != "" {
					cookie = c
				}
			}
			if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("sign-in lost the session cookie contract")
			}
			envelope, err := (*server.Sessions()).Verify(cookie.Value)
			if err != nil || envelope.AccountID != accountID || !envelope.MfaPending {
				t.Fatal("magic-link sign-in bypassed current MFA policy", err)
			}
			if _, err := (*env.Store()).GetSession(t.Context(), envelope.Sid); err != nil {
				t.Fatal("magic-link sign-in lost its server-side session", err)
			}
			replay := httptest.NewRecorder()
			handler.ServeHTTP(replay, httptest.NewRequest(http.MethodGet, "https://dashboard.example.test/auth/verify?token="+token, nil))
			if replay.Code != http.StatusGone || len(replay.Result().Cookies()) != 0 {
				t.Fatal("single-use sign-in link replay authenticated", replay.Code)
			}
		})
	}
}

func TestMagicLinkOutboxNewAccountProofRevokesPreVerificationCredentials(t *testing.T) {
	env := apidserver.SetupPGHandler(t, api.PlanPro)
	server, pool, store := *env.S(), *env.Pool(), *env.Store()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "magic.age")
	if err := os.WriteFile(keyPath, []byte(identity.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{internalmail.EnableEnv: "1", internalmail.ProducersEnableEnv: "1", internalmail.SchedulingEnableEnv: "1", internalmail.ProducerOutboxEnableEnv: "1", internalmail.MagicLinkOutboxEnableEnv: "1",
		"FAAS_MAIL_TRANSPORT": "internal-producer", "FAAS_MAIL_FROM": "notify@example.test", "FAAS_INTERNAL_MAIL_PRODUCER_ID": "auth", "FAAS_INTERNAL_MAIL_PRODUCER_TOKEN": "private-apid-magic-proof-test-credential-at-least-32-bytes",
		mailoutbox.VerificationIdentityEnv: keyPath, mailoutbox.MagicLinkOriginEnv: "https://dashboard.example.test"}
	if err := server.ConfigureMailOutbox(t.Context(), pool, func(k string) string { return flags[k] }); err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	response := resetFormPost(handler, "/v1/auth/signup/magic-link", url.Values{"email": {"owner-proof@example.net"}})
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	account, err := store.AccountByEmail(t.Context(), "owner-proof@example.net")
	if err != nil || account.EmailVerified() {
		t.Fatal("signup bypassed proof of ownership", err)
	}
	// Simulate credentials minted by the unverified signup creator before the
	// address owner requests this link; a password write invalidates older links.
	if err := store.SetAccountPassword(t.Context(), account.ID, "pre-verification-password"); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if _, err := store.CreateSessionWithBinding(t.Context(), sessionID, account.ID, "192.0.2.1", "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, api.HashToken([]byte("pre-verification-api-key")), "unproved key", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if response := resetFormPost(handler, "/v1/auth/signup/magic-link", url.Values{"email": {account.Email}}); response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	var cipher []byte
	if err := pool.QueryRow(t.Context(), "SELECT sealed_verification FROM platform_mail_producer_outbox WHERE account_id=$1 ORDER BY created_at DESC LIMIT 1", account.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	_, raw, err := secretbox.OpenBytes(identity, cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	link := "https://dashboard.example.test/auth/verify?token=" + base64.RawURLEncoding.EncodeToString(raw)
	// A failed claim must not consume the link or partially revoke credentials.
	if _, err := pool.Exec(t.Context(), "ALTER TABLE accounts ADD CONSTRAINT injected_magic_proof_failure CHECK (email<>'owner-proof@example.net' OR email_verified_at IS NULL)"); err != nil {
		t.Fatal(err)
	}
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, link, nil))
	if failed.Code != http.StatusInternalServerError || len(failed.Result().Cookies()) != 0 {
		t.Fatal("failed ownership transaction authenticated", failed.Code)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), account.ID); err != nil {
		t.Fatal("failed ownership proof lost its password", err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE accounts DROP CONSTRAINT injected_magic_proof_failure"); err != nil {
		t.Fatal(err)
	}
	verified := httptest.NewRecorder()
	handler.ServeHTTP(verified, httptest.NewRequest(http.MethodGet, link, nil))
	if verified.Code != http.StatusFound || len(verified.Result().Cookies()) == 0 {
		t.Fatal("new account did not sign in after ownership proof", verified.Code)
	}
	account, err = store.AccountByID(t.Context(), account.ID)
	if err != nil || !account.EmailVerified() {
		t.Fatal("ownership proof did not verify the bound address", err)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("ownership proof retained the pre-verification password", err)
	}
	oldSession, err := store.GetSession(t.Context(), sessionID)
	if err != nil || oldSession.RevokedAt == nil {
		t.Fatal("ownership proof retained a pre-verification session", err)
	}
	keys, err := store.ListAPIKeys(t.Context(), account.ID)
	if err != nil || len(keys) != 0 {
		t.Fatal("ownership proof retained pre-verification API keys", err)
	}
}
