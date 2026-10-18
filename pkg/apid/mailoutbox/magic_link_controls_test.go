package mailoutbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGMagicLinkOutboxRecoveryQuarantinesCredentialAfterGlobalRelease(t *testing.T) {
	for _, held := range []bool{true, false} {
		t.Run(map[bool]string{true: "held", false: "released"}[held], func(t *testing.T) {
			f := magicFixture(t)
			r := queueMagic(t, f, magicAccount(t, f))
			if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,$2)", uuid.NewString(), held); err != nil {
				t.Fatal(err)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := row(t, f, uuidString(r.ID.Bytes)); got.Attempts != 0 || len(f.sender.calls) != 0 {
				t.Fatal("recovery admitted an older magic-link credential")
			}
			f.env[internalmail.MagicLinkOutboxEnableEnv] = "0"
			if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), r.VerificationTokenHash); !errors.Is(err, ErrMagicLinkInvalid) {
				t.Fatal("restore revived a credential after recovery or flag rollback", err)
			}
			if !held {
				f.env[internalmail.MagicLinkOutboxEnableEnv] = "1"
				fresh := queueMagic(t, f, uuidString(r.AccountID.Bytes))
				if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), fresh.VerificationTokenHash); err != nil {
					t.Fatal("fresh post-recovery magic-link could not redeem", err)
				}
			}
		})
	}
	f := magicFixture(t)
	r := queueMagic(t, f, magicAccount(t, f))
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
		_, err := NewMagicLinkCredentials(f.pool).Redeem(ctx, r.VerificationTokenHash)
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	if _, err := tx.Exec(ctx, "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,false)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrMagicLinkInvalid) {
		t.Fatal("redemption crossed a newly committed recovery generation", err)
	}
}

func TestPGMagicLinkOutboxKeyOverlapAndOriginHold(t *testing.T) {
	f := magicFixture(t)
	oldWorker := f.service
	oldPath := f.env[VerificationIdentityEnv]
	first := queueMagic(t, f, magicAccount(t, f))
	_, nextPath := verificationIdentity(t)
	f.env[VerificationIdentityEnv] = nextPath
	f.env[VerificationPreviousIdentityEnv] = oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureMagicLink(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.magicLink.sender = f.sender
	f.service = next
	second := queueMagic(t, f, magicAccount(t, f))
	if err := oldWorker.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row(t, f, uuidString(first.ID.Bytes)).State != "admitted" {
		t.Fatal("previous worker lost its readable magic-link")
	}
	if err := oldWorker.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "queued" || got.Attempts != 0 {
		t.Fatal("old worker claimed an unknown magic-link reader key")
	}
	// A newly started worker must cover every live pending key for its producer.
	previous, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := previous.ConfigureMagicLink(t.Context(), func(k string) string {
		if k == VerificationIdentityEnv {
			return oldPath
		}
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("magic-link startup accepted a missing pending reader")
	}
	origin := next.magicLink.origin
	next.magicLink.origin = "https://changed.example.test"
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.Attempts != 0 {
		t.Fatal("origin drift changed the saved magic-link link")
	}
	next.magicLink.origin = origin
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "admitted" || len(f.sender.calls) != 2 {
		t.Fatal("retained reader could not finish the original magic-link")
	}
}

func TestPGMagicLinkOutboxRejectsCopiedCipherAndPrivateInspectionLeaks(t *testing.T) {
	f := magicFixture(t)
	a := queueMagic(t, f, magicAccount(t, f))
	b := queueMagic(t, f, magicAccount(t, f))
	raw := magicRaw(t, f, a)
	defer clear(raw)
	store, err := internalmail.NewPGStore(f.pool, "platform")
	if err != nil {
		t.Fatal(err)
	}
	service := &internalmail.Service{Store: store, Enabled: func() bool { return true }, OperationsEnabled: func() bool { return true }, ProducerOutboxEnabled: func() bool { return true }}
	item, err := service.ProducerOutboxItem(t.Context(), uuidString(a.ID.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{base64.RawURLEncoding.EncodeToString(raw), "one@example.net", "verification_token_hash", "sealed_verification", "verification_origin", a.VerificationKeyID} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatal("private magic-link inspection leaked a credential")
		}
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_verification=$1 WHERE id=$2", a.SealedVerification, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMagicLinkCredentials(f.pool).Redeem(t.Context(), a.VerificationTokenHash); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := row(t, f, uuidString(b.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" || len(f.sender.calls) != 0 {
		t.Fatal("copied magic-link ciphertext crossed its intent binding", got.State, got.Code)
	}
}

func TestPGMagicLinkOutboxReconciliationGateAuditAndRollbackGuard(t *testing.T) {
	f := magicFixture(t)
	r := queueMagic(t, f, magicAccount(t, f))
	id := uuidString(r.ID.Bytes)
	messageID := admitFixture(t, f, r)
	input := reconciliationInput(r, messageID)
	owner := reconciler(t, f).WithVerification(func() bool { return true }).WithPasswordReset(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("verification gate acknowledged a magic-link admission", err)
	}
	owner = owner.WithMagicLink(func() bool { return true })
	record, err := owner.Acknowledge(t.Context(), id, input)
	if err != nil || record.Template != MagicLinkTemplate {
		t.Fatal("confirmed magic-link receipt could not reconcile", err)
	}
	assertVerificationErased(t, row(t, f, id))
	blocked := owner.WithMagicLink(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("magic-link audit replay bypassed its gate", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", r.AccountID); err != nil {
		t.Fatal(err)
	}
	if saved, err := owner.Audit(t.Context(), input.RequestID); err != nil || saved.Template != MagicLinkTemplate {
		t.Fatal("magic-link erasure lost opaque audit", err)
	}
	_, down := migrationFileParts(t, "20261017082032672_platform_mail_magic_link_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback erased magic-link audit evidence")
	}
}

func TestPGMagicLinkOutboxUnusedRollbackPreservesVerification(t *testing.T) {
	f := newFixture(t)
	configureVerification(t, f)
	f.service.verification.sender = f.sender
	r := queueVerification(t, f, account(t, f))
	raw := verificationRaw(t, f, r)
	defer clear(raw)
	up, down := migrationFileParts(t, "20261017082032672_platform_mail_magic_link_outbox.sql")
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("unused magic-link rollback failed", err)
	}
	if f.service.Ready(t.Context()) == nil {
		t.Fatal("old schema passed new outbox readiness")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 || !bytes.Contains([]byte(f.sender.calls[0].message.TextBody), []byte(base64.RawURLEncoding.EncodeToString(raw))) {
		t.Fatal("unused magic-link rollback changed verification handoff")
	}
}
