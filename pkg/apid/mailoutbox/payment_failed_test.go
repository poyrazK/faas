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
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const paymentMigration = "20261017105351033_platform_mail_payment_failed_outbox.sql"

func paymentFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.PaymentFailedOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.paymentFailed.sender = f.sender
	return f
}

func paymentAccount(t *testing.T, f *fixture, provider string) (string, string) {
	t.Helper()
	id, customer := account(t, f), "customer-"+uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id) VALUES($1,$2,$3)", id, provider, customer); err != nil {
		t.Fatal(err)
	}
	return id, customer
}

func paymentRow(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='payment_failed' ORDER BY created_at DESC LIMIT 1", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func queuePayment(t *testing.T, f *fixture) (string, string, q.PlatformMailProducerOutbox) {
	t.Helper()
	id, customer := paymentAccount(t, f, "stripe")
	if err := f.service.RecordPaymentFailure(t.Context(), id, "stripe", uuid.NewString(), customer, billing.EventPaymentFailed); err != nil {
		t.Fatal(err)
	}
	return id, customer, paymentRow(t, f, id)
}

func TestPaymentFailedDisabledReadsOnlyExactFlag(t *testing.T) {
	var s *Service
	if err := s.ConfigurePaymentFailed(t.Context(), func(k string) string {
		if k != internalmail.PaymentFailedOutboxEnableEnv {
			t.Fatal("disabled configuration read", k)
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPaymentFailure(t.Context(), "invalid", "", "", "", billing.EventPaymentFailed); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("disabled transition performed validation or storage work", err)
	}
}

func TestPGPaymentFailedAtomicReplayAndOriginalDeadline(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		t.Run(provider, func(t *testing.T) {
			f := paymentFixture(t)
			id, customer := paymentAccount(t, f, provider)
			event := uuid.NewString()
			if err := f.service.RecordPaymentFailure(t.Context(), id, provider, event, customer, billing.EventSubscriptionPastDue); err != nil {
				t.Fatal(err)
			}
			r := paymentRow(t, f, id)
			var status string
			var at time.Time
			if err := f.pool.QueryRow(t.Context(), "SELECT status,past_due_at FROM accounts WHERE id=$1", id).Scan(&status, &at); err != nil || status != "past_due" || !at.Equal(r.PaymentPastDueAt.Time) || !r.ExpiresAt.Time.Equal(at.Add(api.InternalMailPaymentFailedTTL)) {
				t.Fatal("status and original deadline were not paired", err)
			}
			binding, plain, err := secretbox.OpenBytes(f.service.paymentFailed.writer, r.SealedSecurityAlert)
			if err != nil || binding != paymentFailedBinding(r) {
				t.Fatal("payload binding", err)
			}
			defer clear(plain)
			var fields map[string]json.RawMessage
			if json.Unmarshal(plain, &fields) != nil || len(fields) != 1 || fields["past_due_at"] == nil || strings.Contains(string(r.SealedSecurityAlert), "one@example.net") {
				t.Fatal("billing intent included more than the original overdue timestamp")
			}
			// Distinct payment.failed and past_due deliveries cannot reset the
			// original clock or enqueue a second warning for this episode.
			for _, kind := range []billing.EventType{billing.EventPaymentFailed, billing.EventSubscriptionPastDue} {
				if err := f.service.RecordPaymentFailure(t.Context(), id, provider, uuid.NewString(), customer, kind); err != nil {
					t.Fatal(err)
				}
			}
			before := paymentRow(t, f, id)
			if !reflect.DeepEqual(before, r) {
				t.Fatal("distinct webhook rewrote notification/deadline")
			}
			f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error {
				return &mail.TransientError{Err: errors.New("private maild unavailable")}
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
			if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "payment-failed:"+uuidString(r.ID.Bytes) || !strings.Contains(f.sender.calls[0].message.TextBody, at.Add(7*24*time.Hour).Format("2006-01-02 15:04:05 UTC")) {
				t.Fatal("retry changed the stable notification or its deadline")
			}
			assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
			// A very old permanent receipt must still suppress re-entry after
			// recovery; it does not use the ingress claim's five-minute TTL.
			if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE billing_payment_failure_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
				t.Fatal(err)
			}
			if err := f.service.RecordPaymentFailure(t.Context(), id, provider, event, customer, billing.EventSubscriptionPastDue); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(t.Context(), "SELECT status FROM accounts WHERE id=$1", id).Scan(&status); err != nil || status != "active" {
				t.Fatal("old webhook reinstated dunning after payment", err)
			}
		})
	}
}

func TestPGPaymentFailedConcurrentFailureKindsAndBindings(t *testing.T) {
	f := paymentFixture(t)
	id, customer := paymentAccount(t, f, "polar")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			kind := billing.EventPaymentFailed
			if i%2 == 0 {
				kind = billing.EventSubscriptionPastDue
			}
			errs <- f.service.RecordPaymentFailure(t.Context(), id, "polar", "same-"+kind.Name(), customer, kind)
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
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox),(SELECT count(*) FROM billing_payment_failure_receipts)").Scan(&intents, &receipts); err != nil || intents != 1 || receipts != 2 {
		t.Fatal("concurrent deliveries duplicated the episode", intents, receipts, err)
	}
	id2, customer2 := paymentAccount(t, f, "polar")
	if err := f.service.RecordPaymentFailure(t.Context(), id2, "polar", "same-payment_failed", customer2, billing.EventPaymentFailed); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("provider receipt crossed accounts", err)
	}
	if err := f.service.RecordPaymentFailure(t.Context(), id2, "polar", "wrong-customer", customer, billing.EventPaymentFailed); !errors.Is(err, ErrAccountUnavailable) {
		t.Fatal("stale customer binding entered dunning", err)
	}
	if err := f.service.RecordPaymentFailure(t.Context(), id2, "polar", "", customer2, billing.EventPaymentFailed); !errors.Is(err, internalmail.ErrInvalid) {
		t.Fatal("unidentified delivery bypassed durable replay protection", err)
	}
	if err := f.service.RecordPaymentFailure(t.Context(), id2, "polar", "same-payment_failed", customer2, billing.EventSubscriptionPastDue); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("receipt accepted a different event type", err)
	}
}

func TestPGPaymentFailedFailuresRollbackStatusIntentAndReceipt(t *testing.T) {
	for _, mode := range []string{"intent", "status", "receipt", "recovery", "quota"} {
		t.Run(mode, func(t *testing.T) {
			f := paymentFixture(t)
			id, customer := paymentAccount(t, f, "stripe")
			var inject string
			switch mode {
			case "intent":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT fail_payment_intent CHECK (template<>'payment_failed')"
			case "status":
				inject = "ALTER TABLE accounts ADD CONSTRAINT fail_payment_status CHECK (status<>'past_due')"
			case "receipt":
				inject = "ALTER TABLE billing_payment_failure_receipts ADD CONSTRAINT fail_payment_receipt CHECK (false)"
			case "recovery":
				inject = "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform','00000000-0000-0000-0000-000000000000',true)"
			case "quota":
				seed := schedule(t, f, account(t, f))
				// Use distinct deletion anchors for the same existing account.
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,deletion_requested_at,restore_until,expires_at,recovery_id)
SELECT gen_random_uuid(),o.account_id,o.namespace,o.producer_id,o.from_address,o.recipient_address,o.template,o.template_version,o.deletion_requested_at-i*interval '1 second',o.restore_until,o.expires_at,o.recovery_id FROM platform_mail_producer_outbox o CROSS JOIN generate_series(1,$2) i WHERE o.id=$1`, seed.ID, api.InternalMailProducerOutboxMaxPending-1); err != nil {
					t.Fatal(err)
				}
			}
			if inject != "" {
				if _, err := f.pool.Exec(t.Context(), inject); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.RecordPaymentFailure(t.Context(), id, "stripe", "retryable", customer, billing.EventPaymentFailed); err == nil {
				t.Fatal("failed paired write was acknowledged")
			}
			var status string
			var stamp bool
			var intents, receipts int
			if err := f.pool.QueryRow(t.Context(), "SELECT status,past_due_at IS NOT NULL,(SELECT count(*) FROM platform_mail_producer_outbox WHERE account_id=$1),(SELECT count(*) FROM billing_payment_failure_receipts WHERE account_id=$1) FROM accounts WHERE id=$1", id).Scan(&status, &stamp, &intents, &receipts); err != nil || status != "active" || stamp || intents != 0 || receipts != 0 {
				t.Fatal("failed write partially committed billing or dedupe", status, stamp, intents, receipts, err)
			}
		})
	}
}

func TestPGPaymentFailedLifecycleAndNewEpisode(t *testing.T) {
	for _, mode := range []string{"recovered", "suspended", "deleted", "address", "new_episode", "suppressed", "off", "expiry", "erasure", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := paymentFixture(t)
			id, customer, r := queuePayment(t, f)
			var err error
			switch mode {
			case "recovered", "new_episode":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1", id)
				if err == nil && mode == "new_episode" {
					err = f.service.RecordPaymentFailure(t.Context(), id, "stripe", uuid.NewString(), customer, billing.EventPaymentFailed)
				}
			case "suspended", "deleted":
				status := "suspended"
				if mode == "deleted" {
					status = "deleted_pending"
				}
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status=$2 WHERE id=$1", id, status)
			case "address":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			case "suppressed":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "off":
				f.env[internalmail.PaymentFailedOutboxEnableEnv] = "0"
			case "expiry":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second',payment_past_due_at=clock_timestamp()-interval '25 hours' WHERE id=$1", r.ID)
			case "erasure":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
			case "corrupt":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", r.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			// Claim the original row explicitly for a deterministic ABA check.
			if mode == "new_episode" {
				claimed, err := f.service.claim(t.Context())
				if err != nil || claimed.ID != r.ID {
					t.Fatal("expected oldest original intent", err)
				}
				if err := f.service.dispatch(t.Context(), claimed); err != nil {
					t.Fatal(err)
				}
			} else if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("obsolete/paused warning reached maild")
			}
			if mode == "erasure" {
				var receipts int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_payment_failure_receipts").Scan(&receipts); err != nil || receipts != 0 {
					t.Fatal("account erasure retained provider receipts", err)
				}
				return
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			want := "cancelled"
			switch mode {
			case "suppressed":
				want = "suppressed"
			case "off":
				want = "queued"
			case "expiry":
				want = "expired"
			case "corrupt":
				want = "failed"
			}
			if got.State != want {
				t.Fatal("wrong lifecycle result", got.State, want)
			}
			if mode == "off" {
				if got.Attempts != 0 {
					t.Fatal("disabled template was claimed")
				}
			} else {
				assertMFAAlertErased(t, got)
			}
		})
	}
}

func TestPGPaymentFailedGateWithdrawalDuringSaveRollsBack(t *testing.T) {
	f := paymentFixture(t)
	id, customer := paymentAccount(t, f, "stripe")
	var on atomic.Bool
	on.Store(true)
	f.service.paymentFailed.enabled = on.Load
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
		done <- f.service.RecordPaymentFailure(ctx, id, "stripe", "withdrawn", customer, billing.EventPaymentFailed)
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late gate withdrawal committed billing", err)
	}
	var status string
	var receipts int
	if err := f.pool.QueryRow(ctx, "SELECT status,(SELECT count(*) FROM billing_payment_failure_receipts) FROM accounts WHERE id=$1", id).Scan(&status, &receipts); err != nil || status != "active" || receipts != 0 {
		t.Fatal("withdrawn transition was partially committed", err)
	}
}
