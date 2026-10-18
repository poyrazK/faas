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

func magicFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.MagicLinkOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	f.env[MagicLinkOriginEnv] = "https://dashboard.example.test"
	if err := f.service.ConfigureMagicLink(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.magicLink.sender = f.sender
	return f
}

func magicAccount(t *testing.T, f *fixture) string { t.Helper(); return resetAccount(t, f) }

func queueMagic(t *testing.T, f *fixture, accountID string) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.QueueMagicLink(t.Context(), accountID, "one@example.net"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='magic_link' ORDER BY created_at DESC LIMIT 1", accountID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return row(t, f, id)
}

func magicRaw(t *testing.T, f *fixture, r q.PlatformMailProducerOutbox) []byte {
	t.Helper()
	raw, err := credentialPayload(f.service.magicLink, r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMagicLinkOutboxExactGateAndIndependentConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.MagicLinkOutboxEnableEnv {
				t.Fatal("disabled magic-link gate read private configuration", k)
			}
			return flag
		}
		var s *Service
		if err := s.ConfigureMagicLink(t.Context(), getenv); err != nil || internalmail.MagicLinkOutboxEnabled(getenv) {
			t.Fatal("nonexact magic-link gate enabled", err)
		}
		if err := s.QueueMagicLink(t.Context(), "invalid", "invalid"); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
	var disabled *Service
	if err := disabled.ConfigureMagicLink(t.Context(), func(string) string { return "1" }); err == nil {
		t.Fatal("magic-link handoff enabled without configured parent")
	}
	f := magicFixture(t)
	r := queueMagic(t, f, magicAccount(t, f))
	f.env[internalmail.MagicLinkOutboxEnableEnv] = "0"
	f.env[internalmail.VerificationOutboxEnableEnv] = "1"
	f.env[VerificationOriginEnv] = f.env[MagicLinkOriginEnv]
	if err := f.service.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.PasswordResetOutboxEnableEnv] = "1"
	f.env[PasswordResetOriginEnv] = f.env[MagicLinkOriginEnv]
	if err := f.service.ConfigurePasswordReset(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := row(t, f, uuidString(r.ID.Bytes))
	if after.State != "queued" || after.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("sibling gate claimed a disabled magic-link credential")
	}
}

func TestPGMagicLinkOutboxAtomicIssuanceAndLateDisable(t *testing.T) {
	for _, table := range []string{"magic_link_tokens", "platform_mail_producer_outbox"} {
		t.Run(table, func(t *testing.T) {
			f := magicFixture(t)
			id := magicAccount(t, f)
			constraint := "ALTER TABLE magic_link_tokens ADD CONSTRAINT injected_reset_failure CHECK (password_revision<0)"
			if table == "platform_mail_producer_outbox" {
				constraint = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_reset_failure CHECK (template<>'magic_link')"
			}
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if err := f.service.QueueMagicLink(t.Context(), id, "one@example.net"); err == nil {
				t.Fatal("injected failure committed a magic-link credential")
			}
			var tokens, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM magic_link_tokens),(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&tokens, &intents); err != nil || tokens != 0 || intents != 0 {
				t.Fatal("magic-link token and intent did not roll back together", err)
			}
		})
	}
	f := magicFixture(t)
	id := magicAccount(t, f)
	var on atomic.Bool
	on.Store(true)
	f.service.magicLink.enabled = on.Load
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
	go func() { done <- f.service.QueueMagicLink(ctx, id, "one@example.net") }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late-disabled magic-link issuance committed", err)
	}
}

func TestPGMagicLinkOutboxPreservesSignInAccountPolicy(t *testing.T) {
	for _, policy := range []struct {
		name, status  string
		held, allowed bool
	}{
		{"active", "active", false, true},
		{"billing_grace", "past_due", false, true},
		{"suspended", "suspended", false, false},
		{"deletion_pending", "deleted_pending", false, false},
		{"abuse_hold", "active", true, false},
	} {
		t.Run(policy.name, func(t *testing.T) {
			f := magicFixture(t)
			id := magicAccount(t, f)
			if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status=$2,abuse_hold_at=CASE WHEN $3::boolean THEN clock_timestamp() ELSE NULL END WHERE id=$1", id, policy.status, policy.held); err != nil {
				t.Fatal(err)
			}
			err := f.service.QueueMagicLink(t.Context(), id, "one@example.net")
			if !policy.allowed {
				if !errors.Is(err, ErrAccountUnavailable) {
					t.Fatal("unavailable account received a durable sign-in credential", err)
				}
				return
			}
			if err != nil {
				t.Fatal("eligible account could not request sign-in", err)
			}
			var hash []byte
			if err := f.pool.QueryRow(t.Context(), "SELECT token_hash FROM magic_link_tokens WHERE account_id=$1", id).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil || len(f.sender.calls) != 1 {
				t.Fatal("eligible account could not receive sign-in mail", err)
			}
			if result, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), hash); err != nil || result.AccountID != id {
				t.Fatal("eligible account could not redeem its sign-in link", err)
			}
		})
	}
}

func TestPGMagicLinkOutboxStableRetryAndPurposeIsolation(t *testing.T) {
	f := magicFixture(t)
	accountID := magicAccount(t, f)
	r := queueMagic(t, f, accountID)
	raw := magicRaw(t, f, r)
	defer clear(raw)
	if bytes.Contains(r.SealedVerification, raw) || !bytes.Equal(api.HashToken(raw), r.VerificationTokenHash) {
		t.Fatal("sign-in token was not encrypted and hash-bound")
	}
	var expires, created time.Time
	if err := f.pool.QueryRow(t.Context(), "SELECT expires_at,created_at FROM magic_link_tokens WHERE token_hash=$1", r.VerificationTokenHash).Scan(&expires, &created); err != nil || !expires.Equal(r.ExpiresAt.Time) || expires.Sub(created) > api.MagicLinkTTL || expires.Sub(created) < api.MagicLinkTTL-time.Second {
		t.Fatal("sign-in token lost its original 15-minute deadline", err)
	}
	if _, err := NewResetCredentials(f.pool).Redeem(t.Context(), r.VerificationTokenHash, "wrong-purpose-phc"); !errors.Is(err, ErrPasswordResetUnknown) {
		t.Fatal("sign-in token changed a password", err)
	}
	if _, err := state.NewPgStore(f.pool).ConsumeLoginToken(t.Context(), r.VerificationTokenHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("durable token entered the legacy shared-purpose table", err)
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error {
		return &mail.TransientError{Err: errors.New("lost acknowledgement")}
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureMagicLink(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.magicLink.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) {
		t.Fatal("retry changed the original sign-in payload, key or deadline")
	}
	if !strings.Contains(f.sender.calls[0].message.TextBody, "https://dashboard.example.test/auth/verify?token="+base64.RawURLEncoding.EncodeToString(raw)) || f.sender.calls[0].message.MessageID != "magic-link:"+uuidString(r.ID.Bytes) {
		t.Fatal("sign-in link or admission identity changed")
	}
	assertVerificationErased(t, row(t, f, uuidString(r.ID.Bytes)))
	f.env[internalmail.MagicLinkOutboxEnableEnv] = "0"
	credentials := NewMagicLinkCredentials(f.pool)
	if result, err := credentials.Redeem(t.Context(), r.VerificationTokenHash); err != nil || result.AccountID != accountID {
		t.Fatal("issued link could not redeem after flag rollback", err)
	}
	if phc, err := state.NewPgStore(f.pool).AccountPasswordByAccountID(t.Context(), accountID); err != nil || phc != "original-phc" {
		t.Fatal("verified sign-in changed the existing password", err)
	}
	if _, err := credentials.Redeem(t.Context(), r.VerificationTokenHash); !errors.Is(err, ErrMagicLinkInvalid) {
		t.Fatal("sign-in token replay authenticated", err)
	}
}

func TestPGMagicLinkOutboxRejectsStaleCredentials(t *testing.T) {
	for _, scenario := range []string{"password_changed", "password_deleted_and_restored", "address_changed", "verification_changed", "inactive", "abuse_held", "used", "expired", "suppressed", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			f := magicFixture(t)
			accountID := magicAccount(t, f)
			r := queueMagic(t, f, accountID)
			store := state.NewPgStore(f.pool)
			var err error
			switch scenario {
			case "password_changed":
				err = store.SetAccountPassword(t.Context(), accountID, "different-phc")
			case "password_deleted_and_restored":
				if err = store.DeleteAccountPassword(t.Context(), accountID); err == nil {
					err = store.SetAccountPassword(t.Context(), accountID, "original-phc")
				}
			case "address_changed":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", accountID)
			case "verification_changed":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email_verified_at=NULL WHERE id=$1", accountID)
			case "abuse_held":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET abuse_hold_at=clock_timestamp() WHERE id=$1", accountID)
			case "inactive":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending' WHERE id=$1", accountID)
			case "used":
				_, err = NewMagicLinkCredentials(f.pool).Redeem(t.Context(), r.VerificationTokenHash)
			case "expired":
				_, err = f.pool.Exec(t.Context(), "UPDATE magic_link_tokens SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute'")
			case "suppressed":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "corrupt":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_verification=decode(repeat('00',100),'hex') WHERE id=$1", r.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			after := row(t, f, uuidString(r.ID.Bytes))
			want := "cancelled"
			if scenario == "suppressed" {
				want = "suppressed"
			}
			if scenario == "corrupt" {
				want = "failed"
			}
			if after.State != want || len(f.sender.calls) != 0 {
				t.Fatal("unavailable credential reached delivery", after.State)
			}
			assertVerificationErased(t, after)
			if scenario != "suppressed" && scenario != "corrupt" {
				if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), r.VerificationTokenHash); !errors.Is(err, ErrMagicLinkInvalid) && !(scenario == "expired" && errors.Is(err, ErrMagicLinkUnknown)) {
					t.Fatal("unavailable credential authenticated", err)
				}
			}
		})
	}
}

func TestPGMagicLinkOwnershipProofCleanupAndConsumptionAreAtomic(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			f := magicFixture(t)
			accountID := account(t, f)
			store := state.NewPgStore(f.pool)
			if err := store.SetAccountPassword(t.Context(), accountID, "unproved-password"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO sessions(id,account_id) VALUES($1,$2)", uuid.NewString(), accountID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO api_keys(id,account_id) VALUES($1,$2)", uuid.NewString(), accountID); err != nil {
				t.Fatal(err)
			}
			first := queueMagic(t, f, accountID)
			sibling := queueMagic(t, f, accountID)
			if fail {
				if _, err := f.pool.Exec(t.Context(), "ALTER TABLE accounts ADD CONSTRAINT injected_magic_claim_failure CHECK (email_verified_at IS NULL)"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), first.VerificationTokenHash)
			var verified, consumed bool
			var passwords, sessions, keys int
			if scanErr := f.pool.QueryRow(t.Context(), `SELECT a.email_verified_at IS NOT NULL,t.consumed_at IS NOT NULL,
                (SELECT count(*) FROM account_passwords),(SELECT count(*) FROM sessions WHERE revoked_at IS NULL),(SELECT count(*) FROM api_keys)
                FROM accounts a JOIN magic_link_tokens t ON t.account_id=a.id WHERE t.token_hash=$1`, first.VerificationTokenHash).Scan(&verified, &consumed, &passwords, &sessions, &keys); scanErr != nil {
				t.Fatal(scanErr)
			}
			if fail {
				if err == nil || verified || consumed || passwords != 1 || sessions != 1 || keys != 1 {
					t.Fatal("failed ownership claim partially committed", err)
				}
				return
			}
			if err != nil || !verified || !consumed || passwords != 0 || sessions != 0 || keys != 0 || result.AccountID != accountID || result.RevokedSessions != 1 || result.RevokedAPIKeys != 1 {
				t.Fatal("ownership proof did not revoke pre-verification credentials", err)
			}
			if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), sibling.VerificationTokenHash); !errors.Is(err, ErrMagicLinkInvalid) {
				t.Fatal("ownership claim revived a pre-verification sibling", err)
			}
		})
	}
}

func TestPGMagicLinkConcurrentRedemptionHasOneWinner(t *testing.T) {
	f := magicFixture(t)
	r := queueMagic(t, f, magicAccount(t, f))
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), r.VerificationTokenHash); err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrMagicLinkInvalid) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("single-use token had multiple winners")
	}
}

func TestPGMagicLinkExpiryDuringOwnershipCleanupRollsBack(t *testing.T) {
	f := magicFixture(t)
	accountID := account(t, f)
	if err := state.NewPgStore(f.pool).SetAccountPassword(t.Context(), accountID, "unproved-password"); err != nil {
		t.Fatal(err)
	}
	r := queueMagic(t, f, accountID)
	deadline := time.Now().Add(350 * time.Millisecond)
	if _, err := f.pool.Exec(t.Context(), "UPDATE magic_link_tokens SET expires_at=$1 WHERE token_hash=$2", deadline, r.VerificationTokenHash); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "SELECT account_id FROM account_passwords WHERE account_id=$1 FOR UPDATE", accountID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewMagicLinkCredentials(f.pool).Redeem(ctx, r.VerificationTokenHash)
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	timer := time.NewTimer(max(time.Until(deadline)+time.Millisecond, 0))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrMagicLinkInvalid) {
		t.Fatal("expired sign-in crossed an ownership-cleanup wait", err)
	}
	var verified, consumed bool
	if err := f.pool.QueryRow(ctx, "SELECT a.email_verified_at IS NOT NULL,t.consumed_at IS NOT NULL FROM accounts a JOIN magic_link_tokens t ON t.account_id=a.id WHERE t.token_hash=$1", r.VerificationTokenHash).Scan(&verified, &consumed); err != nil || verified || consumed {
		t.Fatal("expired sign-in partially committed ownership or consumption", err)
	}
	if phc, err := state.NewPgStore(f.pool).AccountPasswordByAccountID(ctx, accountID); err != nil || phc != "unproved-password" {
		t.Fatal("expired sign-in revoked a password", err)
	}
}

func TestPGMagicLinkExpiredBindingPurgeRemainsBoundedWithChildOff(t *testing.T) {
	f := magicFixture(t)
	accountID := magicAccount(t, f)
	live := queueMagic(t, f, accountID)
	batch := api.InternalMailProducerOutboxMaintenanceBatch
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO magic_link_tokens
        (token_hash,account_id,email_address,password_revision,email_verified_at,namespace,recovery_id,expires_at,created_at)
        SELECT decode(lpad(to_hex(n),64,'0'),'hex'),a.id,lower(a.email),a.password_reset_revision,a.email_verified_at,
            'platform','00000000-0000-0000-0000-000000000000'::uuid,
            clock_timestamp()-interval '1 minute',clock_timestamp()-interval '2 minutes'
        FROM accounts a CROSS JOIN generate_series(1,$2::integer) n WHERE a.id=$1`, accountID, batch+1); err != nil {
		t.Fatal(err)
	}
	f.env[internalmail.MagicLinkOutboxEnableEnv] = "0"
	for _, want := range []int{2, 1} {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM magic_link_tokens").Scan(&count); err != nil || count != want {
			t.Fatal("expired binding purge lost its batch bound or live credential", err, count)
		}
	}
	if got := row(t, f, uuidString(live.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("child-off maintenance dispatched a live sign-in")
	}
}

func TestPGMagicLinkOutboxHandoffFencesPasswordChangesAndErasure(t *testing.T) {
	f := magicFixture(t)
	accountID := magicAccount(t, f)
	r := queueMagic(t, f, accountID)
	raw := magicRaw(t, f, r)
	defer clear(raw)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.sender.send = func(ctx context.Context, _ mail.Message, _ internalmail.MessageTiming) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- f.service.DeliverOne(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	changed := make(chan error, 1)
	go func() { changed <- state.NewPgStore(f.pool).SetAccountPassword(ctx, accountID, "after-handoff-phc") }()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(ctx, tx)
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout='100ms'"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", accountID); err == nil {
		t.Fatal("password change crossed established handoff")
	}
	_ = tx.Rollback(ctx)
	unblock()
	for _, ch := range []chan error{done, changed} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := NewMagicLinkCredentials(f.pool).Redeem(ctx, api.HashToken(raw)); !errors.Is(err, ErrMagicLinkInvalid) {
		t.Fatal("delivered link survived password change", err)
	}
	if _, err := f.pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", accountID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM magic_link_tokens)+(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&n); err != nil || n != 0 {
		t.Fatal("account erasure retained magic-link material", err)
	}
}
