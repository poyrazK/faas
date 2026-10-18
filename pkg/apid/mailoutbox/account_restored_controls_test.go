package mailoutbox

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGAccountRestoredAtomicFailureBoundaries(t *testing.T) {
	for _, mode := range []string{"intent", "status", "org", "receipt", "cancel", "quota", "held"} {
		t.Run(mode, func(t *testing.T) {
			f := paymentFixture(t)
			f.env[internalmail.AccountRestoredOutboxEnableEnv] = "1"
			if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
				t.Fatal(err)
			}
			id, customer, failure := queuePayment(t, f)
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO orgs(id,personal_org,personal_owner_account_id,status) VALUES($1,true,$2,'suspended')", uuid.NewString(), id); err != nil {
				t.Fatal(err)
			}
			var inject string
			switch mode {
			case "intent":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT fail_restored_intent CHECK(template<>'account_restored')"
			case "status":
				inject = "ALTER TABLE accounts ADD CONSTRAINT fail_restored_status CHECK(status<>'active')"
			case "org":
				inject = "ALTER TABLE orgs ADD CONSTRAINT fail_restored_org CHECK(status<>'active')"
			case "receipt":
				inject = "ALTER TABLE billing_payment_recovery_receipts ADD CONSTRAINT fail_restored_receipt CHECK(false)"
			case "cancel":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT fail_restored_cancel CHECK(state<>'cancelled')"
			case "held":
				inject = "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform','00000000-0000-0000-0000-000000000000',true)"
			case "quota":
				// The failed warning for this account will free one slot. Fill
				// the remaining limit with other accounts' deletion intents.
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
			if _, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, "stripe", "retryable", customer); err == nil {
				t.Fatal("failed paired recovery was acknowledged")
			}
			var status, org string
			var revision int64
			var receipts, confirmations int
			if err := f.pool.QueryRow(t.Context(), "SELECT a.status,a.billing_mail_revision,g.status,(SELECT count(*) FROM billing_payment_recovery_receipts),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='account_restored') FROM accounts a JOIN orgs g ON g.personal_owner_account_id=a.id WHERE a.id=$1", id).Scan(&status, &revision, &org, &receipts, &confirmations); err != nil || status != "past_due" || revision != 2 || org != "suspended" || receipts != 0 || confirmations != 0 || row(t, f, uuidString(failure.ID.Bytes)).State != "queued" {
				t.Fatal("failed recovery partially committed account, org, cancellation or receipt", status, revision, org, receipts, confirmations, err)
			}
		})
	}
}

func TestPGAccountRestoredKeysBindingAndReconciliation(t *testing.T) {
	f := restoredFixture(t)
	id, _, first := queueRestored(t, f)
	old, oldPath := f.service, f.env[VerificationIdentityEnv]
	_, path := verificationIdentity(t)
	f.env[VerificationIdentityEnv], f.env[VerificationPreviousIdentityEnv] = path, oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.accountRestored.sender = f.sender
	f.service = next
	_, _, second := queueRestored(t, f)
	if err := next.ConfigureAccountRestored(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("startup ignored pending historical reader")
	}
	for range 2 {
		if err := old.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 1 {
		t.Fatal("old worker claimed new-reader confirmation")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=$1 WHERE id=$2", first.SealedSecurityAlert, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" || len(f.sender.calls) != 1 {
		t.Fatal("cipher crossed account/intent/revision/key binding")
	}
	// Acknowledgement operates only on an exact existing admission, with its
	// own capability and an opaque audit that survives account erasure.
	_, _, intent := queueRestored(t, f)
	messageID := admitFixture(t, f, intent)
	input := reconciliationInput(intent, messageID)
	owner := reconciler(t, f).WithPaymentFailed(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged recovery", err)
	}
	owner = owner.WithAccountRestored(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); err != nil || record.Template != AccountRestoredTemplate {
		t.Fatal(err)
	}
	blocked := owner.WithAccountRestored(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact receipt replay bypassed its gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed its gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1 OR id=$2", id, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != AccountRestoredTemplate {
		t.Fatal("erasure lost opaque audit", err)
	}
	_, down := migrationFileParts(t, restoredMigration)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded used recovery history")
	}
}

func TestPGAccountRestoredReadinessAndReceiptOnlyRollbackGuard(t *testing.T) {
	f := restoredFixture(t)
	up, down := migrationFileParts(t, restoredMigration)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE accounts DISABLE TRIGGER accounts_billing_mail_revision_guard"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("disabled lifecycle fence passed enabled startup")
	}
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old schema passed enabled startup")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	id, customer := paymentAccount(t, f, "stripe")
	if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, "stripe", uuid.NewString(), customer); err != nil || restored {
		t.Fatal("active account was backfilled", restored, err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded a no-op recovery receipt")
	}
}
