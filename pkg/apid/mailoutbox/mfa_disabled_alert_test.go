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

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

const disabledTestPassword = "Correct!Horse-Battery-Staple-2026"

func disabledAlertFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.MFADisabledAlertOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.mfaDisabledAlert.sender = f.sender
	return f
}

func disabledAlertRow(t *testing.T, f *fixture, id string) q.PlatformMailProducerOutbox {
	t.Helper()
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='mfa_disabled'", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func disabledOperation(t *testing.T, f *fixture, id, method string) (func() error, []byte) {
	t.Helper()
	switch method {
	case "password":
		phc, err := auth.Encode(disabledTestPassword)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.NewPgStore(f.pool).SetAccountPassword(t.Context(), id, phc); err != nil {
			t.Fatal(err)
		}
		return func() error {
			return f.service.DisableMFAPasswordWithAlert(t.Context(), id, "one@example.net", disabledTestPassword)
		}, nil
	case "recovery_code":
		var hashes [][]byte
		if err := f.pool.QueryRow(t.Context(), "SELECT mfa_recovery_codes_hash FROM accounts WHERE id=$1", id).Scan(&hashes); err != nil {
			t.Fatal(err)
		}
		return func() error {
			return f.service.DisableMFARecoveryWithAlert(t.Context(), id, "one@example.net", hashes[0])
		}, nil
	case "durable_email":
		f.env[internalmail.MFADisableOutboxEnableEnv] = "1"
		f.env[MFADisableOriginEnv] = "https://dashboard.example.test"
		if err := f.service.ConfigureMFADisable(t.Context(), func(k string) string { return f.env[k] }); err != nil {
			t.Fatal(err)
		}
		r := queueDisable(t, f, id)
		readyDisable(t, f, r)
		return func() error {
			_, err := f.service.ConfirmMFADisableWithAlert(t.Context(), id, r.VerificationTokenHash)
			return err
		}, r.VerificationTokenHash
	case "legacy_email":
		if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET mfa_enrolled_at=clock_timestamp()-interval '26 hours' WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		hash := bytes.Repeat([]byte{9}, 32)
		if err := state.NewPgStore(f.pool).IssueMFADisableRequest(t.Context(), hash, id, time.Now().UTC().Add(-25*time.Hour)); err != nil {
			t.Fatal(err)
		}
		return func() error {
			_, err := f.service.ConfirmLegacyMFADisableWithAlert(t.Context(), id, "one@example.net", hash)
			return err
		}, hash
	default:
		t.Fatal("unknown fixture method")
		return nil, nil
	}
}

func TestMFADisabledAlertExactGateReadsNoPrivateConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.MFADisabledAlertOutboxEnableEnv {
				t.Fatal("disabled capability read configuration", k)
			}
			return flag
		}
		var s *Service
		if err := s.ConfigureMFADisabledAlert(t.Context(), getenv); err != nil || internalmail.MFADisabledAlertOutboxEnabled(getenv) {
			t.Fatal(err)
		}
		if err := s.DisableMFAPasswordWithAlert(t.Context(), "bad", "bad", "bad"); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if err := s.DisableMFARecoveryWithAlert(t.Context(), "bad", "bad", nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if _, err := s.ConfirmMFADisableWithAlert(t.Context(), "bad", nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
		if _, err := s.ConfirmLegacyMFADisableWithAlert(t.Context(), "bad", "bad", nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
}

func TestPGMFADisabledAlertAllMethodsCommitTogether(t *testing.T) {
	for _, method := range []string{"password", "recovery_code", "durable_email", "legacy_email"} {
		t.Run(method, func(t *testing.T) {
			f := disabledAlertFixture(t)
			id := disableAccount(t, f)
			if _, err := state.NewPgStore(f.pool).SetMFARequired(t.Context(), id, true); err != nil {
				t.Fatal(err)
			}
			operation, _ := disabledOperation(t, f, id, method)
			if err := operation(); err != nil {
				t.Fatal(err)
			}
			r := disabledAlertRow(t, f, id)
			payload, err := mfaDisabledAlertPayloadFor(f.service.mfaDisabledAlert, r)
			want := method
			if strings.HasSuffix(method, "email") {
				want = "email"
			}
			if err != nil || payload.Method != want || payload.DisabledAt.IsZero() || !payload.DisabledAt.Add(api.InternalMailMFADisabledAlertTTL).Equal(r.ExpiresAt.Time) {
				t.Fatal("event lost original time/method/deadline", err)
			}
			var cleared, required bool
			if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NULL AND mfa_secret_encrypted IS NULL AND mfa_recovery_codes_hash IS NULL,mfa_required FROM accounts WHERE id=$1", id).Scan(&cleared, &required); err != nil || !cleared || !required {
				t.Fatal("factor clear changed policy", err)
			}
			if len(r.SealedVerification) != 0 || len(r.VerificationTokenHash) != 0 || r.VerificationKeyID != "" || r.VerificationOrigin != "" {
				t.Fatal("security alert contains credentials")
			}
			if err := operation(); err == nil {
				t.Fatal("repeat removal emitted another event")
			}
		})
	}
}

func TestPGMFADisabledAlertAtomicRollbackAllMethods(t *testing.T) {
	for _, method := range []string{"password", "recovery_code", "durable_email", "legacy_email"} {
		for _, failure := range []string{"intent", "clear"} {
			t.Run(method+"/"+failure, func(t *testing.T) {
				f := disabledAlertFixture(t)
				id := disableAccount(t, f)
				operation, hash := disabledOperation(t, f, id, method)
				constraint := "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_disabled_alert_failure CHECK (template<>'mfa_disabled')"
				if failure == "clear" {
					constraint = "ALTER TABLE accounts ADD CONSTRAINT injected_disabled_clear_failure CHECK (mfa_enrolled_at IS NOT NULL)"
				}
				if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
					t.Fatal(err)
				}
				if err := operation(); err == nil {
					t.Fatal("injected save failure succeeded")
				}
				var enrolled bool
				var events int
				if err := f.pool.QueryRow(t.Context(), "SELECT mfa_enrolled_at IS NOT NULL AND cardinality(mfa_recovery_codes_hash)=1,(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_disabled') FROM accounts WHERE id=$1", id).Scan(&enrolled, &events); err != nil || !enrolled || events != 0 {
					t.Fatal("removal and notification did not roll back", err)
				}
				if strings.HasSuffix(method, "email") {
					var used bool
					query := "SELECT consumed_at IS NOT NULL FROM mfa_disable_tokens WHERE token_hash=$1"
					if method == "legacy_email" {
						query = "SELECT consumed_at IS NOT NULL FROM mfa_disable_requests WHERE token_hash=$1"
					}
					if err := f.pool.QueryRow(t.Context(), query, hash).Scan(&used); err != nil || used {
						t.Fatal("failed alert consumed email proof", err)
					}
				}
			})
		}
	}
}

func TestPGMFADisabledAlertFinalCodeConcurrentRemoval(t *testing.T) {
	f := disabledAlertFixture(t)
	id := disableAccount(t, f)
	operation, _ := disabledOperation(t, f, id, "recovery_code")
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			err := operation()
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrMFADisableInvalid) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var events int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='mfa_disabled'").Scan(&events); err != nil || events != 1 || wins.Load() != 1 {
		t.Fatal("concurrent final code use duplicated removal", err)
	}
}

func TestPGMFADisabledAlertRetryKeepsHistoricalEventPrivate(t *testing.T) {
	f := disabledAlertFixture(t)
	id := disableAccount(t, f)
	operation, _ := disabledOperation(t, f, id, "recovery_code")
	if err := operation(); err != nil {
		t.Fatal(err)
	}
	r := disabledAlertRow(t, f, id)
	payload, err := mfaDisabledAlertPayloadFor(f.service.mfaDisabledAlert, r)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(payload)
	if bytes.Contains(plain, bytes.Repeat([]byte{1}, 32)) || strings.Contains(string(plain), "token") || len(r.SealedSecurityAlert) == 0 {
		t.Fatal("alert contains a credential")
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Re-enrollment/password changes and a later abuse hold must not rewrite history.
	if err := state.NewPgStore(f.pool).SetMFASecret(t.Context(), id, []byte("new-factor"), nil); err != nil {
		t.Fatal(err)
	}
	if err := state.NewPgStore(f.pool).MarkMFAEnrolled(t.Context(), id); err != nil {
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
	if err := restarted.ConfigureMFADisabledAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.mfaDisabledAlert.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "mfa-disabled:"+uuidString(r.ID.Bytes) {
		t.Fatal("retry changed event or stable admission key")
	}
	subject, body := mail.MFADisabledBodyV1(r.RecipientAddress, payload.Method, payload.DisabledAt)
	if f.sender.calls[0].message.Subject != subject || f.sender.calls[0].message.TextBody != body {
		t.Fatal("frozen renderer changed")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGMFADisabledAlertProofAndLateGateChanges(t *testing.T) {
	for _, scenario := range []string{"password_changed", "factor_changed", "flag_off"} {
		t.Run(scenario, func(t *testing.T) {
			f := disabledAlertFixture(t)
			id := disableAccount(t, f)
			_, _ = disabledOperation(t, f, id, "password")
			var enabled atomic.Bool
			enabled.Store(true)
			f.service.mfaDisabledAlert.enabled = enabled.Load
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
				done <- f.service.DisableMFAPasswordWithAlert(ctx, id, "one@example.net", disabledTestPassword)
			}()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			switch scenario {
			case "password_changed":
				if err := state.NewPgStore(f.pool).SetAccountPassword(ctx, id, "new-phc"); err != nil {
					t.Fatal(err)
				}
			case "factor_changed":
				if err := state.NewPgStore(f.pool).SetMFASecret(ctx, id, []byte("replacement"), nil); err != nil {
					t.Fatal(err)
				}
			case "flag_off":
				enabled.Store(false)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			want := ErrMFADisableInvalid
			if scenario == "flag_off" {
				want = internalmail.ErrDisabled
			}
			if !errors.Is(err, want) {
				t.Fatal("stale proof or late flag disable removed factor", err)
			}
			var enrolled bool
			if err := f.pool.QueryRow(ctx, "SELECT mfa_enrolled_at IS NOT NULL FROM accounts WHERE id=$1", id).Scan(&enrolled); err != nil || !enrolled {
				t.Fatal("failed proof removed MFA", err)
			}
		})
	}
}
