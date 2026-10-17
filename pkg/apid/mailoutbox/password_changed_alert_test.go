package mailoutbox

import (
	"bytes"
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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func changedAlertFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.PasswordChangedAlertOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.passwordChangedAlert.sender = f.sender
	return f
}

func changedAlertRow(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='password_changed'", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func passwordChangeOperation(t *testing.T, f *fixture, id, method string) (func() error, []byte) {
	t.Helper()
	if method == "dashboard" {
		change, err := f.service.PreparePasswordChange(t.Context(), id, "one@example.net", "", false)
		if err != nil {
			t.Fatal(err)
		}
		return func() error { return f.service.CommitPasswordChange(t.Context(), change, "new-password-phc") }, nil
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email_verified_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{9}, 32)
	if method == "durable_reset" {
		var revision int64
		if err := f.pool.QueryRow(t.Context(), "SELECT password_reset_revision FROM accounts WHERE id=$1", id).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if err := f.service.queries.CreatePasswordResetToken(t.Context(), f.pool, q.CreatePasswordResetTokenParams{TokenHash: hash, AccountID: pgID(id), EmailAddress: "one@example.net", PasswordRevision: revision, ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}, Namespace: "platform", RecoveryID: pgID(uuid.Nil.String())}); err != nil {
			t.Fatal(err)
		}
		return func() error {
			got, err := f.service.ResetPasswordWithAlert(t.Context(), hash, "new-password-phc")
			if err == nil && got != id {
				t.Error("wrong reset account")
			}
			return err
		}, hash
	}
	if method != "legacy_reset" {
		t.Fatal("unknown method")
	}
	if err := state.NewPgStore(f.pool).IssueLoginToken(t.Context(), hash, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return func() error {
		got, err := f.service.ResetLegacyPasswordWithAlert(t.Context(), hash, "new-password-phc")
		if err == nil && got != id {
			t.Error("wrong reset account")
		}
		return err
	}, hash
}

func TestPasswordChangedAlertExactOffReadsOnlyOwnGate(t *testing.T) {
	for _, value := range []string{"", "0", "true", " 1", "01"} {
		var s *Service
		if err := s.ConfigurePasswordChangedAlert(t.Context(), func(k string) string {
			if k != internalmail.PasswordChangedAlertOutboxEnableEnv {
				t.Fatal("off read private configuration")
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PreparePasswordChange(t.Context(), "bad", "bad", "", false); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if err := s.CommitPasswordChange(t.Context(), nil, ""); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if _, err := s.ResetPasswordWithAlert(t.Context(), nil, ""); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if _, err := s.ResetLegacyPasswordWithAlert(t.Context(), nil, ""); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
}

func TestPGPasswordChangedAlertAtomicCommitAndRollback(t *testing.T) {
	for _, method := range []string{"dashboard", "durable_reset", "legacy_reset"} {
		for _, failure := range []string{"", "intent", "password", "hold"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				f := changedAlertFixture(t)
				id := account(t, f)
				operation, hash := passwordChangeOperation(t, f, id, method)
				switch failure {
				case "intent":
					if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_changed_alert_failure CHECK (template<>'password_changed')"); err != nil {
						t.Fatal(err)
					}
				case "password":
					if _, err := f.pool.Exec(t.Context(), "ALTER TABLE account_passwords ADD CONSTRAINT injected_changed_password_failure CHECK (false)"); err != nil {
						t.Fatal(err)
					}
				case "hold":
					if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,true)", uuid.Nil.String()); err != nil {
						t.Fatal(err)
					}
				}
				err := operation()
				if (failure == "") != (err == nil) {
					t.Fatal("unexpected mutation result", err)
				}
				var passwords, events int
				var revision int64
				if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM account_passwords WHERE account_id=$1),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_changed'),password_reset_revision FROM accounts WHERE id=$1", id).Scan(&passwords, &events, &revision); err != nil {
					t.Fatal(err)
				}
				want := 0
				if failure == "" {
					want = 1
				}
				if passwords != want || events != want || revision != int64(1+want) {
					t.Fatal("password, revision and alert did not commit atomically", passwords, events, revision)
				}
				if hash != nil {
					query := "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1"
					if method == "legacy_reset" {
						query = "SELECT consumed_at IS NOT NULL FROM login_tokens WHERE token_hash=$1"
					}
					var used bool
					if err := f.pool.QueryRow(t.Context(), query, hash).Scan(&used); err != nil || used != (failure == "") {
						t.Fatal("token did not share password/alert outcome", err)
					}
				}
				if failure != "" {
					return
				}
				r := changedAlertRow(t, f, id)
				payload, err := passwordChangedAlertPayloadFor(f.service.passwordChangedAlert, r)
				expected := "password_reset"
				if method == "dashboard" {
					expected = "dashboard"
				}
				if err != nil || payload.Method != expected || !payload.ChangedAt.Add(api.InternalMailPasswordChangedAlertTTL).Equal(r.ExpiresAt.Time) {
					t.Fatal("event lost original method/time/deadline", err)
				}
				plain, _ := json.Marshal(payload)
				var fields map[string]any
				if err := json.Unmarshal(plain, &fields); err != nil || len(fields) != 2 || fields["changed_at"] == nil || fields["method"] == nil {
					t.Fatal("alert retained a secret or unexpected metadata")
				}
				if len(r.SealedVerification) != 0 || len(r.VerificationTokenHash) != 0 || r.VerificationOrigin != "" {
					t.Fatal("alert retained a reset credential")
				}
				if err := operation(); err == nil {
					t.Fatal("proof replay emitted another event")
				}
			})
		}
	}
}

func TestPGPasswordChangedAlertDashboardProofPolicyAndRevisions(t *testing.T) {
	for _, scenario := range []string{"session", "missing_password", "wrong_password", "current_password", "step_up", "mfa_pending", "mfa_enrolled", "password_changed", "mfa_changed"} {
		t.Run(scenario, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			store := state.NewPgStore(f.pool)
			current := ""
			fresh := false
			if scenario != "session" && scenario != "mfa_pending" && scenario != "mfa_enrolled" {
				phc, err := auth.Encode(disabledTestPassword)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetAccountPassword(t.Context(), id, phc); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "wrong_password":
				current = "wrong"
			case "current_password", "password_changed", "mfa_changed":
				current = disabledTestPassword
			case "step_up":
				fresh = true
			case "mfa_pending":
				if _, err := store.SetMFARequired(t.Context(), id, true); err != nil {
					t.Fatal(err)
				}
			case "mfa_enrolled":
				if err := store.SetMFASecret(t.Context(), id, []byte("factor"), nil); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkMFAEnrolled(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			change, err := f.service.PreparePasswordChange(t.Context(), id, "one@example.net", current, fresh)
			switch scenario {
			case "missing_password", "wrong_password":
				if !errors.Is(err, ErrPasswordChangeInvalid) {
					t.Fatal(err)
				}
				return
			case "mfa_pending":
				if !errors.Is(err, ErrPasswordChangeMFARequired) {
					t.Fatal(err)
				}
				return
			case "mfa_enrolled":
				if !errors.Is(err, ErrPasswordChangeStepUp) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "current_password"
			if scenario == "session" {
				want = "session"
			}
			if fresh {
				want = "step_up"
			}
			if change.Proof() != want || change.Replacing() != (scenario != "session") {
				t.Fatal("dashboard proof policy changed")
			}
			if scenario == "password_changed" {
				if err := store.SetAccountPassword(t.Context(), id, "later-phc"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "mfa_changed" {
				if _, err := store.SetMFARequired(t.Context(), id, true); err != nil {
					t.Fatal(err)
				}
			}
			err = f.service.CommitPasswordChange(t.Context(), change, "new-password-phc")
			if strings.HasSuffix(scenario, "_changed") {
				if !errors.Is(err, ErrPasswordChangeInvalid) {
					t.Fatal("stale proof changed password", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPGPasswordChangedAlertConcurrentProofAndLateGate(t *testing.T) {
	for _, method := range []string{"dashboard", "durable_reset", "legacy_reset"} {
		t.Run(method, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			operation, _ := passwordChangeOperation(t, f, id, method)
			var wins atomic.Int32
			var wg sync.WaitGroup
			for range 2 {
				wg.Go(func() {
					if err := operation(); err == nil {
						wins.Add(1)
					} else if !errors.Is(err, ErrPasswordChangeInvalid) && !errors.Is(err, ErrPasswordResetInvalid) {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatal("concurrent proof was not single-use")
			}
		})
	}
	f := changedAlertFixture(t)
	id := account(t, f)
	operation, _ := passwordChangeOperation(t, f, id, "dashboard")
	var on atomic.Bool
	on.Store(true)
	f.service.passwordChangedAlert.enabled = on.Load
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
	go func() { done <- operation() }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late gate disable committed change", err)
	}
	var n int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM account_passwords WHERE account_id=$1", id).Scan(&n); err != nil || n != 0 {
		t.Fatal("late disable left password", err)
	}
}

func TestPGPasswordChangedAlertRetryKeepsHistoricalEventPrivate(t *testing.T) {
	f := changedAlertFixture(t)
	id := account(t, f)
	operation, _ := passwordChangeOperation(t, f, id, "dashboard")
	if err := operation(); err != nil {
		t.Fatal(err)
	}
	r := changedAlertRow(t, f, id)
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended',abuse_hold_at=clock_timestamp(),password_reset_revision=password_reset_revision+1,mfa_revision=mfa_revision+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigurePasswordChangedAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.passwordChangedAlert.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "password-changed:"+uuidString(r.ID.Bytes) {
		t.Fatal("retry rewrote historical alert")
	}
	if strings.Contains(f.sender.calls[0].message.TextBody, "new-password-phc") || strings.Contains(f.sender.calls[0].message.TextBody, "?token=") {
		t.Fatal("email contains a credential")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGPasswordChangedAlertDurableResetBindingFailures(t *testing.T) {
	for _, scenario := range []string{"namespace", "address", "revision", "status", "generation", "expiry", "used", "unverified"} {
		t.Run(scenario, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			operation, hash := passwordChangeOperation(t, f, id, "durable_reset")
			switch scenario {
			case "namespace":
				_, err := f.pool.Exec(t.Context(), "UPDATE password_reset_tokens SET namespace='other' WHERE token_hash=$1", hash)
				if err != nil {
					t.Fatal(err)
				}
			case "address":
				_, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='new@example.net' WHERE id=$1", id)
				if err != nil {
					t.Fatal(err)
				}
			case "revision":
				if err := state.NewPgStore(f.pool).SetAccountPassword(t.Context(), id, "other-phc"); err != nil {
					t.Fatal(err)
				}
			case "status":
				_, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id)
				if err != nil {
					t.Fatal(err)
				}
			case "generation":
				_, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,false)", uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
			case "expiry":
				_, err := f.pool.Exec(t.Context(), "UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", hash)
				if err != nil {
					t.Fatal(err)
				}
			case "used":
				_, err := f.pool.Exec(t.Context(), "UPDATE password_reset_tokens SET consumed_at=clock_timestamp() WHERE token_hash=$1", hash)
				if err != nil {
					t.Fatal(err)
				}
			case "unverified":
				_, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email_verified_at=NULL WHERE id=$1", id)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := operation(); !errors.Is(err, ErrPasswordResetInvalid) {
				t.Fatal("alert bypassed saved reset binding", err)
			}
			var alerts, passwords int
			var used bool
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_changed'),(SELECT count(*) FROM account_passwords WHERE account_id=$1),consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$2", id, hash).Scan(&alerts, &passwords, &used); err != nil {
				t.Fatal(err)
			}
			wantPasswords := 0
			if scenario == "revision" {
				wantPasswords = 1
			}
			if alerts != 0 || passwords != wantPasswords || used != (scenario == "used") {
				t.Fatal("invalid credential committed a partial change")
			}
		})
	}
}

func TestPGPasswordChangedAlertQuotaRollsBackAllPaths(t *testing.T) {
	for _, method := range []string{"dashboard", "durable_reset", "legacy_reset"} {
		t.Run(method, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			operation, hash := passwordChangeOperation(t, f, id, method)
			seed := queueChanged(t, f, account(t, f))
			if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,expires_at,recovery_id,sealed_security_alert,security_alert_key_id)
SELECT gen_random_uuid(),o.account_id,o.namespace,o.producer_id,o.from_address,o.recipient_address,o.template,o.template_version,o.expires_at,o.recovery_id,o.sealed_security_alert,o.security_alert_key_id FROM platform_mail_producer_outbox o CROSS JOIN generate_series(1,$2) WHERE o.id=$1`, seed.ID, api.InternalMailProducerOutboxMaxPending-1); err != nil {
				t.Fatal(err)
			}
			if err := operation(); !errors.Is(err, internalmail.ErrQuota) {
				t.Fatal("quota did not refuse atomic change", err)
			}
			var passwords int
			var revision int64
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM account_passwords WHERE account_id=$1),password_reset_revision FROM accounts WHERE id=$1", id).Scan(&passwords, &revision); err != nil || passwords != 0 || revision != 1 {
				t.Fatal("quota partially updated password", err)
			}
			if hash != nil {
				query := "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1"
				if method == "legacy_reset" {
					query = "SELECT consumed_at IS NOT NULL FROM login_tokens WHERE token_hash=$1"
				}
				var used bool
				if err := f.pool.QueryRow(t.Context(), query, hash).Scan(&used); err != nil || used {
					t.Fatal("quota consumed reset proof", err)
				}
			}
		})
	}
}

func TestPGPasswordChangedAlertGateWithdrawalDuringSaveRollsBack(t *testing.T) {
	for _, method := range []string{"dashboard", "durable_reset", "legacy_reset"} {
		t.Run(method, func(t *testing.T) {
			f := changedAlertFixture(t)
			id := account(t, f)
			operation, hash := passwordChangeOperation(t, f, id, method)
			var on atomic.Bool
			on.Store(true)
			f.service.passwordChangedAlert.enabled = on.Load
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
			go func() { done <- operation() }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			on.Store(false)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
				t.Fatal("late withdrawal committed password", err)
			}
			var passwords, alerts int
			var revision int64
			if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM account_passwords WHERE account_id=$1),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='password_changed'),password_reset_revision FROM accounts WHERE id=$1", id).Scan(&passwords, &alerts, &revision); err != nil || passwords != 0 || alerts != 0 || revision != 1 {
				t.Fatal("late withdrawal partially committed", err)
			}
			if hash != nil {
				query := "SELECT consumed_at IS NOT NULL FROM password_reset_tokens WHERE token_hash=$1"
				if method == "legacy_reset" {
					query = "SELECT consumed_at IS NOT NULL FROM login_tokens WHERE token_hash=$1"
				}
				var used bool
				if err := f.pool.QueryRow(ctx, query, hash).Scan(&used); err != nil || used {
					t.Fatal("late withdrawal consumed proof", err)
				}
			}
		})
	}
}
