package mailoutbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGMFADisableIndependentWorkerGate(t *testing.T) {
	f := disableFixture(t)
	r := queueDisable(t, f, disableAccount(t, f))
	f.env[internalmail.MFADisableOutboxEnableEnv] = "0"
	for _, sibling := range []struct {
		flag, origin string
		configure    func(context.Context, func(string) string) error
	}{
		{internalmail.VerificationOutboxEnableEnv, VerificationOriginEnv, f.service.ConfigureVerification},
		{internalmail.PasswordResetOutboxEnableEnv, PasswordResetOriginEnv, f.service.ConfigurePasswordReset},
		{internalmail.MagicLinkOutboxEnableEnv, MagicLinkOriginEnv, f.service.ConfigureMagicLink},
		{internalmail.MFARecoveryAlertOutboxEnableEnv, "unused", f.service.ConfigureMFARecoveryAlert},
	} {
		f.env[sibling.flag] = "1"
		f.env[sibling.origin] = "https://dashboard.example.test"
		if err := sibling.configure(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("sibling capability claimed a disabled request")
	}
}

func TestPGMFADisableReadersOriginAndCipherBinding(t *testing.T) {
	f := disableFixture(t)
	first := queueDisable(t, f, disableAccount(t, f))
	oldWorker := f.service
	oldPath := f.env[VerificationIdentityEnv]
	_, newPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = newPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureMFADisable(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.mfaDisable.sender = f.sender
	f.service = next
	second := queueDisable(t, f, disableAccount(t, f))
	missing, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.ConfigureMFADisable(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("startup ignored pending ciphertext without its reader")
	}
	for range 2 {
		if err := oldWorker.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "queued" || got.Attempts != 0 || len(f.sender.calls) != 1 {
		t.Fatal("old worker claimed a new unreadable payload")
	}
	next.mfaDisable.origin = "https://changed.example.test"
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.Attempts != 0 || len(f.sender.calls) != 1 {
		t.Fatal("origin drift rewrote an issued link")
	}
	next.mfaDisable.origin = f.env[MFADisableOriginEnv]
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_verification=$1 WHERE id=$2", first.SealedVerification, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" || len(f.sender.calls) != 1 {
		t.Fatal("ciphertext crossed intent/account/key bindings")
	}
}

func TestPGMFADisableLifecycleMaintenanceAndErasure(t *testing.T) {
	for _, scenario := range []string{"suppressed", "expired_child_off", "hard_delete"} {
		t.Run(scenario, func(t *testing.T) {
			f := disableFixture(t)
			id := disableAccount(t, f)
			r := queueDisable(t, f, id)
			var err error
			switch scenario {
			case "suppressed":
				_, err = f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')")
			case "expired_child_off":
				f.env[internalmail.MFADisableOutboxEnableEnv] = "0"
				_, err = f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", r.ID)
				if err == nil {
					_, err = f.pool.Exec(t.Context(), "UPDATE mfa_disable_tokens SET requested_at=requested_at-interval '49 hours',ready_at=ready_at-interval '49 hours',expires_at=expires_at-interval '49 hours'")
				}
			case "hard_delete":
				_, err = f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("closed request reached mail admission")
			}
			if scenario != "hard_delete" {
				after := row(t, f, uuidString(r.ID.Bytes))
				want := "suppressed"
				if scenario == "expired_child_off" {
					want = "expired"
				}
				if after.State != want {
					t.Fatal("maintenance lost terminal state", after.State)
				}
				assertMFAAlertErased(t, after)
			}
			if scenario != "suppressed" {
				var tokens int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM mfa_disable_tokens").Scan(&tokens); err != nil || tokens != 0 {
					t.Fatal("expired/erased credentials were retained", err)
				}
			}
		})
	}
}

func TestPGMFADisableRecoveryWaitAndGenerationQuarantine(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
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
	go func() { done <- f.service.QueueMFADisable(ctx, id, "one@example.net") }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	if _, err := tx.Exec(ctx, "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrRecoveryHeld) {
		t.Fatal("issuance crossed a newly committed recovery hold", err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE internal_mail_recovery_state SET held=false"); err != nil {
		t.Fatal(err)
	}
	r := queueDisable(t, f, id)
	if _, err := f.pool.Exec(ctx, "UPDATE internal_mail_recovery_state SET recovery_id=$1", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(ctx); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.Attempts != 0 || len(f.sender.calls) != 0 {
		t.Fatal("global release revived a restored generation's request")
	}
}

func TestPGMFADisableConcurrentReissuanceKeepsOneLiveRequest(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := f.service.QueueMFADisable(t.Context(), id, "one@example.net"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var live, total int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FILTER (WHERE consumed_at IS NULL),count(*) FROM mfa_disable_tokens").Scan(&live, &total); err != nil || live != 1 || total != 2 {
		t.Fatal("concurrent reissue kept multiple usable links", err)
	}
}

func TestPGMFADisableReconciliationAndSchemaRollback(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	r := queueDisable(t, f, id)
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true }).WithMagicLink(func() bool { return true }).WithMFARecoveryAlert(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling capability reconciled a disabled request", err)
	}
	owner = owner.WithMFADisable(func() bool { return true })
	if record, err := owner.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); err != nil || record.Template != MFADisableTemplate {
		t.Fatal("confirmed receipt could not reconcile", err)
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	blocked := owner.WithMFADisable(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(r.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("exact replay bypassed the independent gate", err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("audit bypassed the independent gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if record, err := owner.Audit(t.Context(), input.RequestID); err != nil || record.Template != MFADisableTemplate {
		t.Fatal("account erasure removed opaque audit history", err)
	}
	_, down := migrationFileParts(t, "20261017091735652_platform_mail_mfa_disable_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("schema rollback discarded request history")
	}
}

func TestPGMFADisableUnusedSchemaRollbackPreservesVerification(t *testing.T) {
	f := newFixture(t)
	configureVerification(t, f)
	f.service.verification.sender = f.sender
	r := queueVerification(t, f, account(t, f))
	up, down := migrationFileParts(t, "20261017091735652_platform_mail_mfa_disable_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if f.service.Ready(t.Context()) == nil {
		t.Fatal("old schema passed disable-aware readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "admitted" || len(f.sender.calls) != 1 {
		t.Fatal("unused schema rollback changed verification delivery")
	}
}

func TestPGMFADisablePreservesPolicyAndRecoveryBurnDoesNotResetRequest(t *testing.T) {
	f := disableFixture(t)
	id := disableAccount(t, f)
	s := state.NewPgStore(f.pool)
	if _, err := s.SetMFARequired(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
	r := queueDisable(t, f, id)
	before := disableToken(t, f, r)
	var hashes [][]byte
	if err := f.pool.QueryRow(t.Context(), "SELECT mfa_recovery_codes_hash FROM accounts WHERE id=$1", id).Scan(&hashes); err != nil {
		t.Fatal(err)
	}
	if ok, _, _, err := s.ConsumeRecoveryCode(t.Context(), id, hashes[0]); err != nil || !ok {
		t.Fatal("fixture recovery code did not burn", err)
	}
	if after := disableToken(t, f, r); !after.ReadyAt.Time.Equal(before.ReadyAt.Time) || !after.ExpiresAt.Time.Equal(before.ExpiresAt.Time) {
		t.Fatal("recovery-code use reset a pending request")
	}
	readyDisable(t, f, r)
	if _, err := NewMFADisableCredentials(f.pool).Redeem(t.Context(), id, r.VerificationTokenHash); err != nil {
		t.Fatal("code burn invalidated an unchanged enrolled factor", err)
	}
	var required, enrolled bool
	if err := f.pool.QueryRow(t.Context(), "SELECT mfa_required,mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&required, &enrolled); err != nil || !required || enrolled {
		t.Fatal("confirmation removed explicit MFA policy", err)
	}
}

func TestPGMFADisableConfirmationRechecksAfterOwnerWaits(t *testing.T) {
	for _, scenario := range []string{"expiry", "recovery_hold"} {
		t.Run(scenario, func(t *testing.T) {
			f := disableFixture(t)
			id := disableAccount(t, f)
			r := queueDisable(t, f, id)
			readyDisable(t, f, r)
			var deadline time.Time
			if scenario == "expiry" {
				if err := f.pool.QueryRow(t.Context(), "WITH deadline AS MATERIALIZED (SELECT clock_timestamp()+interval '400 milliseconds' AS at) UPDATE mfa_disable_tokens SET requested_at=d.at-interval '48 hours',ready_at=d.at-interval '24 hours',expires_at=d.at FROM deadline d WHERE token_hash=$1 RETURNING expires_at", r.VerificationTokenHash).Scan(&deadline); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(t.Context(), tx)
			if scenario == "expiry" {
				_, err = tx.Exec(t.Context(), "SELECT token_hash FROM mfa_disable_tokens WHERE token_hash=$1 FOR UPDATE", r.VerificationTokenHash)
			} else {
				_, err = tx.Exec(t.Context(), "SELECT pg_advisory_xact_lock(hashtextextended('platform',871739))")
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := NewMFADisableCredentials(f.pool).Redeem(ctx, id, r.VerificationTokenHash)
				done <- err
			}()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			if scenario == "expiry" {
				timer := time.NewTimer(max(time.Until(deadline)+time.Millisecond, 0))
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			} else if _, err := tx.Exec(ctx, "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrMFADisableInvalid) {
				t.Fatal("confirmation crossed an expiry/recovery wait", err)
			}
			var enrolled bool
			if err := f.pool.QueryRow(ctx, "SELECT mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || !enrolled || disableToken(t, f, r).ConsumedAt.Valid {
				t.Fatal("blocked confirmation changed factor or token", err)
			}
		})
	}
}
