package mailoutbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func queueDisabledRecovery(t *testing.T, f *fixture, id string, hash []byte) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.DisableMFARecoveryWithAlert(t.Context(), id, "one@example.net", hash); err != nil {
		t.Fatal(err)
	}
	return disabledAlertRow(t, f, id)
}

func TestPGMFADisabledAlertIndependentGateAndHistoryLifecycle(t *testing.T) {
	for _, scenario := range []string{"child_off", "address", "deletion", "suppression", "expiry", "corrupt", "erasure", "recovery_generation"} {
		t.Run(scenario, func(t *testing.T) {
			f := disabledAlertFixture(t)
			id, hashes := mfaAlertAccount(t, f, 3)
			r := queueDisabledRecovery(t, f, id, hashes[0])
			var err error
			switch scenario {
			case "child_off":
				f.env[internalmail.MFADisabledAlertOutboxEnableEnv] = "0"
				for _, sibling := range []struct {
					flag, origin string
					configure    func(context.Context, func(string) string) error
				}{
					{internalmail.MFARecoveryAlertOutboxEnableEnv, "unused", f.service.ConfigureMFARecoveryAlert},
					{internalmail.MFADisableOutboxEnableEnv, MFADisableOriginEnv, f.service.ConfigureMFADisable},
					{internalmail.VerificationOutboxEnableEnv, VerificationOriginEnv, f.service.ConfigureVerification},
					{internalmail.PasswordResetOutboxEnableEnv, PasswordResetOriginEnv, f.service.ConfigurePasswordReset},
					{internalmail.MagicLinkOutboxEnableEnv, MagicLinkOriginEnv, f.service.ConfigureMagicLink},
				} {
					f.env[sibling.flag] = "1"
					f.env[sibling.origin] = "https://dashboard.example.test"
					if err := sibling.configure(t.Context(), func(k string) string { return f.env[k] }); err != nil {
						t.Fatal(err)
					}
				}
			case "address":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET email='new@example.net' WHERE id=$1", id)
			case "deletion":
				_, err = f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending' WHERE id=$1", id)
			case "suppression":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "expiry":
				f.env[internalmail.MFADisabledAlertOutboxEnableEnv] = "0"
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID)
			case "corrupt":
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", r.ID)
			case "erasure":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
			case "recovery_generation":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,false)", uuid.NewString())
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("unavailable/paused alert reached maild")
			}
			if scenario == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox").Scan(&n); err != nil || n != 0 {
					t.Fatal("erasure retained event", err)
				}
				return
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			if scenario == "child_off" || scenario == "recovery_generation" {
				if got.State != "queued" || got.Attempts != 0 {
					t.Fatal("disabled/quarantined alert was claimed")
				}
				return
			}
			want := "cancelled"
			if scenario == "suppression" {
				want = "suppressed"
			}
			if scenario == "expiry" {
				want = "expired"
			}
			if scenario == "corrupt" {
				want = "failed"
			}
			if got.State != want {
				t.Fatal("wrong terminal state", got.State)
			}
			assertMFAAlertErased(t, got)
		})
	}
}

func TestPGMFADisabledAlertRecoveryHoldRefusesAllRemovals(t *testing.T) {
	for _, method := range []string{"password", "recovery_code", "durable_email", "legacy_email"} {
		t.Run(method, func(t *testing.T) {
			f := disabledAlertFixture(t)
			id := disableAccount(t, f)
			operation, _ := disabledOperation(t, f, id, method)
			// Use the pre-recovery generation so the held-state check itself is tested.
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.Nil.String()); err != nil {
				t.Fatal(err)
			}
			err := operation()
			if !errors.Is(err, internalmail.ErrRecoveryHeld) && !errors.Is(err, ErrMFADisableInvalid) {
				t.Fatal("hold allowed paired removal", err)
			}
			var enrolled bool
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || !enrolled {
				t.Fatal("hold cleared MFA", err)
			}
		})
	}
}

func TestPGMFADisabledAlertKeysAndCipherBinding(t *testing.T) {
	f := disabledAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	first := queueDisabledRecovery(t, f, id, hashes[0])
	old := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, newPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = newPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.mfaDisabledAlert.sender = f.sender
	f.service = next
	id, hashes = mfaAlertAccount(t, f, 3)
	second := queueDisabledRecovery(t, f, id, hashes[0])
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigureMFADisabledAlert(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("startup ignored a pending reader")
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
		t.Fatal("cipher crossed account/intent/key binding")
	}
}

func TestPGMFADisabledAlertReconciliationAndSchemaGuards(t *testing.T) {
	f := disabledAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	r := queueDisabledRecovery(t, f, id, hashes[0])
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true }).WithMagicLink(func() bool { return true }).WithMFARecoveryAlert(func() bool { return true }).WithMFADisable(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged alert", err)
	}
	owner = owner.WithMFADisabledAlert(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != MFADisabledAlertTemplate {
		t.Fatal(err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithMFADisabledAlert(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact replay bypassed own gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != MFADisabledAlertTemplate {
		t.Fatal("erasure lost opaque audit", err)
	}
	_, down := migrationFileParts(t, "20261017094937523_platform_mail_mfa_disabled_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded alert history")
	}
}

func TestPGMFADisabledAlertUnusedSchemaRollbackAndReadiness(t *testing.T) {
	f := disabledAlertFixture(t)
	up, down := migrationFileParts(t, "20261017094937523_platform_mail_mfa_disabled_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old constraints passed enabled readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestPGMFADisabledAlertExpiryDuringIntentSaveRollsBackConfirmation(t *testing.T) {
	f := disabledAlertFixture(t)
	id := disableAccount(t, f)
	_, hash := disabledOperation(t, f, id, "durable_email")
	var deadline time.Time
	if err := f.pool.QueryRow(t.Context(), "WITH deadline AS MATERIALIZED (SELECT clock_timestamp()+interval '2 seconds' AS at) UPDATE mfa_disable_tokens SET requested_at=d.at-interval '48 hours',ready_at=d.at-interval '24 hours',expires_at=d.at FROM deadline d WHERE token_hash=$1 RETURNING expires_at", hash).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "LOCK TABLE platform_mail_producer_outbox IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.service.ConfirmMFADisableWithAlert(ctx, id, hash); done <- err }()
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
	if err := <-done; !errors.Is(err, ErrMFADisableInvalid) {
		t.Fatal("expired credential crossed an alert-save wait", err)
	}
	var enrolled, consumed bool
	var events int
	if err := f.pool.QueryRow(ctx, "SELECT a.mfa_enrolled_at IS NOT NULL,t.consumed_at IS NOT NULL,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_disabled') FROM accounts a JOIN mfa_disable_tokens t ON t.account_id=a.id WHERE t.token_hash=$1", hash).Scan(&enrolled, &consumed, &events); err != nil || !enrolled || consumed || events != 0 {
		t.Fatal("expiry partially committed removal, credential or alert", err)
	}
}
