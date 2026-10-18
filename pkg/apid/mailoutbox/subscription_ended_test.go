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
)

const endedMigration = "20261017142138994_platform_mail_subscription_ended_outbox.sql"

func endedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.SubscriptionEndedOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.subscriptionEnded.sender = f.sender
	return f
}

func subscriptionAccount(t *testing.T, f *fixture, provider string) (id, customer, sub string) {
	t.Helper()
	id, customer, sub = account(t, f), "customer-"+uuid.NewString(), "sub-"+uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan='pro',provider_customer_id=$2,stripe_subscription_item=$3 WHERE id=$1", id, customer, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id,subscription_id) VALUES($1,$2,$3,$4)", id, provider, customer, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO orgs(id,personal_org,personal_owner_account_id,plan,provider_customer_id,stripe_subscription_item) VALUES($1,true,$2,'pro',$3,$4)", uuid.NewString(), id, customer, sub); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO apps(id,account_id,streaming_enabled) VALUES($1,$2,true)", uuid.NewString(), id); err != nil {
		t.Fatal(err)
	}
	return
}

func queueEnded(t *testing.T, f *fixture) (string, string, string, q.PlatformMailProducerOutbox) {
	t.Helper()
	id, customer, sub := subscriptionAccount(t, f, "stripe")
	result, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer, sub)
	if err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='subscription_ended'", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return id, customer, sub, row(t, f, intent)
}

func TestSubscriptionEndedDisabledReadsOnlyOwnGate(t *testing.T) {
	var s *Service
	for _, value := range []string{"", "0", "true", " 1", "1 "} {
		if err := s.ConfigureSubscriptionEnded(t.Context(), func(k string) string {
			if k != internalmail.SubscriptionEndedOutboxEnableEnv {
				t.Fatal("disabled configuration read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EndSubscriptionWithConfirmation(t.Context(), "", "", "", "", ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGSubscriptionEndedAtomicStateSnapshotAndReplay(t *testing.T) {
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		t.Run(provider, func(t *testing.T) {
			f := endedFixture(t)
			id, customer, sub := subscriptionAccount(t, f, provider)
			conn, err := f.pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			if _, err := conn.Exec(t.Context(), "LISTEN app_changed"); err != nil {
				t.Fatal(err)
			}
			event := uuid.NewString()
			result, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, provider, event, customer, sub)
			if err != nil || !result.Applied || result.FromPlan != "pro" || result.Status != "active" {
				t.Fatal(result, err)
			}
			var plan, binding, cache, orgPlan, orgSub string
			var streaming bool
			if err := f.pool.QueryRow(t.Context(), `SELECT a.plan,b.subscription_id,a.stripe_subscription_item,g.plan,g.stripe_subscription_item,p.streaming_enabled
FROM accounts a JOIN billing_identities b ON b.account_id=a.id JOIN orgs g ON g.personal_owner_account_id=a.id JOIN apps p ON p.account_id=a.id WHERE a.id=$1`, id).Scan(&plan, &binding, &cache, &orgPlan, &orgSub, &streaming); err != nil || plan != "free" || binding != "" || cache != "" || orgPlan != "free" || orgSub != "" || streaming {
				t.Fatal("partial subscription termination", plan, binding, cache, orgPlan, orgSub, streaming, err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			notification, err := conn.Conn().WaitForNotification(ctx)
			var payload map[string]string
			if err != nil || json.Unmarshal([]byte(notification.Payload), &payload) != nil || payload["account_id"] != id || payload["kind"] != "account_plan_changed" {
				t.Fatal("plan invalidation lost", err)
			}
			var intent string
			if err := f.pool.QueryRow(t.Context(), "SELECT id FROM platform_mail_producer_outbox WHERE account_id=$1", id).Scan(&intent); err != nil {
				t.Fatal(err)
			}
			r := row(t, f, intent)
			frozen, err := subscriptionEndedPayloadFor(f.service.subscriptionEnded, r)
			if err != nil || frozen.FromPlan != "pro" || !frozen.EndedAt.Equal(result.EndedAt) || !r.ExpiresAt.Time.Equal(result.EndedAt.Add(24*time.Hour)) {
				t.Fatal(frozen, err)
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE billing_subscription_end_receipts SET processed_at=clock_timestamp()-interval '30 days'"); err != nil {
				t.Fatal(err)
			}
			for _, delivery := range []string{event, uuid.NewString()} {
				if again, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, provider, delivery, customer, sub); err != nil || again.Applied {
					t.Fatal("replay changed account", again, err)
				}
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan='scale',stripe_subscription_item='new-sub' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE billing_identities SET subscription_id='new-sub' WHERE account_id=$1", id); err != nil {
				t.Fatal(err)
			}
			// A delivery replay and a distinct late event for the old binding
			// cannot rewind a current subscription.
			for _, delivery := range []string{event, uuid.NewString()} {
				if again, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, provider, delivery, customer, sub); err != nil || again.Applied {
					t.Fatal(again, err)
				}
			}
			var receipts, ends, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT plan,(SELECT count(*) FROM billing_subscription_end_receipts),(SELECT count(*) FROM billing_subscription_ends),(SELECT count(*) FROM platform_mail_producer_outbox) FROM accounts WHERE id=$1", id).Scan(&plan, &receipts, &ends, &intents); err != nil || plan != "scale" || receipts != 3 || ends != 1 || intents != 1 {
				t.Fatal("permanent replay proof lost", plan, receipts, ends, intents, err)
			}
			if next, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, provider, uuid.NewString(), customer, "new-sub"); err != nil || !next.Applied || next.FromPlan != "scale" {
				t.Fatal("new subscription could not end", next, err)
			}
		})
	}
}

func TestPGSubscriptionEndedEligibilityAndBindingGuards(t *testing.T) {
	for _, mode := range []string{"past_due", "suspended", "deleted_pending", "overdue_active", "deleting_active", "free", "superseded", "missing_binding", "wrong_provider", "wrong_customer", "missing_id"} {
		t.Run(mode, func(t *testing.T) {
			f := endedFixture(t)
			id, customer, sub := subscriptionAccount(t, f, "stripe")
			status, plan := "active", "pro"
			switch mode {
			case "past_due", "suspended", "deleted_pending":
				status = mode
			case "free":
				plan = "free"
			case "superseded":
				sub = "old-sub"
			case "wrong_provider":
				if _, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "paddle", uuid.NewString(), customer, sub); !errors.Is(err, ErrAccountUnavailable) {
					t.Fatal(err)
				}
				return
			case "wrong_customer":
				customer = "wrong-customer"
			case "missing_id":
				sub = ""
			case "missing_binding":
				if _, err := f.pool.Exec(t.Context(), "UPDATE billing_identities SET subscription_id='' WHERE account_id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan=$2,status=$3,past_due_at=CASE WHEN $4 THEN clock_timestamp() ELSE NULL END,deletion_requested_at=CASE WHEN $5 THEN clock_timestamp() ELSE NULL END WHERE id=$1", id, plan, status, mode == "past_due" || mode == "suspended" || mode == "deleted_pending" || mode == "overdue_active", mode == "deleted_pending" || mode == "deleting_active"); err != nil {
				t.Fatal(err)
			}
			result, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer, sub)
			if mode == "wrong_customer" || mode == "missing_binding" || mode == "missing_id" {
				if err == nil {
					t.Fatal("unbound command acknowledged")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var gotStatus, gotPlan string
			var intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT status,plan,(SELECT count(*) FROM platform_mail_producer_outbox) FROM accounts WHERE id=$1", id).Scan(&gotStatus, &gotPlan, &intents); err != nil || gotStatus != status || intents != 0 {
				t.Fatal("dunning received voluntary notice or changed status", gotStatus, gotPlan, intents, err)
			}
			wantPlan := "free"
			if mode == "superseded" {
				wantPlan = "pro"
			}
			if gotPlan != wantPlan || result.Applied != (mode != "superseded") {
				t.Fatal("incorrect stale-binding/Free behavior", gotPlan, result)
			}
		})
	}
}

func TestPGSubscriptionEndedConcurrentDeliveries(t *testing.T) {
	f := endedFixture(t)
	id, customer, sub := subscriptionAccount(t, f, "stripe")
	var applied atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			result, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer, sub)
			if err != nil {
				t.Error(err)
			}
			if result.Applied {
				applied.Add(1)
			}
		})
	}
	wg.Wait()
	var receipts, ends, intents int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM billing_subscription_end_receipts),(SELECT count(*) FROM billing_subscription_ends),(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&receipts, &ends, &intents); err != nil || receipts != 12 || ends != 1 || intents != 1 || applied.Load() != 1 {
		t.Fatal(receipts, ends, intents, applied.Load(), err)
	}
}

func TestPGSubscriptionEndedAtomicRollback(t *testing.T) {
	for _, mode := range []string{"intent", "plan", "binding", "org", "streaming", "receipt", "business_receipt", "held", "quota"} {
		t.Run(mode, func(t *testing.T) {
			f := endedFixture(t)
			id, customer, sub := subscriptionAccount(t, f, "stripe")
			inject := ""
			switch mode {
			case "intent":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_end_intent CHECK(template<>'subscription_ended')"
			case "plan":
				inject = "ALTER TABLE accounts ADD CONSTRAINT reject_end_plan CHECK(plan<>'free')"
			case "binding":
				inject = "ALTER TABLE billing_identities ADD CONSTRAINT reject_end_binding CHECK(subscription_id<>'')"
			case "org":
				inject = "ALTER TABLE orgs ADD CONSTRAINT reject_end_org CHECK(plan<>'free')"
			case "streaming":
				inject = "ALTER TABLE apps ADD CONSTRAINT reject_end_streaming CHECK(streaming_enabled)"
			case "receipt":
				inject = "ALTER TABLE billing_subscription_end_receipts ADD CONSTRAINT reject_end_receipt CHECK(false)"
			case "business_receipt":
				inject = "ALTER TABLE billing_subscription_ends ADD CONSTRAINT reject_end_business CHECK(false)"
			case "held":
				inject = "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform','00000000-0000-0000-0000-000000000000',true)"
			case "quota":
				seed := schedule(t, f, account(t, f))
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
			event := uuid.NewString()
			if _, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "stripe", event, customer, sub); err == nil {
				t.Fatal("failed paired write acknowledged")
			}
			var plan, binding, orgPlan string
			var streaming bool
			var receipts, intents int
			if err := f.pool.QueryRow(t.Context(), `SELECT a.plan,b.subscription_id,g.plan,p.streaming_enabled,(SELECT count(*) FROM billing_subscription_end_receipts),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='subscription_ended') FROM accounts a JOIN billing_identities b ON b.account_id=a.id JOIN orgs g ON g.personal_owner_account_id=a.id JOIN apps p ON p.account_id=a.id WHERE a.id=$1`, id).Scan(&plan, &binding, &orgPlan, &streaming, &receipts, &intents); err != nil || plan != "pro" || binding != sub || orgPlan != "pro" || !streaming || receipts != 0 || intents != 0 {
				t.Fatal("partial termination committed", plan, binding, orgPlan, streaming, receipts, intents, err)
			}
		})
	}
}

func TestPGSubscriptionEndedFrozenRetryAndLifecycle(t *testing.T) {
	for _, mode := range []string{"retry", "plan_aba", "address_aba", "binding_aba", "identity_aba", "identity_reinsert", "other_provider", "status_aba", "delete", "erasure", "off", "suppressed", "unrelated", "corrupt", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			f := endedFixture(t)
			id, customer, _, intent := queueEnded(t, f)
			// Lifecycle protection also applies to changes while issuance is off.
			f.env[internalmail.SubscriptionEndedOutboxEnableEnv] = "0"
			var mutation string
			switch mode {
			case "plan_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan='pro' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET plan='free' WHERE id=$1"
			case "address_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET email='one@example.net' WHERE id=$1"
			case "binding_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET stripe_subscription_item='other' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET stripe_subscription_item='' WHERE id=$1"
			case "identity_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE billing_identities SET subscription_id='other' WHERE account_id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE billing_identities SET subscription_id='' WHERE account_id=$1"
			case "identity_reinsert":
				if _, err := f.pool.Exec(t.Context(), "DELETE FROM billing_identities WHERE account_id=$1", id); err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id) VALUES($1,'stripe',$2)", id, customer); err != nil {
					t.Fatal(err)
				}
			case "other_provider":
				mutation = "INSERT INTO billing_identities(account_id,provider,customer_id,subscription_id) VALUES($1,'polar','new-customer','new-sub')"
			case "status_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET status='active' WHERE id=$1"
			case "delete":
				mutation = "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1"
			case "erasure":
				mutation = "DELETE FROM accounts WHERE id=$1"
			case "unrelated":
				mutation = "UPDATE accounts SET password_reset_revision=password_reset_revision+1,mfa_revision=mfa_revision+1,last_quota_warning_at=clock_timestamp() WHERE id=$1"
			case "suppressed":
				mutation = "INSERT INTO mail_suppressions(email) SELECT email FROM accounts WHERE id=$1"
			case "corrupt":
				if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", intent.ID); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", intent.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "" {
				if _, err := f.pool.Exec(t.Context(), mutation, id); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "off" {
				f.env[internalmail.SubscriptionEndedOutboxEnableEnv] = "1"
			}
			if mode == "retry" {
				f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "retry" {
				f.sender.send = nil
				if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp()-interval '1 second'"); err != nil {
					t.Fatal(err)
				}
				if err := f.service.DeliverOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || !strings.Contains(f.sender.calls[0].message.TextBody, "pro") || f.sender.calls[0].message.MessageID != "subscription-ended:"+uuidString(intent.ID.Bytes) {
					t.Fatal("retry rewrote frozen termination notice")
				}
				return
			}
			if mode == "unrelated" {
				if len(f.sender.calls) != 1 {
					t.Fatal("unrelated security/quota change cancelled notice")
				}
				return
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("stale notice sent", mode)
			}
			if mode == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_subscription_end_receipts").Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				return
			}
			got := row(t, f, uuidString(intent.ID.Bytes))
			want := "cancelled"
			switch mode {
			case "off":
				want = "queued"
			case "suppressed":
				want = "suppressed"
			case "corrupt":
				want = "failed"
			case "expiry":
				want = "expired"
			}
			if got.State != want {
				t.Fatal(got.State, want)
			}
			if mode != "off" {
				assertMFAAlertErased(t, got)
			}
		})
	}
}
