package mailoutbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func disableFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.MFADisableOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	f.env[MFADisableOriginEnv] = "https://dashboard.example.test"
	if err := f.service.ConfigureMFADisable(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.mfaDisable.sender = f.sender
	return f
}

func disableAccount(t *testing.T, f *fixture) string {
	t.Helper()
	id := account(t, f)
	s := state.NewPgStore(f.pool)
	if err := s.SetMFASecret(t.Context(), id, []byte("sealed-factor"), [][]byte{bytes.Repeat([]byte{1}, 32)}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMFAEnrolled(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func queueDisable(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.QueueMFADisable(t.Context(), id, "one@example.net"); err != nil {
		t.Fatal(err)
	}
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='mfa_disable_request' ORDER BY created_at DESC LIMIT 1", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func disableToken(t *testing.T, f *fixture, r q.PlatformMailProducerOutbox) q.MfaDisableToken {
	t.Helper()
	token, err := q.New().ShareMFADisableToken(t.Context(), f.pool, q.ShareMFADisableTokenParams{TokenHash: r.VerificationTokenHash, AccountID: r.AccountID})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// Move only fixture timestamps together; constraints enforce the real 24h/48h
// intervals and production code has no clock override or way to shorten them.
func readyDisable(t *testing.T, f *fixture, r q.PlatformMailProducerOutbox) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '25 hours',ready_at=ready_at-interval '25 hours',expires_at=expires_at-interval '25 hours' WHERE token_hash=$1", r.VerificationTokenHash); err != nil {
		t.Fatal(err)
	}
}

func TestMFADisableExactGateReadsNoPrivateConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.MFADisableOutboxEnableEnv {
				t.Fatal("disabled capability read configuration", k)
			}
			return flag
		}
		var service *Service
		if err := service.ConfigureMFADisable(t.Context(), getenv); err != nil || internalmail.MFADisableOutboxEnabled(getenv) {
			t.Fatal("nonexact gate enabled disable requests", err)
		}
		if err := service.QueueMFADisable(t.Context(), "invalid", "invalid"); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
	var service *Service
	if service.ConfigureMFADisable(t.Context(), func(string) string { return "1" }) == nil {
		t.Fatal("disable requests configured without parent outbox")
	}
}

func TestPGMFADisableAtomicIssuanceAndReplacement(t *testing.T) {
	for _, failure := range []string{"intent", "token"} {
		t.Run(failure, func(t *testing.T) {
			f := disableFixture(t)
			id := disableAccount(t, f)
			old := queueDisable(t, f, id)
			constraint := "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_disable_failure CHECK (template<>'mfa_disable_request') NOT VALID"
			if failure == "token" {
				constraint = "ALTER TABLE mfa_disable_tokens ADD CONSTRAINT injected_disable_failure CHECK (false) NOT VALID"
			}
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if err := f.service.QueueMFADisable(t.Context(), id, "one@example.net"); err == nil {
				t.Fatal("injected write failure succeeded")
			}
			if token := disableToken(t, f, old); token.ConsumedAt.Valid {
				t.Fatal("failed replacement invalidated the existing token")
			}
			var tokens, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM mfa_disable_tokens),(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&tokens, &intents); err != nil || tokens != 1 || intents != 1 || len(f.sender.calls) != 0 {
				t.Fatal("credential and notification did not roll back together", err)
			}
		})
	}
}

func TestPGMFADisableRetryPreservesLinkCooldownAndExpiry(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	r := queueDisable(t, f, id)
	token := disableToken(t, f, r)
	if token.ReadyAt.Time.Sub(token.RequestedAt.Time) != api.MFADisableEmailCooldown || token.ExpiresAt.Time.Sub(token.ReadyAt.Time) != api.MFADisableEmailConfirmWindow || r.ExpiresAt.Time.Sub(token.RequestedAt.Time) != api.InternalMailMFADisableTTL {
		t.Fatal("original cooldown/confirmation/delivery deadlines changed")
	}
	raw, err := credentialPayload(f.service.mfaDisable, r)
	if err != nil || len(raw) != api.MFADisableTokenBytes || !bytes.Equal(api.HashToken(raw), r.VerificationTokenHash) || bytes.Contains(r.SealedVerification, raw) {
		t.Fatal("credential was not encrypted and hash-bound", err)
	}
	defer clear(raw)
	for _, table := range []string{"login_tokens", "password_reset_tokens", "magic_link_tokens", "mfa_disable_requests"} {
		if table == "login_tokens" {
			continue // the deliberately minimal projection has no legacy login table
		}
		var count int
		if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("MFA disable token crossed credential purposes", table, err)
		}
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "queued" || got.VerificationKeyID != r.VerificationKeyID {
		t.Fatal("transient failure discarded original payload")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureMFADisable(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.mfaDisable.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "mfa-disable:"+uuidString(r.ID.Bytes) {
		t.Fatal("retry rebuilt the link or changed idempotency inputs")
	}
	wantLink := "https://dashboard.example.test" + mfaDisablePathV1 + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mail.MFADisableEmailRequestedBodyV1("one@example.net", wantLink, token.RequestedAt.Time)
	if f.sender.calls[0].message.Subject != subject || f.sender.calls[0].message.TextBody != body || !strings.Contains(body, token.ExpiresAt.Time.UTC().Format("2006-01-02 15:04 UTC")) {
		t.Fatal("saved disable-email renderer changed")
	}
	if after := disableToken(t, f, r); !reflect.DeepEqual(after, token) {
		t.Fatal("delivery retry changed credential deadlines")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGMFADisableCooldownSingleUseAndFlagRollback(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	r := queueDisable(t, f, id)
	c := NewMFADisableCredentials(f.pool)
	ready, err := c.Redeem(t.Context(), id, r.VerificationTokenHash)
	if !errors.Is(err, ErrMFADisableCooldown) || !ready.Equal(disableToken(t, f, r).ReadyAt.Time) || disableToken(t, f, r).ConsumedAt.Valid {
		t.Fatal("early request consumed a token or bypassed cooldown", err)
	}
	other := disableAccount(t, f)
	if _, err := c.Redeem(t.Context(), other, r.VerificationTokenHash); !errors.Is(err, ErrMFADisableInvalid) {
		t.Fatal("foreign account used a bound token", err)
	}
	readyDisable(t, f, r)
	// Turning off every delivery flag and removing all keys/origins must not
	// remove the floor on credentials already issued.
	for k := range f.env {
		f.env[k] = ""
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := NewMFADisableCredentials(f.pool).Redeem(t.Context(), id, r.VerificationTokenHash)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrMFADisableInvalid) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var enrolled bool
	if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL OR mfa_secret_encrypted IS NOT NULL OR mfa_recovery_codes_hash IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || enrolled || wins.Load() != 1 || !disableToken(t, f, r).ConsumedAt.Valid {
		t.Fatal("confirmation did not clear exactly one factor atomically", err)
	}
}

func TestPGMFADisableRejectsStaleBindings(t *testing.T) {
	for _, scenario := range []string{"password", "password_delete_restore", "factor_replace_restore", "enrollment", "clear_reenroll", "policy", "address", "inactive", "held", "reissued", "inline_reissued", "expired", "recovery_held", "recovery_generation"} {
		t.Run(scenario, func(t *testing.T) {
			f := disableFixture(t)
			id := disableAccount(t, f)
			r := queueDisable(t, f, id)
			s := state.NewPgStore(f.pool)
			var err error
			switch scenario {
			case "password":
				err = s.SetAccountPassword(t.Context(), id, "new-phc")
			case "password_delete_restore":
				err = s.DeleteAccountPassword(t.Context(), id)
				if err == nil {
					err = s.SetAccountPassword(t.Context(), id, "original-phc")
				}
			case "factor_replace_restore":
				err = s.SetMFASecret(t.Context(), id, []byte("new-factor"), nil)
				if err == nil {
					err = s.SetMFASecret(t.Context(), id, []byte("sealed-factor"), nil)
				}
			case "enrollment":
				err = s.MarkMFAEnrolled(t.Context(), id)
			case "clear_reenroll":
				err = s.ClearMFA(t.Context(), id)
				if err == nil {
					err = s.SetMFASecret(t.Context(), id, []byte("sealed-factor"), nil)
				}
				if err == nil {
					err = s.MarkMFAEnrolled(t.Context(), id)
				}
			case "policy":
				_, err = s.SetMFARequired(t.Context(), id, true)
			case "address":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			case "inactive":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending' WHERE id=$1", id)
			case "held":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET abuse_hold_at=clock_timestamp() WHERE id=$1", id)
			case "reissued":
				_ = queueDisable(t, f, id)
			case "inline_reissued":
				err = s.IssueMFADisableRequest(t.Context(), bytes.Repeat([]byte{2}, 32), id, time.Now().UTC())
			case "expired":
				_, err = f.pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '49 hours',ready_at=ready_at-interval '49 hours',expires_at=expires_at-interval '49 hours' WHERE token_hash=$1", r.VerificationTokenHash)
			case "recovery_held", "recovery_generation":
				generation := uuid.Nil.String()
				if scenario == "recovery_generation" {
					generation = uuid.NewString()
				}
				_, err = f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,$2)", generation, scenario == "recovery_held")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewMFADisableCredentials(f.pool).Redeem(t.Context(), id, r.VerificationTokenHash); !errors.Is(err, ErrMFADisableInvalid) {
				t.Fatal("stale credential passed a binding check", err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, call := range f.sender.calls {
				if call.message.MessageID == "mfa-disable:"+uuidString(r.ID.Bytes) {
					t.Fatal("worker handed off a stale disable link")
				}
			}
		})
	}
}

func TestPGMFADisableConfirmationRollsBackOnFailure(t *testing.T) {
	for _, constraint := range []string{
		"ALTER TABLE accounts ADD CONSTRAINT injected_clear_failure CHECK (mfa_enrolled_at IS NOT NULL)",
		"ALTER TABLE mfa_disable_tokens ADD CONSTRAINT injected_consume_failure CHECK (consumed_at IS NULL)",
	} {
		t.Run(constraint, func(t *testing.T) {
			f := disableFixture(t)
			id := disableAccount(t, f)
			r := queueDisable(t, f, id)
			readyDisable(t, f, r)
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if _, err := NewMFADisableCredentials(f.pool).Redeem(t.Context(), id, r.VerificationTokenHash); err == nil {
				t.Fatal("injected clear/consume failure succeeded")
			}
			var enrolled bool
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || !enrolled || disableToken(t, f, r).ConsumedAt.Valid {
				t.Fatal("MFA clear and token consumption did not roll back together", err)
			}
		})
	}
}

func TestPGMFADisableLateDisableRollsBackIssuance(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.mfaDisable.enabled = enabled.Load
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if err := f.service.queries.LockAdmission(t.Context(), tx, "platform"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.service.QueueMFADisable(ctx, id, "one@example.net") }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	enabled.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late gate closure committed an issuance", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM mfa_disable_tokens)+(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled issuance retained paired state", err)
	}
}
