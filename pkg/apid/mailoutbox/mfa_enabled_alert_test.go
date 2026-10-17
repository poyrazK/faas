package mailoutbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/pquerna/otp/totp"
)

func enabledAlertFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.MFAEnabledAlertOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.mfaEnabledAlert.sender = f.sender
	return f
}

type enrollmentProof struct {
	host   *age.X25519Identity
	secret string
	sealed []byte
	hashes [][]byte
}

func newEnrollmentProof(t *testing.T, host *age.X25519Identity) enrollmentProof {
	t.Helper()
	var err error
	if host == nil {
		host, err = age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
	}
	secret, err := auth.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.Seal(host.Recipient(), secretbox.Envelope{"MFA_SECRET": secret})
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([][]byte, authcode.RecoveryCodeCount)
	for i := range hashes {
		hashes[i] = make([]byte, authcode.HMACSize)
		if _, err := rand.Read(hashes[i]); err != nil {
			t.Fatal(err)
		}
	}
	return enrollmentProof{host, secret, sealed, hashes}
}

func (p enrollmentProof) code(t *testing.T) string {
	t.Helper()
	code, err := totp.GenerateCode(p.secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func saveEnrollment(t *testing.T, f *fixture, id string, p enrollmentProof) {
	t.Helper()
	if err := f.service.SavePendingMFAEnrollment(t.Context(), id, "one@example.net", p.sealed, p.hashes); err != nil {
		t.Fatal(err)
	}
}

func confirmEnrollment(t *testing.T, f *fixture, id string, p enrollmentProof) error {
	t.Helper()
	return f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", p.code(t), []*age.X25519Identity{p.host})
}

func enabledAlertRow(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='mfa_enabled'", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func TestMFAEnabledAlertExactOffReadsOnlyOwnGate(t *testing.T) {
	for _, value := range []string{"", "0", "true", "01", " 1"} {
		var s *Service
		if err := s.ConfigureMFAEnabledAlert(t.Context(), func(k string) string {
			if k != internalmail.MFAEnabledAlertOutboxEnableEnv {
				t.Fatal("off read private configuration")
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.SavePendingMFAEnrollment(t.Context(), "bad", "bad", nil, nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if err := s.ConfirmMFAEnrollmentWithAlert(t.Context(), "bad", "bad", "bad", nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
}

func TestPGMFAEnabledAlertAtomicEnrollmentAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "intent", "mark", "hold", "bad_code", "no_host_reader", "corrupt_secret", "empty_secret", "address", "status"} {
		t.Run(scenario, func(t *testing.T) {
			f := enabledAlertFixture(t)
			id := account(t, f)
			p := newEnrollmentProof(t, nil)
			if _, err := state.NewPgStore(f.pool).SetMFARequired(t.Context(), id, true); err != nil {
				t.Fatal(err)
			}
			saveEnrollment(t, f, id, p)
			var initialRevision int64
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_revision FROM accounts WHERE id=$1", id).Scan(&initialRevision); err != nil {
				t.Fatal(err)
			}
			code := p.code(t)
			keys := []*age.X25519Identity{p.host}
			switch scenario {
			case "intent":
				if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_enabled_alert_failure CHECK (template<>'mfa_enabled')"); err != nil {
					t.Fatal(err)
				}
			case "mark":
				if _, err := f.pool.Exec(t.Context(), "ALTER TABLE accounts ADD CONSTRAINT injected_enabled_mark_failure CHECK (mfa_enrolled_at IS NULL)"); err != nil {
					t.Fatal(err)
				}
			case "hold":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.Nil.String()); err != nil {
					t.Fatal(err)
				}
			case "bad_code":
				code = "not-a-code"
			case "no_host_reader":
				keys = nil
			case "corrupt_secret":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET mfa_secret_encrypted='corrupt' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "empty_secret":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET mfa_secret_encrypted=NULL WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "address":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='new@example.net' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "status":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			err := f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", code, keys)
			if (scenario == "success") != (err == nil) {
				t.Fatal("unexpected confirmation result", err)
			}
			var enrolled, required bool
			var events int
			var revision int64
			var hashes [][]byte
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL,mfa_required,mfa_revision,mfa_recovery_codes_hash,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled') FROM accounts WHERE id=$1", id).Scan(&enrolled, &required, &revision, &hashes, &events); err != nil {
				t.Fatal(err)
			}
			success := scenario == "success"
			wantEvents := 0
			if success {
				wantEvents = 1
			}
			if enrolled != success || required == success || events != wantEvents || revision != initialRevision+int64(wantEvents) || !reflect.DeepEqual(hashes, p.hashes) {
				t.Fatal("enrollment, policy, revision, recovery codes and alert were not atomic")
			}
			if !success {
				return
			}
			r := enabledAlertRow(t, f, id)
			payload, err := mfaEnabledAlertPayloadFor(f.service.mfaEnabledAlert, r)
			var at time.Time
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at FROM accounts WHERE id=$1", id).Scan(&at); err != nil {
				t.Fatal(err)
			}
			if err != nil || !at.Equal(payload.EnabledAt) || !payload.EnabledAt.Add(api.InternalMailMFAEnabledAlertTTL).Equal(r.ExpiresAt.Time) {
				t.Fatal("alert lost exact activation time/deadline", err)
			}
			plain, _ := json.Marshal(payload)
			var fields map[string]any
			if err := json.Unmarshal(plain, &fields); err != nil || len(fields) != 1 || fields["enabled_at"] == nil || bytes.Contains(plain, []byte(p.secret)) {
				t.Fatal("alert contains secret material")
			}
			if len(r.SealedVerification) != 0 || len(r.VerificationTokenHash) != 0 || r.VerificationOrigin != "" {
				t.Fatal("alert contains credentials")
			}
			if err := confirmEnrollment(t, f, id, p); err != nil {
				t.Fatal("valid repeated confirmation failed", err)
			}
			var unchanged bool
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at=$2 AND mfa_revision=$3,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled') FROM accounts WHERE id=$1", id, at, revision).Scan(&unchanged, &events); err != nil || !unchanged || events != 1 {
				t.Fatal("repeat confirmation rewrote activation or duplicated event", err)
			}
			if err := f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", "bad", keys); !errors.Is(err, ErrMFAEnrollmentInvalidCode) {
				t.Fatal("repeat bypassed TOTP possession", err)
			}
		})
	}
}

func TestPGMFAEnabledAlertConcurrentConfirmationAndSupersededSetup(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	p := newEnrollmentProof(t, nil)
	saveEnrollment(t, f, id, p)
	code := p.code(t)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := f.service.ConfirmMFAEnrollmentWithAlert(t.Context(), id, "one@example.net", code, []*age.X25519Identity{p.host}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent confirmation duplicated alert", err)
	}
	next := newEnrollmentProof(t, p.host)
	if err := f.service.SavePendingMFAEnrollment(t.Context(), id, "one@example.net", next.sealed, next.hashes); !errors.Is(err, ErrMFAEnrollmentAlreadyEnabled) {
		t.Fatal("stale setup replaced confirmed factor", err)
	}
	var sealed []byte
	var hashes [][]byte
	if err := f.pool.QueryRow(t.Context(), "SELECT mfa_secret_encrypted,mfa_recovery_codes_hash FROM accounts WHERE id=$1", id).Scan(&sealed, &hashes); err != nil || !bytes.Equal(sealed, p.sealed) || !reflect.DeepEqual(hashes, p.hashes) {
		t.Fatal("stale setup changed the factor or recovery codes", err)
	}
	// Pending setup can be intentionally replaced. The old factor cannot confirm it.
	other := account(t, f)
	saveEnrollment(t, f, other, p)
	for auth.VerifyCode(next.secret, p.code(t)) {
		next = newEnrollmentProof(t, p.host)
	}
	saveEnrollment(t, f, other, next)
	if err := confirmEnrollment(t, f, other, p); !errors.Is(err, ErrMFAEnrollmentInvalidCode) {
		t.Fatal("superseded factor confirmed enrollment", err)
	}
	if err := confirmEnrollment(t, f, other, next); err != nil {
		t.Fatal(err)
	}
}

func TestPGMFAEnabledAlertExistingEnrollmentDoesNotBackfill(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	p := newEnrollmentProof(t, nil)
	saveEnrollment(t, f, id, p)
	if err := state.NewPgStore(f.pool).MarkMFAEnrolled(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := confirmEnrollment(t, f, id, p); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled'").Scan(&n); err != nil || n != 0 {
		t.Fatal("confirmation backfilled an old enrollment", err)
	}
}

func TestPGMFAEnabledAlertRetryKeepsHistoricalActivation(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	p := newEnrollmentProof(t, nil)
	saveEnrollment(t, f, id, p)
	if err := confirmEnrollment(t, f, id, p); err != nil {
		t.Fatal(err)
	}
	r := enabledAlertRow(t, f, id)
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := state.NewPgStore(f.pool).ClearMFA(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended',abuse_hold_at=clock_timestamp(),password_reset_revision=password_reset_revision+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureMFAEnabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.mfaEnabledAlert.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "mfa-enabled:"+uuidString(r.ID.Bytes) {
		t.Fatal("retry rewrote historical activation")
	}
	if strings.Contains(f.sender.calls[0].message.TextBody, p.secret) {
		t.Fatal("email contains authenticator secret")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGMFAEnabledAlertLateGateWithdrawalRollsBack(t *testing.T) {
	f := enabledAlertFixture(t)
	id := account(t, f)
	p := newEnrollmentProof(t, nil)
	saveEnrollment(t, f, id, p)
	var on atomic.Bool
	on.Store(true)
	f.service.mfaEnabledAlert.enabled = on.Load
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
	code := p.code(t)
	done := make(chan error, 1)
	go func() {
		done <- f.service.ConfirmMFAEnrollmentWithAlert(ctx, id, "one@example.net", code, []*age.X25519Identity{p.host})
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late withdrawal confirmed MFA", err)
	}
	var enrolled bool
	var n int
	if err := f.pool.QueryRow(ctx, "SELECT mfa_enrolled_at IS NOT NULL,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_enabled') FROM accounts WHERE id=$1", id).Scan(&enrolled, &n); err != nil || enrolled || n != 0 {
		t.Fatal("withdrawal partially committed enrollment", err)
	}
}
