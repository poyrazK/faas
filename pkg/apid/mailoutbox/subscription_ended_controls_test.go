package mailoutbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGSubscriptionEndedWithdrawalRollsBack(t *testing.T) {
	f := endedFixture(t)
	id, customer, sub := subscriptionAccount(t, f, "stripe")
	var on atomic.Bool
	on.Store(true)
	f.service.subscriptionEnded.enabled = on.Load
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
		_, err := f.service.EndSubscriptionWithConfirmation(ctx, id, "stripe", uuid.NewString(), customer, sub)
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	var plan, binding string
	var receipts int
	if err := f.pool.QueryRow(ctx, "SELECT a.plan,b.subscription_id,(SELECT count(*) FROM billing_subscription_end_receipts) FROM accounts a JOIN billing_identities b ON b.account_id=a.id WHERE a.id=$1", id).Scan(&plan, &binding, &receipts); err != nil || plan != "pro" || binding != sub || receipts != 0 {
		t.Fatal("withdrawal partially ended subscription", plan, binding, receipts, err)
	}
}

func TestPGSubscriptionEndedIdentityWriterLocksAccountFirst(t *testing.T) {
	f := endedFixture(t)
	id, customer, _ := subscriptionAccount(t, f, "stripe")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- state.NewPgStore(f.pool).UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: id, Provider: "stripe", CustomerID: customer, SubscriptionID: "new-sub"})
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	var binding string
	if err := f.pool.QueryRow(ctx, "SELECT subscription_id FROM billing_identities WHERE account_id=$1 FOR UPDATE NOWAIT", id).Scan(&binding); err != nil {
		t.Fatal("identity write locked binding before the account", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPGSubscriptionEndedHistoryPreventsSameBindingReapplication(t *testing.T) {
	f := endedFixture(t)
	id, customer, sub, _ := queueEnded(t, f)
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan='pro',stripe_subscription_item=$2 WHERE id=$1", id, sub); err != nil {
		t.Fatal(err)
	}
	if err := state.NewPgStore(f.pool).UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: id, Provider: "stripe", CustomerID: customer, SubscriptionID: sub}); err != nil {
		t.Fatal(err)
	}
	if result, err := f.service.EndSubscriptionWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer, sub); err != nil || result.Applied {
		t.Fatal("same ended binding applied after a cycle", result, err)
	}
}

func TestPGSubscriptionEndedReadinessCounterAndRollback(t *testing.T) {
	f := endedFixture(t)
	for _, trigger := range []struct{ table, name string }{{"accounts", "accounts_subscription_mail_revision_guard"}, {"billing_identities", "billing_identities_subscription_mail_revision_guard"}} {
		if _, err := f.pool.Exec(t.Context(), "ALTER TABLE "+trigger.table+" DISABLE TRIGGER "+trigger.name); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err == nil {
			t.Fatal("disabled lifecycle trigger accepted", trigger.name)
		}
		if _, err := f.pool.Exec(t.Context(), "ALTER TABLE "+trigger.table+" ENABLE TRIGGER "+trigger.name); err != nil {
			t.Fatal(err)
		}
	}
	up, down := migrationFileParts(t, endedMigration)
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old schema accepted")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	id, _, _, intent := queueEnded(t, f)
	var revision int64
	if err := f.pool.QueryRow(t.Context(), "UPDATE accounts SET subscription_mail_revision=1 WHERE id=$1 RETURNING subscription_mail_revision", id).Scan(&revision); err != nil || revision <= intent.SubscriptionEndedRevision.Int64 {
		t.Fatal("counter rewind revived an old notice", revision, err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded used history")
	}
}

func TestPGSubscriptionEndedReadersReconciliationAndRollback(t *testing.T) {
	f := endedFixture(t)
	id, _, _, first := queueEnded(t, f)
	old, oldPath := f.service, f.env[VerificationIdentityEnv]
	_, path := verificationIdentity(t)
	f.env[VerificationIdentityEnv], f.env[VerificationPreviousIdentityEnv] = path, oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureSubscriptionEnded(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.subscriptionEnded.sender = f.sender
	f.service = next
	_, _, _, second := queueEnded(t, f)
	if err := next.ConfigureSubscriptionEnded(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("missing pending reader accepted")
	}
	for range 2 {
		if err := old.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sender.calls) != 1 || row(t, f, uuidString(second.ID.Bytes)).Attempts != 0 {
		t.Fatal("old worker claimed new-reader notice")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=$1 WHERE id=$2", first.SealedSecurityAlert, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" {
		t.Fatal("cipher crossed intent binding")
	}
	_, _, _, intent := queueEnded(t, f)
	input := reconciliationInput(intent, admitFixture(t, f, intent))
	owner := reconciler(t, f).WithAccountRestored(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling reconciled subscription-ended confirmation", err)
	}
	owner = owner.WithSubscriptionEnded(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); err != nil {
		t.Fatal(err)
	}
	blocked := owner.WithSubscriptionEnded(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1 OR id=$2", id, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Audit(t.Context(), input.RequestID); err != nil {
		t.Fatal("erasure removed opaque audit", err)
	}
	_, down := migrationFileParts(t, endedMigration)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded subscription-ended confirmation history")
	}
}
