package mailoutbox

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/pquerna/otp/totp"
)

func queueEnabled(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	p := newEnrollmentProof(t, nil)
	saveEnrollment(t, f, id, p)
	if err := confirmEnrollment(t, f, id, p); err != nil {
		t.Fatal(err)
	}
	return enabledAlertRow(t, f, id)
}

func TestPGMFAEnabledAlertIndependentGateAndHistoryLifecycle(t *testing.T) {
	for _, scenario := range []string{"child_off", "address", "deletion", "suppression", "expiry", "corrupt", "erasure", "recovery_generation"} {
		t.Run(scenario, func(t *testing.T) {
			f := enabledAlertFixture(t)
			id := account(t, f)
			r := queueEnabled(t, f, id)
			var err error
			switch scenario {
			case "child_off":
				f.env[internalmail.MFAEnabledAlertOutboxEnableEnv] = "0"
				for _, sibling := range []struct {
					flag, origin string
					configure    func(context.Context, func(string) string) error
				}{
					{internalmail.PasswordChangedAlertOutboxEnableEnv, "unused", f.service.ConfigurePasswordChangedAlert},
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
				f.env[internalmail.MFAEnabledAlertOutboxEnableEnv] = "0"
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

func TestPGMFAEnabledAlertKeysAndCipherBinding(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	first := queueEnabled(t, f, id)
	old := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, newPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = newPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.mfaEnabledAlert.sender = f.sender
	f.service = next
	id = account(t, f)
	second := queueEnabled(t, f, id)
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigureMFAEnabledAlert(t.Context(), func(k string) string {
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

func TestPGMFAEnabledAlertReconciliationAndSchemaGuards(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	r := queueEnabled(t, f, id)
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true }).WithMagicLink(func() bool { return true }).WithMFARecoveryAlert(func() bool { return true }).WithMFADisable(func() bool { return true }).WithMFADisabledAlert(func() bool { return true }).WithPasswordChangedAlert(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability acknowledged alert", err)
	}
	owner = owner.WithMFAEnabledAlert(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != MFAEnabledAlertTemplate {
		t.Fatal(err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithMFAEnabledAlert(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact replay bypassed own gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != MFAEnabledAlertTemplate {
		t.Fatal("erasure lost opaque audit", err)
	}
	_, down := migrationFileParts(t, "20261017102802761_platform_mail_mfa_enabled_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded alert history")
	}
}

func TestPGMFAEnabledAlertUnusedSchemaRollbackAndReadiness(t *testing.T) {
	f := enabledAlertFixture(t)
	up, down := migrationFileParts(t, "20261017102802761_platform_mail_mfa_enabled_alert_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old constraints passed enabled readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestPGMFAEnabledAlertTOTPExpiryDuringSaveRollsBack(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	p := newEnrollmentProof(t, nil)
	if _, err := state.NewPgStore(f.pool).SetMFARequired(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
	// A code from the preceding step expires at the next boundary. Near that
	// boundary use the current step instead, keeping the real-clock wait <38s.
	anchor := time.Now().UTC().Truncate(30 * time.Second)
	deadline := anchor.Add(30 * time.Second)
	codeAt := anchor.Add(-30 * time.Second)
	if time.Until(deadline) < 8*time.Second {
		deadline = anchor.Add(60 * time.Second)
		codeAt = anchor
	}
	var code string
	for {
		var err error
		code, err = totp.GenerateCode(p.secret, codeAt)
		if err != nil {
			t.Fatal(err)
		}
		collision := false
		for i := -1; i <= 1; i++ {
			after, err := totp.GenerateCode(p.secret, deadline.Add(time.Duration(i)*30*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			collision = collision || after == code
		}
		if !collision {
			break
		}
		p = newEnrollmentProof(t, p.host)
	}
	saveEnrollment(t, f, id, p)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "LOCK TABLE platform_mail_producer_outbox IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- f.service.ConfirmMFAEnrollmentWithAlert(ctx, id, "one@example.net", code, []*age.X25519Identity{p.host})
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	timer := time.NewTimer(max(time.Until(deadline)+10*time.Millisecond, 0))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrMFAEnrollmentInvalidCode) {
		t.Fatal("expired TOTP crossed the intent save wait", err)
	}
	var enrolled, required bool
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT mfa_enrolled_at IS NOT NULL,mfa_required,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled') FROM accounts WHERE id=$1", id).Scan(&enrolled, &required, &count); err != nil || enrolled || !required || count != 0 {
		t.Fatal("expired proof partially committed enrollment", err)
	}
}

func TestPGMFAEnabledAlertProofAndPreparationAfterOwnerWait(t *testing.T) {
	for _, scenario := range []string{"factor_replaced", "factor_removed", "flag_off", "setup_after_confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			f := enabledAlertFixture(t)
			id := account(t, f)
			p := newEnrollmentProof(t, nil)
			saveEnrollment(t, f, id, p)
			next := newEnrollmentProof(t, p.host)
			code := p.code(t)
			for auth.VerifyCode(next.secret, code) {
				next = newEnrollmentProof(t, p.host)
			}
			var on atomic.Bool
			on.Store(true)
			f.service.mfaEnabledAlert.enabled = on.Load
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
			go func() {
				if scenario == "setup_after_confirmation" {
					done <- f.service.SavePendingMFAEnrollment(ctx, id, "one@example.net", next.sealed, next.hashes)
				} else {
					done <- f.service.ConfirmMFAEnrollmentWithAlert(ctx, id, "one@example.net", code, []*age.X25519Identity{p.host})
				}
			}()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			store := state.NewPgStore(f.pool)
			want := ErrMFAEnrollmentInvalidCode
			switch scenario {
			case "factor_replaced":
				if err := store.SetMFASecret(ctx, id, next.sealed, next.hashes); err != nil {
					t.Fatal(err)
				}
			case "factor_removed":
				if err := store.ClearMFA(ctx, id); err != nil {
					t.Fatal(err)
				}
				want = ErrMFAEnrollmentUnavailable
			case "flag_off":
				on.Store(false)
				want = internalmail.ErrDisabled
			case "setup_after_confirmation":
				if err := store.MarkMFAEnrolled(ctx, id); err != nil {
					t.Fatal(err)
				}
				want = ErrMFAEnrollmentAlreadyEnabled
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("owner wait admitted stale MFA work", err)
			}
			if scenario == "setup_after_confirmation" {
				var sealed []byte
				if err := f.pool.QueryRow(ctx, "SELECT mfa_secret_encrypted FROM accounts WHERE id=$1", id).Scan(&sealed); err != nil || !bytes.Equal(sealed, p.sealed) {
					t.Fatal("stale setup overwrote the confirmed factor", err)
				}
			}
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&n); err != nil || n != 0 {
				t.Fatal("stale proof emitted activation", err)
			}
		})
	}
}

func TestPGMFAEnabledAlertQuotaAndHostKeyOverlap(t *testing.T) {
	t.Run("quota", func(t *testing.T) {
		f := enabledAlertFixture(t)
		id := account(t, f)
		p := newEnrollmentProof(t, nil)
		saveEnrollment(t, f, id, p)
		seed := queueEnabled(t, f, account(t, f))
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
SELECT gen_random_uuid(),o.account_id,o.namespace,o.producer_id,o.from_address,o.recipient_address,o.template,o.template_version,o.expires_at,o.recovery_id,o.sealed_security_alert,o.security_alert_key_id FROM platform_mail_producer_outbox o CROSS JOIN generate_series(1,$2) WHERE o.id=$1`, seed.ID, api.InternalMailProducerOutboxMaxPending-1); err != nil {
			t.Fatal(err)
		}
		if err := confirmEnrollment(t, f, id, p); !errors.Is(err, internalmail.ErrQuota) {
			t.Fatal("quota allowed activation without an alert", err)
		}
		var enrolled bool
		if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || enrolled {
			t.Fatal("quota did not roll back activation", err)
		}
	})
	t.Run("host_overlap", func(t *testing.T) {
		f := enabledAlertFixture(t)
		id := account(t, f)
		p := newEnrollmentProof(t, nil)
		saveEnrollment(t, f, id, p)
		current, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", p.code(t), []*age.X25519Identity{current}); !errors.Is(err, ErrMFAEnrollmentSecretUnavailable) {
			t.Fatal("unreadable host secret was accepted", err)
		}
		if err := f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", p.code(t), []*age.X25519Identity{current, p.host}); err != nil {
			t.Fatal("host overlap reader failed", err)
		}
	})
}
