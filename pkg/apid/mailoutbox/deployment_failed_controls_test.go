package mailoutbox

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGDeploymentFailedReadersReconciliationAndRollback(t *testing.T) {
	f := deploymentFixture(t)
	id, _, _, first := queueDeployment(t, f)
	old, oldPath := f.service, f.env[VerificationIdentityEnv]
	_, path := verificationIdentity(t)
	f.env[VerificationIdentityEnv], f.env[VerificationPreviousIdentityEnv] = path, oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureDeploymentFailed(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.deploymentFailed.sender = f.sender
	f.service = next
	_, _, _, second := queueDeployment(t, f)
	if err := next.ConfigureDeploymentFailed(t.Context(), func(k string) string {
		if k == VerificationPreviousIdentityEnv {
			return ""
		}
		return f.env[k]
	}); err == nil {
		t.Fatal("missing pending reader accepted")
	}
	for range 2 {
		if err := old.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sender.calls) != 1 || row(t, f, uuidString(second.ID.Bytes)).Attempts != 0 {
		t.Fatal("old worker claimed new-reader notice")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=$1 WHERE id=$2", first.SealedSecurityAlert, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(second.ID.Bytes)); got.State != "failed" || got.Code != "payload_unavailable" {
		t.Fatal("cipher crossed intent binding")
	}
	_, _, _, intent := queueDeployment(t, f)
	input := reconciliationInput(intent, admitFixture(t, f, intent))
	owner := reconciler(t, f).WithAccountRestored(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling reconciled deployment-failure notice", err)
	}
	owner = owner.WithDeploymentFailed(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); err != nil {
		t.Fatal(err)
	}
	blocked := owner.WithDeploymentFailed(func() bool { return false })
	if _, err := blocked.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := blocked.Audit(t.Context(), input.RequestID); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1 OR id=$2", id, intent.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Audit(t.Context(), input.RequestID); err != nil {
		t.Fatal("erasure removed opaque audit", err)
	}
	_, down := migrationFileParts(t, deploymentFailedMigration)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded deployment-failure notice history")
	}
}
