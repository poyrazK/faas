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

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func resetFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.PasswordResetOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	f.env[PasswordResetOriginEnv] = "https://dashboard.example.test"
	if err := f.service.ConfigurePasswordReset(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.passwordReset.sender = f.sender
	return f
}

func resetAccount(t *testing.T, f *fixture) string {
	t.Helper()
	id := account(t, f)
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email_verified_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := state.NewPgStore(f.pool).SetAccountPassword(t.Context(), id, "original-phc"); err != nil {
		t.Fatal(err)
	}
	return id
}

func queueReset(t *testing.T, f *fixture, accountID string) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.QueuePasswordReset(t.Context(), accountID, "one@example.net"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='password_reset' ORDER BY created_at DESC LIMIT 1", accountID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return row(t, f, id)
}

func resetRaw(t *testing.T, f *fixture, r q.PlatformMailProducerOutbox) []byte {
	t.Helper()
	raw, err := credentialPayload(f.service.passwordReset, r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPasswordResetOutboxExactGateAndIndependentConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.PasswordResetOutboxEnableEnv {
				t.Fatal("disabled reset read private configuration", k)
			}
			return flag
		}
		var s *Service
		if err := s.ConfigurePasswordReset(t.Context(), getenv); err != nil || internalmail.PasswordResetOutboxEnabled(getenv) {
			t.Fatal("nonexact reset gate enabled", err)
		}
		if err := s.QueuePasswordReset(t.Context(), "invalid", "invalid"); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
	var disabled *Service
	if err := disabled.ConfigurePasswordReset(t.Context(), func(string) string { return "1" }); err == nil {
		t.Fatal("reset enabled without configured parent")
	}
	f := resetFixture(t)
	r := queueReset(t, f, resetAccount(t, f))
	f.env[internalmail.PasswordResetOutboxEnableEnv] = "0"
	f.env[internalmail.VerificationOutboxEnableEnv] = "1"
	f.env[VerificationOriginEnv] = f.env[PasswordResetOriginEnv]
	if err := f.service.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := row(t, f, uuidString(r.ID.Bytes))
	if after.State != "queued" || after.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("verification gate claimed a disabled reset credential")
	}
}

func TestPGPasswordResetOutboxAtomicIssuanceAndLateDisable(t *testing.T) {
	for _, table := range []string{"password_reset_tokens", "platform_mail_producer_outbox"} {
		t.Run(table, func(t *testing.T) {
			f := resetFixture(t)
			id := resetAccount(t, f)
			constraint := "ALTER TABLE password_reset_tokens ADD CONSTRAINT injected_reset_failure CHECK (password_revision<0)"
			if table == "platform_mail_producer_outbox" {
				constraint = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_reset_failure CHECK (template<>'password_reset')"
			}
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if err := f.service.QueuePasswordReset(t.Context(), id, "one@example.net"); err == nil {
				t.Fatal("injected failure committed reset")
			}
			var tokens, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM password_reset_tokens),(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&tokens, &intents); err != nil || tokens != 0 || intents != 0 {
				t.Fatal("reset token and intent did not roll back together", err)
			}
		})
	}
	f := resetFixture(t)
	id := resetAccount(t, f)
	var on atomic.Bool
	on.Store(true)
	f.service.passwordReset.enabled = on.Load
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
	go func() { done <- f.service.QueuePasswordReset(ctx, id, "one@example.net") }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late-disabled reset committed", err)
	}
}

func TestPGPasswordResetOutboxStableRetryPurposeAndAtomicRedemption(t *testing.T) {
	f := resetFixture(t)
	accountID := resetAccount(t, f)
	r := queueReset(t, f, accountID)
	id := uuidString(r.ID.Bytes)
	raw := resetRaw(t, f, r)
	defer clear(raw)
	if bytes.Contains(r.SealedVerification, raw) || !bytes.Equal(api.HashToken(raw), r.VerificationTokenHash) {
		t.Fatal("reset credential is not encrypted and bound")
	}
	var expires, created time.Time
	var revision int64
	if err := f.pool.QueryRow(t.Context(), "SELECT expires_at,created_at,password_revision FROM password_reset_tokens WHERE token_hash=$1", api.HashToken(raw)).Scan(&expires, &created, &revision); err != nil || !expires.Equal(r.ExpiresAt.Time) || expires.Sub(created) > api.PasswordResetTTL || expires.Sub(created) < api.PasswordResetTTL-time.Second {
		t.Fatal("reset lost original fifteen-minute deadline", err)
	}
	if _, err := state.NewPgStore(f.pool).ConsumeLoginToken(t.Context(), api.HashToken(raw)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("reset token authenticated as a login", err)
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
	if err := restarted.ConfigurePasswordReset(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.passwordReset.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) {
		t.Fatal("retry changed reset payload or deadline")
	}
	if !strings.Contains(f.sender.calls[0].message.TextBody, "https://dashboard.example.test/auth/reset?token="+base64.RawURLEncoding.EncodeToString(raw)) || f.sender.calls[0].message.MessageID != "password-reset:"+id {
		t.Fatal("reset link or stable admission key changed")
	}
	assertVerificationErased(t, row(t, f, id))
	// Rollback needs no key or origin to enforce a link already in a mailbox.
	f.env[internalmail.PasswordResetOutboxEnableEnv] = "0"
	credentials := NewResetCredentials(f.pool)
	if got, err := credentials.Redeem(t.Context(), api.HashToken(raw), "replacement-phc"); err != nil || got != accountID {
		t.Fatal("saved reset failed after flag rollback", err)
	}
	if _, err := credentials.Redeem(t.Context(), api.HashToken(raw), "replay-phc"); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatal("reset replay changed a password", err)
	}
	if phc, err := state.NewPgStore(f.pool).AccountPasswordByAccountID(t.Context(), accountID); err != nil || phc != "replacement-phc" {
		t.Fatal("reset password did not commit", err)
	}
}

func TestPGPasswordResetOutboxStopsStaleOrSuppressedCredentials(t *testing.T) {
	for _, scenario := range []string{"password_changed", "password_deleted_and_restored", "address_changed", "unverified", "inactive", "used", "expired", "suppressed", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			f := resetFixture(t)
			accountID := resetAccount(t, f)
			r := queueReset(t, f, accountID)
			raw := resetRaw(t, f, r)
			defer clear(raw)
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
			case "unverified":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email_verified_at=NULL WHERE id=$1", accountID)
			case "inactive":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending' WHERE id=$1", accountID)
			case "used":
				_, err = NewResetCredentials(f.pool).Redeem(t.Context(), api.HashToken(raw), "used-phc")
			case "expired":
				_, err = f.pool.Exec(t.Context(), "UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute'")
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
				t.Fatal("stale reset reached handoff", after.State)
			}
			assertVerificationErased(t, after)
			if scenario != "suppressed" && scenario != "corrupt" {
				if _, err := NewResetCredentials(f.pool).Redeem(t.Context(), api.HashToken(raw), "unexpected-phc"); err == nil {
					t.Fatal("stale reset remained redeemable")
				}
			}
		})
	}
}

func TestPGPasswordResetRedemptionRollbackAndSiblingRace(t *testing.T) {
	f := resetFixture(t)
	accountID := resetAccount(t, f)
	r := queueReset(t, f, accountID)
	raw := resetRaw(t, f, r)
	defer clear(raw)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE account_passwords ADD CONSTRAINT injected_reset_password_failure CHECK (hash<>'rejected-phc')"); err != nil {
		t.Fatal(err)
	}
	credentials := NewResetCredentials(f.pool)
	if _, err := credentials.Redeem(t.Context(), api.HashToken(raw), "rejected-phc"); err == nil {
		t.Fatal("failed password write committed reset")
	}
	var consumed bool
	if err := f.pool.QueryRow(t.Context(), "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1", api.HashToken(raw)).Scan(&consumed); err != nil || consumed {
		t.Fatal("failed reset burned its token", err)
	}
	second := queueReset(t, f, accountID)
	raw2 := resetRaw(t, f, second)
	defer clear(raw2)
	var wg sync.WaitGroup
	done := make(chan error, 2)
	for _, token := range [][]byte{raw, raw2} {
		wg.Go(func() { _, err := credentials.Redeem(t.Context(), api.HashToken(token), "winner-phc"); done <- err })
	}
	wg.Wait()
	close(done)
	wins := 0
	for err := range done {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrPasswordResetInvalid) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("sibling reset links both changed the credential", wins)
	}
}

func TestPGPasswordResetExpiryDuringPasswordWaitRollsBack(t *testing.T) {
	f := resetFixture(t)
	accountID := resetAccount(t, f)
	r := queueReset(t, f, accountID)
	raw := resetRaw(t, f, r)
	defer clear(raw)
	deadline := time.Now().Add(350 * time.Millisecond)
	if _, err := f.pool.Exec(t.Context(), "UPDATE password_reset_tokens SET expires_at=$1 WHERE token_hash=$2", deadline, api.HashToken(raw)); err != nil {
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
	go func() { _, err := NewResetCredentials(f.pool).Redeem(ctx, api.HashToken(raw), "late-phc"); done <- err }()
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
	if err := <-done; !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatal("expired reset crossed password wait", err)
	}
	if phc, err := state.NewPgStore(f.pool).AccountPasswordByAccountID(t.Context(), accountID); err != nil || phc != "original-phc" {
		t.Fatal("expired reset committed password", err)
	}
}

func TestPGPasswordResetOutboxHandoffFencesPasswordChangesAndErasure(t *testing.T) {
	f := resetFixture(t)
	accountID := resetAccount(t, f)
	r := queueReset(t, f, accountID)
	raw := resetRaw(t, f, r)
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
	if _, err := NewResetCredentials(f.pool).Redeem(ctx, api.HashToken(raw), "invalid-phc"); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatal("delivered link survived password change", err)
	}
	if _, err := f.pool.Exec(ctx, "DELETE FROM accounts WHERE id=$1", accountID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM password_reset_tokens)+(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&n); err != nil || n != 0 {
		t.Fatal("account erasure retained reset material", err)
	}
}
