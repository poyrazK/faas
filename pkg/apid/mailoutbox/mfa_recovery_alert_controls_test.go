package mailoutbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGMFARecoveryAlertIndependentWorkerGate(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	r := queueMFAAlert(t, f, id, hashes[0])
	f.env[internalmail.MFARecoveryAlertOutboxEnableEnv] = "0"
	for _, configure := range []struct {
		flag, origin string
		configure    func(context.Context, func(string) string) error
	}{
		{internalmail.VerificationOutboxEnableEnv, VerificationOriginEnv, f.service.ConfigureVerification},
		{internalmail.PasswordResetOutboxEnableEnv, PasswordResetOriginEnv, f.service.ConfigurePasswordReset},
		{internalmail.MagicLinkOutboxEnableEnv, MagicLinkOriginEnv, f.service.ConfigureMagicLink},
	} {
		f.env[configure.flag] = "1"
		f.env[configure.origin] = "https://dashboard.example.test"
		if err := configure.configure(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("sibling capability dispatched a disabled security alert")
	}
}

func TestPGMFARecoveryAlertRecoveryHoldAndGenerationQuarantine(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	generation := uuid.NewString()
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", generation); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hashes[0]); !errors.Is(err, internalmail.ErrRecoveryHeld) {
		t.Fatal("recovery hold admitted a paired consume", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT cardinality(mfa_recovery_codes_hash) FROM accounts WHERE id=$1", id).Scan(&count); err != nil || count != 3 {
		t.Fatal("held operation consumed a recovery code", err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recovery_state SET held=false"); err != nil {
		t.Fatal(err)
	}
	r := queueMFAAlert(t, f, id, hashes[0])
	if uuidString(r.RecoveryID.Bytes) != generation {
		t.Fatal("security event lost its recovery generation")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE internal_mail_recovery_state SET recovery_id=$1,held=false", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("global release revived an older alert")
	}
}

func TestPGMFARecoveryAlertLifecycleSuppressionExpiryAndErasure(t *testing.T) {
	for _, scenario := range []string{"address_changed", "deletion_pending", "suppressed", "expired", "corrupt", "erased"} {
		t.Run(scenario, func(t *testing.T) {
			f := mfaAlertFixture(t)
			id, hashes := mfaAlertAccount(t, f, 3)
			r := queueMFAAlert(t, f, id, hashes[0])
			var err error
			switch scenario {
			case "address_changed":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id)
			case "deletion_pending":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending' WHERE id=$1", id)
			case "suppressed":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "expired":
				f.env[internalmail.MFARecoveryAlertOutboxEnableEnv] = "0"
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID)
			case "corrupt":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", r.ID)
			case "erased":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("unavailable alert reached admission")
			}
			if scenario == "erased" {
				var count int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox").Scan(&count); err != nil || count != 0 {
					t.Fatal("account erasure retained an alert", err)
				}
				return
			}
			want := "cancelled"
			if scenario == "suppressed" {
				want = "suppressed"
			} else if scenario == "expired" {
				want = "expired"
			} else if scenario == "corrupt" {
				want = "failed"
			}
			after := row(t, f, uuidString(r.ID.Bytes))
			if after.State != want {
				t.Fatal("alert lifecycle settlement changed", after.State)
			}
			assertMFAAlertErased(t, after)
		})
	}
}

func TestPGMFARecoveryAlertKeyOverlapAndCipherBinding(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	first := queueMFAAlert(t, f, id, hashes[0])
	oldWorker := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, newPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = newPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureMFARecoveryAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.mfaRecoveryAlert.sender = f.sender
	f.service = next
	second := queueMFAAlert(t, f, id, hashes[1])
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigureMFARecoveryAlert(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("startup ignored a pending alert's reader")
	}
	if err := oldWorker.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := oldWorker.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 1 {
		t.Fatal("old worker claimed an unreadable security event")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=$1 WHERE id=$2", first.SealedSecurityAlert, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" || len(f.sender.calls) != 1 {
		t.Fatal("copied ciphertext crossed event or key binding")
	}
}

func TestPGMFARecoveryAlertReconciliationAndRollbackGuards(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	r := queueMFAAlert(t, f, id, hashes[0])
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true }).WithMagicLink(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged a security event", err)
	}
	owner = owner.WithMFARecoveryAlert(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != MFARecoveryAlertTemplate {
		t.Fatal("confirmed security-event receipt could not reconcile", err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithMFARecoveryAlert(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact reconciliation replay bypassed alert gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != MFARecoveryAlertTemplate {
		t.Fatal("account erasure removed opaque audit evidence", err)
	}
	_, down := migrationFileParts(t, "20261017084607182_platform_mail_mfa_recovery_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("schema rollback discarded a security event audit")
	}
}

func TestPGMFARecoveryAlertUnusedRollbackPreservesExistingMail(t *testing.T) {
	f := newFixture(t)
	configureVerification(t, f)
	f.service.verification.sender = f.sender
	r := queueVerification(t, f, account(t, f))
	up, down := migrationFileParts(t, "20261017084607182_platform_mail_mfa_recovery_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if f.service.Ready(t.Context()) == nil {
		t.Fatal("old schema passed alert-aware readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "admitted" || len(f.sender.calls) != 1 {
		t.Fatal("unused alert rollback changed verification delivery")
	}
}

func TestPGMFARecoveryAlertWaitsForRecoveryFenceBeforeConsuming(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "SELECT pg_advisory_xact_lock(hashtextextended('platform',871739))"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := f.service.ConsumeMFARecoveryAlert(ctx, id, "one@example.net", hashes[0])
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	if _, err := tx.Exec(ctx, "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrRecoveryHeld) {
		t.Fatal("consume crossed a newly committed recovery hold", err)
	}
}
