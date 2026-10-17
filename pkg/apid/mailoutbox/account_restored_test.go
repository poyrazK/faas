package mailoutbox

import (
	"context"
	"encoding/json"
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
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const restoredMigration = "20261017112923185_platform_mail_account_restored_outbox.sql"

func restoredFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.AccountRestoredOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.accountRestored.sender = f.sender
	return f
}

func overdueAccount(t *testing.T, f *fixture, provider string) (string, string) {
	t.Helper()
	id, customer := paymentAccount(t, f, provider)
	if err := state.NewPgStore(f.pool).MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	return id, customer
}

func restoredRow(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='account_restored' ORDER BY created_at DESC LIMIT 1", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func queueRestored(t *testing.T, f *fixture) (string, string, q.PlatformMailProducerOutbox) {
	t.Helper()
	id, customer := overdueAccount(t, f, "stripe")
	if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer); err != nil || !restored {
		t.Fatal(restored, err)
	}
	return id, customer, restoredRow(t, f, id)
}

func TestAccountRestoredDisabledReadsOnlyOwnGate(t *testing.T) {
	var s *Service
	if err := s.ConfigureAccountRestored(t.Context(), func(key string) string {
		if key != internalmail.AccountRestoredOutboxEnableEnv {
			t.Fatal("disabled configuration read", key)
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestorePaymentWithConfirmation(t.Context(), "invalid", "", "", ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("disabled recovery performed validation or storage work", err)
	}
}

func TestPGAccountRestoredOriginalEventAndPermanentReplay(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		t.Run(provider, func(t *testing.T) {
			f := restoredFixture(t)
			id, customer := overdueAccount(t, f, provider)
			event := uuid.NewString()
			if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, provider, event, customer); err != nil || !restored {
				t.Fatal(restored, err)
			}
			r := restoredRow(t, f, id)
			binding, plain, err := secretbox.OpenBytes(f.service.accountRestored.writer, r.SealedSecurityAlert)
			if err != nil || binding != accountRestoredBinding(r) {
				t.Fatal(err)
			}
			defer clear(plain)
			var fields map[string]json.RawMessage
			if json.Unmarshal(plain, &fields) != nil || len(fields) != 1 || fields["restored_at"] == nil {
				t.Fatal("confirmation contained more than its original recovery time")
			}
			payload, err := accountRestoredPayloadFor(f.service.accountRestored, r)
			if err != nil || !payload.RestoredAt.Add(api.InternalMailAccountRestoredTTL).Equal(r.ExpiresAt.Time) {
				t.Fatal("delivery deadline not anchored to recovery", err)
			}
			var current int64
			if err := f.pool.QueryRow(t.Context(), "SELECT billing_mail_revision FROM accounts WHERE id=$1", id).Scan(&current); err != nil || current != r.AccountRestoredRevision.Int64 {
				t.Fatal("confirmation was not bound to committed restoration", err)
			}
			// Both the same delivery and a distinct success while active are
			// silent. Recording the latter also prevents future replay recovery.
			other := uuid.NewString()
			for _, next := range []string{event, other, event} {
				if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, provider, next, customer); err != nil || restored {
					t.Fatal("duplicate/no-op success restored again", restored, err)
				}
			}
			if !reflect.DeepEqual(r, restoredRow(t, f, id)) {
				t.Fatal("replay rewrote confirmation or its expiry")
			}
			f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error {
				return &mail.TransientError{Status: 503}
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.sender.send = nil
			if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "account-restored:"+uuidString(r.ID.Bytes) || !strings.Contains(f.sender.calls[0].message.TextBody, payload.RestoredAt.Format("2006-01-02 15:04:05 UTC")) {
				t.Fatal("retry changed recovery content, key or expiry")
			}
			assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
			store := state.NewPgStore(f.pool)
			if err := store.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE billing_payment_recovery_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
				t.Fatal(err)
			}
			for _, replay := range []string{event, other} {
				if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, provider, replay, customer); err != nil || restored {
					t.Fatal("permanent replay restored a later overdue period", err)
				}
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), "SELECT status FROM accounts WHERE id=$1", id).Scan(&status); err != nil || status != "past_due" {
				t.Fatal("success replay changed later dunning", err)
			}
		})
	}
}

func TestPGAccountRestoredConcurrentSuccessesAndBindings(t *testing.T) {
	f := restoredFixture(t)
	id, customer := overdueAccount(t, f, "polar")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	var count atomic.Int32
	for i := range 12 {
		wg.Go(func() {
			event := "same-success"
			if i%2 == 0 {
				event = "other-success"
			}
			restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, "polar", event, customer)
			if restored {
				count.Add(1)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var intents, receipts int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox),(SELECT count(*) FROM billing_payment_recovery_receipts)").Scan(&intents, &receipts); err != nil || count.Load() != 1 || intents != 1 || receipts != 2 {
		t.Fatal("overlapping successes duplicated restoration", count.Load(), intents, receipts, err)
	}
	id2, customer2 := overdueAccount(t, f, "polar")
	if _, err := f.service.RestorePaymentWithConfirmation(t.Context(), id2, "polar", "same-success", customer2); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("receipt crossed accounts", err)
	}
	if _, err := f.service.RestorePaymentWithConfirmation(t.Context(), id2, "polar", "wrong-binding", customer); !errors.Is(err, ErrAccountUnavailable) {
		t.Fatal("customer binding crossed accounts", err)
	}
	if _, err := f.service.RestorePaymentWithConfirmation(t.Context(), id2, "polar", "", customer2); !errors.Is(err, internalmail.ErrInvalid) {
		t.Fatal("empty delivery ID bypassed replay protection", err)
	}
}

func TestPGAccountRestoredLifecycleRevisionSurvivesABAAndRollback(t *testing.T) {
	for _, mode := range []string{"overdue", "status_aba", "address_aba", "deletion_aba", "unrelated_security", "off", "suppressed", "expiry", "erasure", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := restoredFixture(t)
			id, _, r := queueRestored(t, f)
			store := state.NewPgStore(f.pool)
			var err error
			switch mode {
			case "overdue", "status_aba":
				// These are the existing legacy/meter writers, with no new gate.
				f.env[internalmail.AccountRestoredOutboxEnableEnv] = "0"
				err = store.MarkDunningStep(t.Context(), id, state.AccountActive, state.AccountPastDue)
				if err == nil && mode == "status_aba" {
					err = store.UpdateAccountStatus(t.Context(), id, state.AccountActive)
				}
				f.env[internalmail.AccountRestoredOutboxEnableEnv] = "1"
			case "address_aba":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
				if err == nil {
					_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='one@example.net' WHERE id=$1", id)
				}
			case "deletion_aba":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1", id)
				if err == nil {
					_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=NULL WHERE id=$1", id)
				}
			case "unrelated_security":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET password_reset_revision=password_reset_revision+1,mfa_revision=mfa_revision+1 WHERE id=$1", id)
			case "off":
				f.env[internalmail.AccountRestoredOutboxEnableEnv] = "0"
			case "suppressed":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "expiry":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID)
			case "erasure":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
			case "corrupt":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", r.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated_security" {
				if len(f.sender.calls) != 1 {
					t.Fatal("unrelated security change invalidated billing recovery")
				}
				return
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("obsolete or disabled confirmation reached maild")
			}
			if mode == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_payment_recovery_receipts").Scan(&n); err != nil || n != 0 {
					t.Fatal("erasure retained recovery receipts", err)
				}
				return
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			want := "cancelled"
			switch mode {
			case "off":
				want = "queued"
			case "expiry":
				want = "expired"
			case "suppressed":
				want = "suppressed"
			case "corrupt":
				want = "failed"
			}
			if got.State != want {
				t.Fatal("wrong lifecycle result", got.State, want)
			}
			if mode == "off" {
				if got.Attempts != 0 {
					t.Fatal("disabled child was claimed")
				}
			} else {
				assertMFAAlertErased(t, got)
			}
		})
	}
}

func TestPGAccountRestoredFlagWithdrawalDuringSaveRollsBack(t *testing.T) {
	f := restoredFixture(t)
	id, customer := overdueAccount(t, f, "stripe")
	var on atomic.Bool
	on.Store(true)
	f.service.accountRestored.enabled = on.Load
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "LOCK TABLE platform_mail_producer_outbox IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.service.RestorePaymentWithConfirmation(ctx, id, "stripe", "withdrawn", customer)
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("withdrawn capability committed recovery", err)
	}
	var status string
	var revision int64
	var receipts int
	if err := f.pool.QueryRow(ctx, "SELECT status,billing_mail_revision,(SELECT count(*) FROM billing_payment_recovery_receipts) FROM accounts WHERE id=$1", id).Scan(&status, &revision, &receipts); err != nil || status != "past_due" || revision != 2 || receipts != 0 {
		t.Fatal("withdrawn recovery partially committed status, revision or receipt", err)
	}
}
