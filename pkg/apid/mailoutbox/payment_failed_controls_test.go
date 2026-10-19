package mailoutbox

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGPaymentFailedKeysCipherBindingAndReadiness(t *testing.T) {
	f := paymentFixture(t)
	_, _, first := queuePayment(t, f)
	old := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, path := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = path
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.paymentFailed.sender = f.sender
	f.service = next
	_, _, second := queuePayment(t, f)
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigurePaymentFailed(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("startup ignored the pending historical reader")
	}
	for range 2 {
		if err := old.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 1 {
		t.Fatal("old worker claimed unreadable payload")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=$1 WHERE id=$2", first.SealedSecurityAlert, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" || len(f.sender.calls) != 1 {
		t.Fatal("cipher crossed account/intent/episode/key binding")
	}
}

func TestPGPaymentFailedReconciliationAndReceiptRollbackGuards(t *testing.T) {
	f := paymentFixture(t)
	id, _, r := queuePayment(t, f)
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithMFAEnabledAlert(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged a payment warning", err)
	}
	owner = owner.WithPaymentFailed(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != PaymentFailedTemplate {
		t.Fatal(err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithPaymentFailed(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact replay bypassed the payment gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed the payment gate", err)
	}
	_, down := migrationFileParts(t, paymentMigration)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded used payment history")
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != PaymentFailedTemplate {
		t.Fatal("erasure lost opaque reconciliation audit", err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded surviving reconciliation history")
	}
	// Even a no-op webhook has a durable receipt, which must not be discarded
	// just because no notification was generated for its account state.
	clean := paymentFixture(t)
	id, customer := paymentAccount(t, clean, "polar")
	if _, err := clean.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := clean.service.RecordPaymentFailure(t.Context(), id, "polar", uuid.NewString(), customer, billing.EventPaymentFailed); err != nil {
		t.Fatal(err)
	}
	if _, err := clean.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded a durable no-op event receipt")
	}
}

func TestPGPaymentFailedUnusedMigrationDownAndUp(t *testing.T) {
	f := paymentFixture(t)
	up, down := migrationFileParts(t, paymentMigration)
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old schema passed enabled readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigurePaymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestPGPaymentFailedRecoveryCancelsIntentAtomically(t *testing.T) {
	for _, mode := range []string{"past_due", "dunning_suspended", "dunning_deleted", "operator_suspended", "org_failure", "cancel_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := paymentFixture(t)
			id, customer, r := queuePayment(t, f)
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO orgs(id,personal_org,personal_owner_account_id,status) VALUES($1,true,$2,'suspended')", uuid.NewString(), id); err != nil {
				t.Fatal(err)
			}
			var inject string
			switch mode {
			case "dunning_suspended", "operator_suspended":
				inject = "UPDATE accounts SET status='suspended'"
				if mode == "operator_suspended" {
					inject += ",past_due_at=NULL"
				}
			case "dunning_deleted":
				inject = "UPDATE accounts SET status='deleted_pending',deletion_requested_at=clock_timestamp()"
			case "org_failure":
				inject = "ALTER TABLE orgs ADD CONSTRAINT fail_payment_org_restore CHECK (status<>'active')"
			case "cancel_failure":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT fail_payment_cancel CHECK (state<>'cancelled')"
			}
			if inject != "" {
				if _, err := f.pool.Exec(t.Context(), inject); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer)
			failure := mode == "org_failure" || mode == "cancel_failure"
			if failure != (err != nil) || !failure && restored != (mode != "operator_suspended") {
				t.Fatal("unexpected recovery result", restored, err)
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			var status, orgStatus string
			var overdue, deletion bool
			if err := f.pool.QueryRow(t.Context(), "SELECT status,past_due_at IS NOT NULL,deletion_requested_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&status, &overdue, &deletion); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(t.Context(), "SELECT status FROM orgs WHERE personal_owner_account_id=$1", id).Scan(&orgStatus); err != nil {
				t.Fatal(err)
			}
			if failure {
				if status != "past_due" || !overdue || orgStatus != "suspended" || got.State != "queued" {
					t.Fatal("failed recovery partially committed account/org/notification")
				}
			} else if mode == "operator_suspended" {
				if status != "suspended" || orgStatus != "suspended" {
					t.Fatal("payment lifted an operator suspension")
				}
			} else {
				if status != "active" || overdue || deletion || orgStatus != "active" || got.State != "cancelled" {
					t.Fatal("payment failed to restore and cancel the pending warning")
				}
				assertMFAAlertErased(t, got)
				if again, err := f.service.RestorePaymentAfterFailure(t.Context(), id, "stripe", customer); err != nil || again {
					t.Fatal("repeated payment recovery reported another transition", err)
				}
			}
		})
	}
}
