package mailoutbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

func mfaAlertFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.MFARecoveryAlertOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	if err := f.service.ConfigureMFARecoveryAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.mfaRecoveryAlert.sender = f.sender
	return f
}

func mfaAlertAccount(t *testing.T, f *fixture, count int) (string, [][]byte) {
	t.Helper()
	id := account(t, f)
	hashes := make([][]byte, count)
	for i := range hashes {
		hashes[i] = bytes.Repeat([]byte{byte(i + 1)}, authcode.HMACSize)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET mfa_enrolled_at=clock_timestamp(),mfa_recovery_codes_hash=$2 WHERE id=$1", id, hashes); err != nil {
		t.Fatal(err)
	}
	return id, hashes
}

func queueMFAAlert(t *testing.T, f *fixture, id string, hash []byte) q.PlatformMailProducerOutbox {
	t.Helper()
	matched, last, _, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hash)
	if err != nil || !matched || last {
		t.Fatal("recovery event was not saved", err)
	}
	var intent string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='mfa_recovery_used' ORDER BY created_at DESC LIMIT 1", id).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	return row(t, f, intent)
}

func assertMFAAlertErased(t *testing.T, r q.PlatformMailProducerOutbox) {
	t.Helper()
	assertVerificationErased(t, r)
	if len(r.SealedSecurityAlert) != 0 || r.SecurityAlertKeyID != "" {
		t.Fatal("terminal intent retained its security event payload")
	}
}

func TestMFARecoveryAlertExactGateReadsNoPrivateConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.MFARecoveryAlertOutboxEnableEnv {
				t.Fatal("disabled alert read private configuration", k)
			}
			return flag
		}
		var service *Service
		if err := service.ConfigureMFARecoveryAlert(t.Context(), getenv); err != nil || internalmail.MFARecoveryAlertOutboxEnabled(getenv) {
			t.Fatal("nonexact alert gate enabled", err)
		}
		if _, _, _, err := service.ConsumeMFARecoveryAlert(t.Context(), "invalid", "invalid", nil); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal(err)
		}
	}
	var service *Service
	if service.ConfigureMFARecoveryAlert(t.Context(), func(string) string { return "1" }) == nil {
		t.Fatal("alert configured without parent outbox")
	}
}

func TestPGMFARecoveryAlertCodeAndIntentCommitTogether(t *testing.T) {
	for _, failure := range []string{"intent", "code_write"} {
		t.Run(failure, func(t *testing.T) {
			f := mfaAlertFixture(t)
			id, hashes := mfaAlertAccount(t, f, 3)
			constraint := "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_mfa_alert_failure CHECK (template<>'mfa_recovery_used')"
			if failure == "code_write" {
				constraint = "ALTER TABLE accounts ADD CONSTRAINT injected_mfa_code_failure CHECK (cardinality(mfa_recovery_codes_hash)=3)"
			}
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hashes[0]); err == nil {
				t.Fatal("failed transaction consumed a recovery code")
			}
			var remaining, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT cardinality(mfa_recovery_codes_hash),(SELECT count(*) FROM platform_mail_producer_outbox) FROM accounts WHERE id=$1", id).Scan(&remaining, &intents); err != nil || remaining != 3 || intents != 0 {
				t.Fatal("code and notification did not roll back together", err)
			}
		})
	}
}

func TestPGMFARecoveryAlertLateDisableDoesNotConsumeCode(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	var enabled atomic.Bool
	enabled.Store(true)
	f.service.mfaRecoveryAlert.enabled = enabled.Load
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
		_, _, _, err := f.service.ConsumeMFARecoveryAlert(ctx, id, "one@example.net", hashes[0])
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	enabled.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late disabled operation committed", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT cardinality(mfa_recovery_codes_hash) FROM accounts WHERE id=$1", id).Scan(&count); err != nil || count != 3 {
		t.Fatal("late disabled operation burned a code", err)
	}
}

func TestPGMFARecoveryAlertLastCodeAndConcurrentUse(t *testing.T) {
	for _, sameCode := range []bool{false, true} {
		t.Run(map[bool]string{false: "different_codes", true: "same_code"}[sameCode], func(t *testing.T) {
			f := mfaAlertFixture(t)
			id, hashes := mfaAlertAccount(t, f, 2)
			var wins atomic.Int32
			var wg sync.WaitGroup
			for i := range 2 {
				hash := hashes[i]
				if sameCode {
					hash = hashes[0]
				}
				wg.Go(func() {
					matched, last, _, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hash)
					if err != nil {
						t.Error(err)
					} else if matched && !last {
						wins.Add(1)
					}
				})
			}
			wg.Wait()
			var remaining, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT cardinality(mfa_recovery_codes_hash),(SELECT count(*) FROM platform_mail_producer_outbox) FROM accounts WHERE id=$1", id).Scan(&remaining, &intents); err != nil || remaining != 1 || intents != 1 || wins.Load() != 1 {
				t.Fatal("concurrent recovery duplicated an event or burned the last code", err)
			}
		})
	}
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 1)
	matched, last, remaining, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hashes[0])
	if err != nil || !matched || !last || remaining != 1 {
		t.Fatal("last-code refusal contract changed", err)
	}
	if matched, _, _, err := f.service.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", bytes.Repeat([]byte{99}, authcode.HMACSize)); err != nil || matched {
		t.Fatal("unknown recovery code matched", err)
	}
}

func TestPGMFARecoveryAlertRetryPreservesHistoricalEventAndSecrets(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	r := queueMFAAlert(t, f, id, hashes[0])
	payload, err := mfaRecoveryAlertPayloadFor(f.service.mfaRecoveryAlert, r)
	if err != nil || payload.Remaining != 2 || !payload.UsedAt.Add(api.InternalMailMFARecoveryAlertTTL).Equal(r.ExpiresAt.Time) {
		t.Fatal("saved event lost its timestamp, count or deadline", err)
	}
	if len(r.VerificationTokenHash) != 0 || len(r.SealedVerification) != 0 {
		t.Fatal("alert contains credential-family material")
	}
	for _, hash := range hashes {
		if bytes.Contains(r.SealedSecurityAlert, hash) {
			t.Fatal("alert intent contains a recovery hash")
		}
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error {
		return &mail.TransientError{Err: errors.New("lost acknowledgement")}
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Later credential changes must not rewrite or cancel a historical event.
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET mfa_enrolled_at=NULL,mfa_recovery_codes_hash=NULL,password_reset_revision=password_reset_revision+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureMFARecoveryAlert(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.mfaRecoveryAlert.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) {
		t.Fatal("retry changed the saved event or admission identity")
	}
	wantSubject, wantBody := mail.RecoveryCodeBurnedBodyV1("one@example.net", 2, payload.UsedAt)
	message := f.sender.calls[0].message
	if message.Subject != wantSubject || message.TextBody != wantBody || message.MessageID != "mfa-recovery-alert:"+uuidString(r.ID.Bytes) {
		t.Fatal("alert did not render the immutable event")
	}
	for _, hash := range hashes {
		if strings.Contains(message.TextBody, hex.EncodeToString(hash)) || strings.Contains(message.TextBody, base64.RawURLEncoding.EncodeToString(hash)) {
			t.Fatal("alert exposed recovery-code material")
		}
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
	if matched, _, _, err := restarted.ConsumeMFARecoveryAlert(t.Context(), id, "one@example.net", hashes[0]); err == nil && matched {
		t.Fatal("recovery event replay created another intent")
	}
}

func TestPGMFARecoveryAlertPrivateInspectionOmitsPayload(t *testing.T) {
	f := mfaAlertFixture(t)
	id, hashes := mfaAlertAccount(t, f, 3)
	r := queueMFAAlert(t, f, id, hashes[0])
	store, err := internalmail.NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	service := &internalmail.Service{Store: store, Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true }}
	item, err := service.ProducerOutboxItem(t.Context(), uuidString(r.ID.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"one@example.net", "sealed_security_alert", "security_alert_key_id", r.SecurityAlertKeyID, "remaining", "used_at"} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatal("inspection exposed security event payload")
		}
	}
}
