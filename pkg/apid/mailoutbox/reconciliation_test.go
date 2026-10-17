package mailoutbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func admitFixture(t *testing.T, f *fixture, intent q.PlatformMailProducerOutbox) string {
	t.Helper()
	id := uuid.NewString()
	keyPrefix := "account-delete:"
	if intent.Template == VerificationTemplate {
		keyPrefix = "verify-email:"
	}
	if intent.Template == MFADisableTemplate {
		keyPrefix = "mfa-disable:"
	}
	if intent.Template == DeploymentFailedTemplate {
		keyPrefix = "deployment-failed:"
	}
	if intent.Template == SubscriptionEndedTemplate {
		keyPrefix = "subscription-ended:"
	}
	if intent.Template == QuotaWarningTemplate {
		keyPrefix = "quota-warning:"
	}
	if intent.Template == NonpaymentDeletionTemplate {
		keyPrefix = "nonpayment-deletion:"
	}
	if intent.Template == AccountSuspendedTemplate {
		keyPrefix = "account-suspended:"
	}
	if intent.Template == AccountRestoredTemplate {
		keyPrefix = "account-restored:"
	}
	if intent.Template == PaymentFailedTemplate {
		keyPrefix = "payment-failed:"
	}
	if intent.Template == MFAEnabledAlertTemplate {
		keyPrefix = "mfa-enabled:"
	}
	if intent.Template == PasswordChangedAlertTemplate {
		keyPrefix = "password-changed:"
	}
	if intent.Template == MFADisabledAlertTemplate {
		keyPrefix = "mfa-disabled:"
	}
	if intent.Template == MFARecoveryAlertTemplate {
		keyPrefix = "mfa-recovery-alert:"
	}
	if intent.Template == MagicLinkTemplate {
		keyPrefix = "magic-link:"
	}
	if intent.Template == PasswordResetTemplate {
		keyPrefix = "password-reset:"
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO internal_mail_messages(id,namespace,producer_id,idempotency_key,sender_address,created_at,expires_at)
        VALUES($1,$2,$3,$4,'notify@example.test',clock_timestamp(),$5)`, id, intent.Namespace, intent.ProducerID, keyPrefix+uuidString(intent.ID.Bytes), intent.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recipients(id,namespace,message_id) VALUES($1,$2,$3)", uuid.NewString(), intent.Namespace, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func reconciliationInput(intent q.PlatformMailProducerOutbox, messageID string) ReconciliationRequest {
	return ReconciliationRequest{OperatorAuditInput: internalmail.OperatorAuditInput{RequestID: uuid.NewString(), Actor: "operator", Reason: "lost_acknowledgement", Evidence: "ticket:handoff-1"},
		ExpectedRevision: intent.Revision, RecoveryID: uuid.Nil.String(), MessageID: messageID}
}

func reconciler(t *testing.T, f *fixture) *Reconciler {
	t.Helper()
	r, err := NewReconciler(f.pool, "platform", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPGOutboxReconciliationAcknowledgesFailedHandoffAndReplaysAudit(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	id := uuidString(intent.ID.Bytes)
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET attempts=32 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.claim(t.Context()); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	intent = row(t, f, id)
	if intent.State != "failed" || intent.RecipientAddress != "" {
		t.Fatal("maintenance did not exhaust the handoff")
	}
	input := reconciliationInput(intent, messageID)
	review, err := internalmail.LookupProducerOutboxAdmission(t.Context(), f.pool, "platform", id)
	if err != nil || !review.CanAcknowledge || review.Receipt.MessageID != messageID {
		t.Fatal("missing original admission", review, err)
	}
	record, err := reconciler(t, f).Acknowledge(t.Context(), id, input)
	if err != nil || record.PreviousState != "failed" || record.ResultingRevision != intent.Revision+1 {
		t.Fatal("failed admission acknowledgement", record, err)
	}
	after := row(t, f, id)
	if after.State != "admitted" || after.RecipientAddress != "" || after.Code != "" || after.LeaseID.Valid || after.LeaseUntil.Valid {
		t.Fatal("acknowledgement did not settle intent")
	}
	intent.State, intent.Code, intent.UpdatedAt, intent.Revision = "admitted", "", after.UpdatedAt, after.Revision
	if !reflect.DeepEqual(intent, after) {
		t.Fatal("acknowledgement changed immutable intent, deadlines or attempt budget")
	}
	// A fresh owner process can recover a lost acknowledgement using the exact
	// request even though its original expected revision is now stale.
	for range 2 {
		got, err := reconciler(t, f).Acknowledge(t.Context(), id, input)
		if err != nil || !reflect.DeepEqual(got, record) {
			t.Fatal("request replay did not return original audit", err)
		}
	}
	changed := input
	changed.Evidence = "ticket:different"
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, changed); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("contradictory request UUID reused", err)
	}
	if _, err := reconciler(t, f).Acknowledge(t.Context(), uuid.NewString(), input); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("request UUID reused for a different intent", err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.sender.calls) != 0 {
		t.Fatal("reconciliation sent mail", err)
	}
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM accounts WHERE id=$1", intent.AccountID); err != nil {
		t.Fatal(err)
	}
	got, err := reconciler(t, f).Audit(t.Context(), input.RequestID)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatal("account erasure lost opaque operation evidence", err)
	}
	_, down := migrationFileParts(t, "20261016192204806_platform_mail_outbox_reconciliation.sql")
	if _, err := f.pool.Exec(t.Context(), down); err == nil {
		t.Fatal("rollback discarded reconciliation history")
	}
}

func TestPGOutboxReconciliationExpiredClaimFencesStaleWorker(t *testing.T) {
	f := newFixture(t)
	schedule(t, f, account(t, f))
	claimed, err := f.service.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messageID := admitFixture(t, f, claimed)
	id := uuidString(claimed.ID.Bytes)
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET lease_until=clock_timestamp()-interval '1 second',revision=revision+1 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	current := row(t, f, id)
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, reconciliationInput(current, messageID)); err != nil {
		t.Fatal("expired claim with an admission could not settle", err)
	}
	if err := f.service.dispatch(t.Context(), claimed); !errors.Is(err, internalmail.ErrLeaseLost) || len(f.sender.calls) != 0 {
		t.Fatal("old worker sent after acknowledgement", err)
	}
	if got := row(t, f, id); got.Attempts != current.Attempts || got.ExpiresAt != current.ExpiresAt || got.RecoveryID != current.RecoveryID {
		t.Fatal("expired-claim acknowledgement reset a safeguard")
	}
}

func TestPGOutboxReconciliationAcknowledgesPastAdmissionWithoutExtendingExpiry(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	id := uuidString(intent.ID.Bytes)
	// Age the accepted message and its unchanged 24-hour intent together.
	if _, err := f.pool.Exec(t.Context(), `UPDATE platform_mail_producer_outbox SET created_at=created_at-interval '2 days',
        deletion_requested_at=deletion_requested_at-interval '2 days',restore_until=restore_until-interval '2 days',
        expires_at=expires_at-interval '2 days',revision=revision+1;
        UPDATE internal_mail_messages SET created_at=created_at-interval '2 days',expires_at=expires_at-interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.claim(t.Context()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("expired intent was claimed", err)
	}
	before := row(t, f, id)
	if before.State != "expired" || before.ExpiresAt.Time.After(time.Now()) {
		t.Fatal("fixture did not expire the accepted intent")
	}
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, reconciliationInput(before, messageID)); err != nil {
		t.Fatal("historical admission could not be acknowledged", err)
	}
	after := row(t, f, id)
	if after.State != "admitted" || after.ExpiresAt != before.ExpiresAt || after.Attempts != before.Attempts || after.NextAttemptAt != before.NextAttemptAt || after.RecoveryID != before.RecoveryID {
		t.Fatal("historical acknowledgement extended a deadline or budget")
	}
	if err := f.service.DeliverOne(t.Context()); err != nil || len(f.sender.calls) != 0 {
		t.Fatal("historical acknowledgement caused a new handoff", err)
	}
}

func TestPGOutboxReconciliationRequiresExactReceiptAndPreservesFences(t *testing.T) {
	for _, tc := range []struct{ name, mutation, status, block string }{
		{"missing", "DELETE FROM internal_mail_messages", "missing", "admission_missing"},
		{"wrong namespace", "UPDATE internal_mail_messages SET namespace='elsewhere'", "missing", "admission_missing"},
		{"wrong producer", "UPDATE internal_mail_messages SET producer_id='other'", "missing", "admission_missing"},
		{"wrong key", "UPDATE internal_mail_messages SET idempotency_key='different'", "missing", "admission_missing"},
		{"wrong sender", "UPDATE internal_mail_messages SET sender_address='other@example.test'", "mismatch", "admission_mismatch"},
		{"wrong deadline", "UPDATE internal_mail_messages SET expires_at=expires_at+interval '1 second'", "mismatch", "admission_mismatch"},
		{"scheduled", "UPDATE internal_mail_messages SET send_at=clock_timestamp()", "mismatch", "admission_mismatch"},
		{"missing recipient", "DELETE FROM internal_mail_recipients", "mismatch", "admission_mismatch"},
		{"earlier message", "UPDATE internal_mail_messages SET created_at=created_at-interval '1 hour'", "mismatch", "admission_mismatch"},
		{"active claim", "UPDATE platform_mail_producer_outbox SET state='claimed',lease_id=gen_random_uuid(),lease_until=clock_timestamp()+interval '1 minute',revision=revision+1", "confirmed", "active_claim"},
		{"cancelled", "UPDATE platform_mail_producer_outbox SET state='cancelled',recipient_address='',revision=revision+1", "confirmed", "intent_closed"},
		{"suppressed", "UPDATE platform_mail_producer_outbox SET state='suppressed',recipient_address='',revision=revision+1", "confirmed", "intent_closed"},
		{"held", "INSERT INTO internal_mail_recovery_state VALUES('platform','00000000-0000-0000-0000-000000000000',true)", "confirmed", "recovery_held"},
		{"quarantined", "INSERT INTO internal_mail_recovery_state VALUES('platform','11111111-1111-4111-8111-111111111111',false)", "confirmed", "recovery_quarantined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			intent := schedule(t, f, account(t, f))
			messageID := admitFixture(t, f, intent)
			if _, err := f.pool.Exec(t.Context(), tc.mutation); err != nil {
				t.Fatal(err)
			}
			id := uuidString(intent.ID.Bytes)
			before := row(t, f, id)
			review, err := internalmail.LookupProducerOutboxAdmission(t.Context(), f.pool, "platform", id)
			if err != nil || review.CanAcknowledge || review.AdmissionStatus != tc.status || review.BlockReason != tc.block {
				t.Fatal("unsafe receipt review", review, err)
			}
			input := reconciliationInput(before, messageID)
			input.RecoveryID = review.RecoveryID
			if _, err := reconciler(t, f).Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrConflict) {
				t.Fatal("unsafe acknowledgement succeeded", err)
			}
			if !reflect.DeepEqual(before, row(t, f, id)) {
				t.Fatal("refused acknowledgement changed intent")
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_outbox_reconciliations").Scan(&count); err != nil || count != 0 {
				t.Fatal("failed acknowledgement left a success audit", err)
			}
		})
	}
}

func TestPGOutboxReconciliationSerializesRequestsAndRejectsStaleRevision(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	id := uuidString(intent.ID.Bytes)
	input := reconciliationInput(intent, messageID)
	stale := input
	stale.ExpectedRevision++
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, stale); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("stale revision accepted", err)
	}
	stale = input
	stale.MessageID = uuid.NewString()
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, stale); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("operator message assertion used as proof", err)
	}
	other, err := NewReconciler(f.pool, "elsewhere", func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrNotFound) {
		t.Fatal("cross namespace acknowledgement", err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := reconciler(t, f).Acknowledge(t.Context(), id, input); err != nil {
				t.Error("concurrent replay", err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_outbox_reconciliations").Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent replay duplicated audit", err)
	}
	input.RequestID = uuid.NewString()
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, input); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("different request mutated an acknowledged intent", err)
	}
}

func TestPGOutboxReconciliationAuditFailureRollsBackAcknowledgement(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT injected_failure CHECK (actor<>'operator')"); err != nil {
		t.Fatal(err)
	}
	id := uuidString(intent.ID.Bytes)
	if _, err := reconciler(t, f).Acknowledge(t.Context(), id, reconciliationInput(intent, messageID)); err == nil {
		t.Fatal("injected audit failure succeeded")
	}
	if !reflect.DeepEqual(intent, row(t, f, id)) {
		t.Fatal("audit rollback left an unaudited acknowledgement")
	}
}

func waitForReconciliationBlock(t *testing.T, f *fixture, ctx context.Context, blocker uint32) {
	t.Helper()
	for {
		var blocked bool
		if err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
	}
}

func TestPGOutboxReconciliationGateRevocationRollsBackAfterAuditWait(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	id := uuidString(intent.ID.Bytes)
	var enabled atomic.Bool
	enabled.Store(true)
	r, err := NewReconciler(f.pool, "platform", enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(t.Context(), tx)
	if _, err := tx.Exec(t.Context(), "LOCK TABLE platform_mail_outbox_reconciliations IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Acknowledge(ctx, id, reconciliationInput(intent, messageID)); done <- err }()
	waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
	enabled.Store(false)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("late disable was ignored", err)
	}
	if !reflect.DeepEqual(intent, row(t, f, id)) {
		t.Fatal("late disable left an acknowledgement")
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_outbox_reconciliations").Scan(&count); err != nil || count != 0 {
		t.Fatal("late disable left an audit", err)
	}
}

func TestPGOutboxReconciliationChecksGateAndGenerationAfterLockWait(t *testing.T) {
	for _, change := range []string{"gate", "generation"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			intent := schedule(t, f, account(t, f))
			messageID := admitFixture(t, f, intent)
			id := uuidString(intent.ID.Bytes)
			var enabled atomic.Bool
			enabled.Store(true)
			r, err := NewReconciler(f.pool, "platform", enabled.Load)
			if err != nil {
				t.Fatal(err)
			}
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
			go func() { _, err := r.Acknowledge(ctx, id, reconciliationInput(intent, messageID)); done <- err }()
			waitForReconciliationBlock(t, f, ctx, tx.Conn().PgConn().PID())
			want := internalmail.ErrConflict
			if change == "gate" {
				enabled.Store(false)
				want = internalmail.ErrDisabled
			} else if _, err := tx.Exec(ctx, "INSERT INTO internal_mail_recovery_state VALUES('platform',gen_random_uuid(),false)"); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, want) {
				t.Fatal("late fence change was ignored", err)
			}
			if !reflect.DeepEqual(intent, row(t, f, id)) {
				t.Fatal("late fence change mutated intent")
			}
		})
	}
}

func TestOutboxReconciliationDefaultOffReadsNoPrivateConfiguration(t *testing.T) {
	for _, gate := range []string{"", "0", "true", "1 "} {
		getenv := func(k string) string {
			if k != internalmail.OutboxReconciliationEnableEnv {
				t.Fatal("disabled operator read private config", k)
			}
			return gate
		}
		if internalmail.OutboxReconciliationEnabled(getenv) || RunOperator(t.Context(), []string{"reconcile"}, getenv, io.Discard, io.Discard) == 0 {
			t.Fatal("nonexact flag enabled operator")
		}
	}
	if _, err := NewReconciler(nil, "platform", nil); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("disabled constructor accessed pool", err)
	}
	var r *Reconciler
	if _, err := r.Acknowledge(t.Context(), uuid.NewString(), ReconciliationRequest{}); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal("disabled acknowledgement accessed pool", err)
	}
}

func TestPGOutboxReconciliationOwnerCLIHasPrivateJSONAndSavedAudit(t *testing.T) {
	f := newFixture(t)
	intent := schedule(t, f, account(t, f))
	messageID := admitFixture(t, f, intent)
	input := reconciliationInput(intent, messageID)
	connection := f.pool.Config().ConnString()
	u, err := url.Parse(connection)
	if err != nil {
		t.Fatal(err)
	}
	searchPath := f.pool.Config().ConnConfig.RuntimeParams["search_path"]
	if u.Scheme == "postgres" || u.Scheme == "postgresql" {
		query := u.Query()
		query.Set("search_path", searchPath)
		u.RawQuery = query.Encode()
		connection = u.String()
	} else {
		connection += " search_path=" + searchPath
	}
	getenv := func(k string) string {
		switch k {
		case internalmail.OutboxReconciliationEnableEnv, internalmail.ProducerOutboxEnableEnv, internalmail.EnableEnv, internalmail.OperationsEnableEnv:
			return "1"
		case "DATABASE_URL":
			return connection
		}
		return ""
	}
	args := []string{"reconcile", "--id", uuidString(intent.ID.Bytes), "--message-id", messageID, "--expected-revision", strconv.FormatInt(intent.Revision, 10), "--recovery-id", input.RecoveryID,
		"--request-id", input.RequestID, "--actor", input.Actor, "--reason", input.Reason, "--evidence", input.Evidence}
	var out, diagnostics bytes.Buffer
	if code := RunOperator(t.Context(), args, getenv, &out, &diagnostics); code != 0 {
		t.Fatal("owner CLI failed", code, diagnostics.String())
	}
	for _, forbidden := range []string{"one@example.net", "notify@example.test", "account-delete:", "request_hash", "DATABASE_URL"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatal("operator output exposed private input", forbidden)
		}
	}
	want := out.String()
	out.Reset()
	if code := RunOperator(t.Context(), []string{"audit", "--request-id", input.RequestID}, getenv, &out, &diagnostics); code != 0 || out.String() != want {
		t.Fatal("saved audit CLI failed", code, diagnostics.String())
	}
	out.Reset()
	if code := RunOperator(t.Context(), args, getenv, &out, &diagnostics); code != 0 || out.String() != want {
		t.Fatal("owner CLI lost response replay failed", code, diagnostics.String())
	}
}
