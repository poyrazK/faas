package mailoutbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func TestPGQuotaWarningAtomicFailures(t *testing.T) {
	for _, mode := range []string{"intent", "stamp", "receipt", "cancel", "quota", "held"} {
		t.Run(mode, func(t *testing.T) {
			f := quotaFixture(t)
			id, at := quotaAccount(t, f, api.PlanPro, quotaWarningTestUsage(api.PlanPro))
			var inject string
			switch mode {
			case "intent":
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_quota_warning_intent CHECK(template<>'quota_warning')"
			case "stamp":
				inject = "ALTER TABLE accounts ADD CONSTRAINT reject_quota_warning_status CHECK(last_quota_warning_at IS NULL)"
			case "receipt":
				inject = "ALTER TABLE billing_quota_warnings ADD CONSTRAINT reject_quota_warning_receipt CHECK(false)"
			case "held":
				inject = "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform','00000000-0000-0000-0000-000000000000',true)"
			case "cancel":
				_, _, old, _ := queueQuotaWarning(t, f)
				if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET account_id=$1,quota_warning_day=quota_warning_day-interval '1 day' WHERE id=$2", id, old.ID); err != nil {
					t.Fatal(err)
				}
				inject = "ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT reject_quota_cancel CHECK(state<>'cancelled')"
			case "quota":
				seed := schedule(t, f, account(t, f))
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO platform_mail_producer_outbox(id,account_id,namespace,producer_id,from_address,recipient_address,template,template_version,deletion_requested_at,restore_until,expires_at,recovery_id)
SELECT gen_random_uuid(),o.account_id,o.namespace,o.producer_id,o.from_address,o.recipient_address,o.template,o.template_version,o.deletion_requested_at-i*interval '1 second',o.restore_until,o.expires_at,o.recovery_id FROM platform_mail_producer_outbox o CROSS JOIN generate_series(1,$2) i WHERE o.id=$1`, seed.ID, api.InternalMailProducerOutboxMaxPending-1); err != nil {
					t.Fatal(err)
				}
			}
			if inject != "" {
				if _, err := f.pool.Exec(t.Context(), inject); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.service.WarnQuotaWithNotice(t.Context(), id, at, api.PlanPro, uuid.NewString()); err == nil {
				t.Fatal("failed paired warning write was acknowledged")
			}
			var stamped bool
			var revision int64
			var receipts, intents int
			if err := f.pool.QueryRow(t.Context(), "SELECT last_quota_warning_at IS NOT NULL,quota_mail_revision,(SELECT count(*) FROM billing_quota_warnings WHERE account_id=$1),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='quota_warning' AND account_id=$1 AND quota_warning_day=$2) FROM accounts WHERE id=$1", id, at).Scan(&stamped, &revision, &receipts, &intents); err != nil || stamped || revision != 2 || receipts != 0 || intents != 0 {
				t.Fatal("partial quota warning committed", stamped, revision, receipts, intents, err)
			}
			if mode == "cancel" {
				var state string
				if err := f.pool.QueryRow(t.Context(), "SELECT state FROM platform_mail_producer_outbox WHERE account_id=$1", id).Scan(&state); err != nil || state != "queued" {
					t.Fatal("failed transaction cancelled warning", err)
				}
			}
		})
	}
}

func TestPGQuotaWarningWithdrawalRollsBack(t *testing.T) {
	f := quotaFixture(t)
	id, at := quotaAccount(t, f, api.PlanPro, quotaWarningTestUsage(api.PlanPro))
	var on atomic.Bool
	on.Store(true)
	f.service.quotaWarning.enabled = on.Load
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
	go func() {
		_, err := f.service.WarnQuotaWithNotice(ctx, id, at, api.PlanPro, uuid.NewString())
		done <- err
	}()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	on.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	var stamped bool
	var n int
	if err := f.pool.QueryRow(ctx, "SELECT last_quota_warning_at IS NOT NULL,(SELECT count(*) FROM billing_quota_warnings) FROM accounts WHERE id=$1", id).Scan(&stamped, &n); err != nil || stamped || n != 0 {
		t.Fatal("withdrawal partially stamped quota warning", stamped, n, err)
	}
}

func TestPGQuotaWarningReadersReconciliationAndRollback(t *testing.T) {
	f := quotaFixture(t)
	id, _, first, _ := queueQuotaWarning(t, f)
	old, oldPath := f.service, f.env[VerificationIdentityEnv]
	_, path := verificationIdentity(t)
	f.env[VerificationIdentityEnv], f.env[VerificationPreviousIdentityEnv] = path, oldPath
	next, err := NewFromEnv(f.pool, func(k string) string { return f.env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	next.quotaWarning.sender = f.sender
	f.service = next
	_, _, second, _ := queueQuotaWarning(t, f)
	if err := next.ConfigureQuotaWarning(t.Context(), func(k string) string {
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
	_, _, intent, _ := queueQuotaWarning(t, f)
	input := reconciliationInput(intent, admitFixture(t, f, intent))
	owner := reconciler(t, f).WithAccountRestored(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("sibling reconciled quota warning", err)
	}
	owner = owner.WithQuotaWarning(func() bool { return true })
	if _, err := owner.Acknowledge(t.Context(), uuidString(intent.ID.Bytes), input); err != nil {
		t.Fatal(err)
	}
	blocked := owner.WithQuotaWarning(func() bool { return false })
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
	_, down := migrationFileParts(t, quotaMigration)
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded quota warning history")
	}
}

func TestPGQuotaWarningReadinessAndUnusedRollback(t *testing.T) {
	f := quotaFixture(t)
	up, down := migrationFileParts(t, quotaMigration)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE accounts DISABLE TRIGGER accounts_quota_mail_revision_guard"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("disabled lifecycle trigger accepted")
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE accounts ENABLE TRIGGER accounts_quota_mail_revision_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err == nil {
		t.Fatal("old schema accepted")
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
}
