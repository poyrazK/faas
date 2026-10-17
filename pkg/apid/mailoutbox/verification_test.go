package mailoutbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/secretbox"
	stateq "github.com/onebox-faas/faas/pkg/state/sqlc"
)

func verificationIdentity(t *testing.T) (*age.X25519Identity, string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "verification.age")
	if err := os.WriteFile(path, []byte(id.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return id, path
}

func configureVerification(t *testing.T, f *fixture) {
	t.Helper()
	_, path := verificationIdentity(t)
	f.env[internalmail.VerificationOutboxEnableEnv] = "1"
	f.env[VerificationIdentityEnv] = path
	f.env[VerificationOriginEnv] = "https://auth.example.test"
	if err := f.service.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}

func verificationFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	configureVerification(t, f)
	f.service.verification.sender = f.sender
	return f
}

func queueVerification(t *testing.T, f *fixture, accountID string) q.PlatformMailProducerOutbox {
	t.Helper()
	if err := f.service.QueueVerification(t.Context(), accountID, "one@example.net"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='email_verification' ORDER BY created_at DESC LIMIT 1", accountID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return row(t, f, id)
}

func verificationRaw(t *testing.T, f *fixture, r q.PlatformMailProducerOutbox) []byte {
	t.Helper()
	binding, raw, err := secretbox.OpenBytes(f.service.verification.readers[r.VerificationKeyID], r.SealedVerification)
	if err != nil || binding != verificationBinding(r) || len(raw) != api.EmailVerificationTokenBytes {
		t.Fatal("queued credential did not decrypt in its original context", err)
	}
	return raw
}

func assertVerificationErased(t *testing.T, r q.PlatformMailProducerOutbox) {
	t.Helper()
	if r.RecipientAddress != "" || len(r.SealedVerification) != 0 || len(r.VerificationTokenHash) != 0 || r.VerificationKeyID != "" || r.VerificationOrigin != "" {
		t.Fatal("terminal handoff retained credential material")
	}
}

func TestVerificationOutboxExactGateReadsNoPrivateConfiguration(t *testing.T) {
	for _, flag := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.VerificationOutboxEnableEnv {
				t.Fatal("disabled verification read private configuration", k)
			}
			return flag
		}
		var s *Service
		if err := s.ConfigureVerification(t.Context(), getenv); err != nil || internalmail.VerificationOutboxEnabled(getenv) {
			t.Fatal("nonexact gate enabled verification", err)
		}
		if err := s.QueueVerification(t.Context(), "invalid", "invalid"); !errors.Is(err, internalmail.ErrDisabled) {
			t.Fatal("disabled issuance touched state", err)
		}
	}
	var s *Service
	if err := s.ConfigureVerification(t.Context(), func(string) string { return "1" }); err == nil {
		t.Fatal("verification enabled without parent producer outbox")
	}
	for _, origin := range []string{"", "http://auth.example.test", "https://user:password@auth.example.test", "https://auth.example.test/path", "https://auth.example.test?token=private", "https://auth.example.test/#secret", "https://auth.example.test#", "https://:443", "https://auth.example.test:0", "https://auth.example.test:99999"} {
		if _, err := verificationOrigin(origin); err == nil {
			t.Fatal("noncanonical verification origin accepted")
		}
	}
	if got, err := verificationOrigin("https://AUTH.example.test/"); err != nil || got != "https://auth.example.test" {
		t.Fatal("canonical origin normalization failed", err)
	}
}

func TestPGVerificationOutboxAtomicIssuanceAndLateDisable(t *testing.T) {
	for _, table := range []string{"email_verification_tokens", "platform_mail_producer_outbox"} {
		t.Run(table, func(t *testing.T) {
			f := verificationFixture(t)
			id := account(t, f)
			constraint := "ALTER TABLE email_verification_tokens ADD CONSTRAINT injected_failure CHECK (email_address='')"
			if table == "platform_mail_producer_outbox" {
				constraint = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT injected_failure CHECK (template<>'email_verification')"
			}
			if _, err := f.pool.Exec(t.Context(), constraint); err != nil {
				t.Fatal(err)
			}
			if err := f.service.QueueVerification(t.Context(), id, "one@example.net"); err == nil {
				t.Fatal("injected failure committed verification")
			}
			var tokens, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM email_verification_tokens),(SELECT count(*) FROM platform_mail_producer_outbox)").Scan(&tokens, &intents); err != nil || tokens != 0 || intents != 0 {
				t.Fatal("token and intent did not roll back together", err)
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("business transaction performed a handoff")
			}
		})
	}
	f := verificationFixture(t)
	id := account(t, f)
	var on atomic.Bool
	on.Store(true)
	f.service.verification.enabled = on.Load
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
	go func() { done <- f.service.QueueVerification(ctx, id, "one@example.net") }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late disable committed a credential", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM email_verification_tokens").Scan(&count); err != nil || count != 0 {
		t.Fatal("late disable retained token", err)
	}
}

func TestPGVerificationOutboxRestartReusesEncryptedTokenAndExpiry(t *testing.T) {
	f := verificationFixture(t)
	r := queueVerification(t, f, account(t, f))
	id := uuidString(r.ID.Bytes)
	raw := verificationRaw(t, f, r)
	defer clear(raw)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if bytes.Contains(r.SealedVerification, raw) || bytes.Contains(r.SealedVerification, []byte(encoded)) || !bytes.Equal(api.HashToken(raw), r.VerificationTokenHash) {
		t.Fatal("queued token is not sealed or bound to its hash")
	}
	var expires time.Time
	var address string
	if err := f.pool.QueryRow(t.Context(), "SELECT expires_at,email_address FROM email_verification_tokens WHERE token_hash=$1", api.HashToken(raw)).Scan(&expires, &address); err != nil || !expires.Equal(r.ExpiresAt.Time) || address != "one@example.net" {
		t.Fatal("token and intent disagree", err)
	}
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error {
		return &mail.TransientError{Err: errors.New("lost acknowledgement")}
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	queued := row(t, f, id)
	if queued.State != "queued" || !bytes.Equal(queued.SealedVerification, r.SealedVerification) {
		t.Fatal("ambiguous handoff discarded saved credential")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	restarted.verification.sender = f.sender
	f.sender.send = nil
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) {
		t.Fatal("restart changed the original verification notification")
	}
	if !strings.Contains(f.sender.calls[0].message.TextBody, "https://auth.example.test"+verificationPathV1+"?token="+encoded) || f.sender.calls[0].message.MessageID != "verify-email:"+id {
		t.Fatal("handoff did not use its saved canonical link and stable key")
	}
	got := row(t, f, id)
	if got.State != "admitted" || got.Attempts != 2 || got.ExpiresAt != r.ExpiresAt {
		t.Fatal("acknowledgement changed expiry or retry accounting")
	}
	assertVerificationErased(t, got)
	if _, err := stateq.New().ConsumeEmailVerificationToken(t.Context(), f.pool, api.HashToken(raw)); err != nil {
		t.Fatal("handed-off token could not verify its original address", err)
	}
	if _, err := stateq.New().ConsumeEmailVerificationToken(t.Context(), f.pool, api.HashToken(raw)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("verification token was not single-use", err)
	}
}

func TestPGVerificationOutboxCancelsStaleCredentialsAndErasesCiphertext(t *testing.T) {
	for _, tc := range []struct{ name, mutation, state, code string }{
		{"changed address", "UPDATE accounts SET email='changed@example.net'", "cancelled", "account_changed"},
		{"verified", "UPDATE accounts SET email_verified_at=clock_timestamp()", "cancelled", "token_unavailable"},
		{"inactive", "UPDATE accounts SET status='deleted_pending'", "cancelled", "token_unavailable"},
		{"consumed", "UPDATE email_verification_tokens SET consumed_at=clock_timestamp()", "cancelled", "token_unavailable"},
		{"missing token", "DELETE FROM email_verification_tokens", "cancelled", "token_unavailable"},
		{"expired", "UPDATE platform_mail_producer_outbox SET expires_at=clock_timestamp()-interval '1 second'; UPDATE email_verification_tokens SET expires_at=clock_timestamp()-interval '1 second'", "expired", "notification_expired"},
		{"suppressed", "INSERT INTO mail_suppressions(email) VALUES('one@example.net')", "suppressed", "platform_suppressed"},
		{"corrupted payload", "UPDATE platform_mail_producer_outbox SET sealed_verification=decode(repeat('0f',128),'hex')", "failed", "payload_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := verificationFixture(t)
			r := queueVerification(t, f, account(t, f))
			if _, err := f.pool.Exec(t.Context(), tc.mutation); err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			if got.State != tc.state || got.Code != tc.code || len(f.sender.calls) != 0 {
				t.Fatal("stale credential was handed off", got.State, got.Code)
			}
			assertVerificationErased(t, got)
		})
	}
}

func TestPGVerificationOutboxHoldsOnGateOriginAndRecoveryChanges(t *testing.T) {
	for _, change := range []string{"gate", "origin", "recovery"} {
		t.Run(change, func(t *testing.T) {
			f := verificationFixture(t)
			r := queueVerification(t, f, account(t, f))
			switch change {
			case "gate":
				f.env[internalmail.VerificationOutboxEnableEnv] = "0"
			case "origin":
				f.service.verification.origin = "https://different.example.test"
			case "recovery":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state VALUES('platform',gen_random_uuid(),false)"); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			if got.State != "queued" || got.Attempts != 0 || !bytes.Equal(got.SealedVerification, r.SealedVerification) || len(f.sender.calls) != 0 {
				t.Fatal("a disabled or quarantined verification was claimed")
			}
		})
	}
}

func TestPGVerificationOutboxReaderOverlapAndResourceBinding(t *testing.T) {
	f := verificationFixture(t)
	first := queueVerification(t, f, account(t, f))
	second := queueVerification(t, f, account(t, f))
	_, nextPath := verificationIdentity(t)
	f.env[VerificationPreviousIdentityEnv] = f.env[VerificationIdentityEnv]
	f.env[VerificationIdentityEnv] = nextPath
	restarted, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	previous := f.env[VerificationPreviousIdentityEnv]
	delete(f.env, VerificationPreviousIdentityEnv)
	if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("enabled startup accepted missing pending readers")
	}
	f.env[VerificationPreviousIdentityEnv] = previous
	if err := restarted.ConfigureVerification(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal("reader overlap could not resume pending work", err)
	}
	restarted.verification.sender = f.sender
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_verification=$1 WHERE id=$2", first.SealedVerification, second.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := restarted.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := row(t, f, uuidString(first.ID.Bytes)); got.State != "admitted" {
		t.Fatal("previous reader did not open the original intent")
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" {
		t.Fatal("copied ciphertext escaped its resource binding")
	}
	if len(f.sender.calls) != 1 {
		t.Fatal("copied credential was sent")
	}
	// An older still-running worker knows only A. A newer writer uses B with
	// A as a reader; the old worker must leave B's intent available for it.
	if err := restarted.QueueVerification(t.Context(), account(t, f), "one@example.net"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 {
		t.Fatal("older reader claimed work encrypted by a newer writer")
	}
	if err := restarted.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 {
		t.Fatal("new writer could not hand off its own pending verification")
	}
}

func TestPGVerificationOutboxConsumptionAndHandoffShareLockOrder(t *testing.T) {
	for _, winner := range []string{"handoff", "consumption"} {
		t.Run(winner, func(t *testing.T) {
			f := verificationFixture(t)
			r := queueVerification(t, f, account(t, f))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if winner == "consumption" {
				tx, err := f.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rollback(ctx, tx)
				if _, err := stateq.New().ConsumeEmailVerificationToken(ctx, tx, r.VerificationTokenHash); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- f.service.DeliverOne(ctx) }()
				waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-done; err != nil {
					t.Fatal("consumption and handoff deadlocked", err)
				}
				if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "cancelled" || len(f.sender.calls) != 0 {
					t.Fatal("consumed token was sent")
				}
				return
			}
			started, release := make(chan struct{}), make(chan struct{})
			f.sender.send = func(ctx context.Context, _ mail.Message, _ internalmail.MessageTiming) error {
				close(started)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			delivered := make(chan error, 1)
			go func() { delivered <- f.service.DeliverOne(ctx) }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("handoff did not start")
			}
			consumer, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Release()
			consumed := make(chan error, 1)
			go func() {
				_, err := stateq.New().ConsumeEmailVerificationToken(ctx, consumer, r.VerificationTokenHash)
				consumed <- err
			}()
			for {
				var blocked bool
				if err := f.pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1::int))>0", consumer.Conn().PgConn().PID()).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
			}
			close(release)
			if err := <-delivered; err != nil {
				t.Fatal(err)
			}
			if err := <-consumed; err != nil {
				t.Fatal("consumer did not finish after admitted handoff", err)
			}
			if len(f.sender.calls) != 1 {
				t.Fatal("handoff duplicated during consumption")
			}
		})
	}
}

func TestPGVerificationOutboxAddressBindingSurvivesHandoff(t *testing.T) {
	f := verificationFixture(t)
	r := queueVerification(t, f, account(t, f))
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='changed@example.net' WHERE id=$1", r.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := stateq.New().ConsumeEmailVerificationToken(t.Context(), f.pool, r.VerificationTokenHash); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("old address link verified a replacement address", err)
	}
}

func TestPGVerificationOutboxReconciliationRequiresItsGateAndErasesSecrets(t *testing.T) {
	f := verificationFixture(t)
	r := queueVerification(t, f, account(t, f))
	messageID := admitFixture(t, f, r)
	id := uuidString(r.ID.Bytes)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f)
	if _, err := owner.Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("verification reconciliation bypassed its own gate", err)
	}
	if got := row(t, f, id); !bytes.Equal(got.SealedVerification, r.SealedVerification) {
		t.Fatal("disabled acknowledgement changed ciphertext")
	}
	owner = owner.WithVerification(func() bool { return true })
	record, err := owner.Acknowledge(t.Context(), id, input)
	if err != nil || record.Template != VerificationTemplate {
		t.Fatal("confirmed verification admission did not reconcile", err)
	}
	assertVerificationErased(t, row(t, f, id))
	if _, err := owner.Acknowledge(t.Context(), id, input); err != nil {
		t.Fatal("verification audit replay failed", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", r.AccountID); err != nil {
		t.Fatal(err)
	}
	got, err := owner.Audit(t.Context(), input.RequestID)
	if err != nil || got.Template != VerificationTemplate {
		t.Fatal("verification history disappeared after erasure", err)
	}
	_, down := migrationFileParts(t, "20261016195608821_platform_mail_verification_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded verification audit or address-binding history")
	}
}

func TestPGVerificationOutboxPrivateInspectionOmitsCredentialMaterial(t *testing.T) {
	f := verificationFixture(t)
	r := queueVerification(t, f, account(t, f))
	raw := verificationRaw(t, f, r)
	defer clear(raw)
	store, err := internalmail.NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	s := &internalmail.Service{Store: store, Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true }}
	item, err := s.ProducerOutboxItem(t.Context(), uuidString(r.ID.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{base64.RawURLEncoding.EncodeToString(raw), "one@example.net", "sealed_verification", "verification_token_hash", "verification_origin", r.VerificationKeyID} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatal("private inspection exposed credential material")
		}
	}
	_, down := migrationFileParts(t, "20261016195608821_platform_mail_verification_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded pending verification intent")
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", r.AccountID); err != nil {
		t.Fatal(err)
	}
	var intents, tokens int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM platform_mail_producer_outbox),(SELECT count(*) FROM email_verification_tokens)").Scan(&intents, &tokens); err != nil || intents != 0 || tokens != 0 {
		t.Fatal("account erasure retained verification credentials", err)
	}
}
