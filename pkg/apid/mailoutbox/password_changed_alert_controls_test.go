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

func queueChanged(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	change, err := f.service.PreparePasswordChange(t.Context(), id, "one@example.net", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.CommitPasswordChange(t.Context(), change, "new-password-phc"); err != nil {
		t.Fatal(err)
	}
	return changedAlertRow(t, f, id)
}

func TestPGPasswordChangedAlertIndependentGateAndHistoryLifecycle(t *testing.T) {
	for _, scenario := range []string{"child_off", "address", "deletion", "suppression", "expiry", "corrupt", "erasure", "recovery_generation"} {
		t.Run(scenario, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			r := queueChanged(t, f, id)
			var err error
			switch scenario {
			case "child_off":
				f.env[internalmail.PasswordChangedAlertOutboxEnableEnv] = "0"
				for _, sibling := range []struct {
					flag, origin string
					configure    func(context.Context, func(string) string) error
				}{
					{internalmail.MFARecoveryAlertOutboxEnableEnv, "unused", f.service.ConfigureMFARecoveryAlert},
					{internalmail.MFADisabledAlertOutboxEnableEnv, "unused", f.service.ConfigureMFADisabledAlert},
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
				f.env[internalmail.PasswordChangedAlertOutboxEnableEnv] = "0"
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

func TestPGPasswordChangedAlertKeysAndCipherBinding(t *testing.T) {
	f := changedAlertFixture(t)
	id := account(t, f)
	first := queueChanged(t, f, id)
	old := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, newPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = newPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.passwordChangedAlert.sender = f.sender
	f.service = next
	id = account(t, f)
	second := queueChanged(t, f, id)
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigurePasswordChangedAlert(t.Context(), func(k string) string {
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

func TestPGPasswordChangedAlertReconciliationAndSchemaGuards(t *testing.T) {
	f := changedAlertFixture(t)
	id := account(t, f)
	r := queueChanged(t, f, id)
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true }).WithMagicLink(func() bool { return true }).WithMFARecoveryAlert(func() bool { return true }).WithMFADisable(func() bool { return true }).WithMFADisabledAlert(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged alert", err)
	}
	owner = owner.WithPasswordChangedAlert(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != PasswordChangedAlertTemplate {
		t.Fatal(err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithPasswordChangedAlert(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact replay bypassed own gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != PasswordChangedAlertTemplate {
		t.Fatal("erasure lost opaque audit", err)
	}
	_, down := migrationFileParts(t, "20261017101020256_platform_mail_password_changed_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded alert history")
	}
}

func TestPGPasswordChangedAlertUnusedSchemaRollbackAndReadiness(t *testing.T) {
	f := changedAlertFixture(t)
	up, down := migrationFileParts(t, "20261017101020256_platform_mail_password_changed_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old constraints passed enabled readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestPGPasswordChangedAlertResetExpiryDuringIntentSaveRollsBack(t *testing.T) {
	for _, method := range []string{"durable_reset", "legacy_reset"} {
		t.Run(method, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			operation, hash := passwordChangeOperation(t, f, id, method)
			query := "UPDATE password_reset_tokens SET expires_at=clock_timestamp()+interval '2 seconds' WHERE token_hash=$1 RETURNING expires_at"
			if method == "legacy_reset" {
				query = "UPDATE login_tokens SET expires_at=clock_timestamp()+interval '2 seconds' WHERE token_hash=$1 RETURNING expires_at"
			}
			var deadline time.Time
			if err := f.pool.QueryRow(t.Context(), query, hash).Scan(&deadline); err != nil {
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
			go func() { done <- operation() }()
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
				t.Fatal("expired reset crossed the alert insert wait", err)
			}
			var n int
			var revision int64
			if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM account_passwords WHERE account_id=$1)+(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_changed'),password_reset_revision FROM accounts WHERE id=$1", id).Scan(&n, &revision); err != nil || n != 0 || revision != 1 {
				t.Fatal("expiry committed a partial password change", err)
			}
			query = "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1"
			if method == "legacy_reset" {
				query = "SELECT consumed_at IS NOT NULL FROM login_tokens WHERE token_hash=$1"
			}
			var consumed bool
			if err := f.pool.QueryRow(ctx, query, hash).Scan(&consumed); err != nil || consumed {
				t.Fatal("expiry consumed reset token", err)
			}
		})
	}
}
