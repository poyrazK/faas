package mailoutbox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

const nonpaymentMigration = "20261017125602853_platform_mail_nonpayment_deletion_outbox.sql"

func nonpaymentFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.NonpaymentDeletionOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureNonpaymentDeletion(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.nonpaymentDeletion.sender = f.sender
	return f
}

func nonpaymentAccount(t *testing.T, f *fixture, age time.Duration) (string, time.Time) {
	t.Helper()
	id := account(t, f)
	var at time.Time
	if err := f.pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=clock_timestamp()-make_interval(secs=>$2::integer) WHERE id=$1 RETURNING past_due_at", id, int32(age/time.Second)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return id, at
}

func queueNonpaymentDeletion(t *testing.T, f *fixture) (string, time.Time, q.PlatformMailProducerOutbox, api.InternalMailNonpaymentDeletionResponse) {
	t.Helper()
	id, at := nonpaymentAccount(t, f, 22*24*time.Hour)
	result, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
	if err != nil || !result.Followup {
		t.Fatal(result, err)
	}
	return id, at, row(t, f, result.IntentID), result
}

func TestNonpaymentDeletionDisabledReadsOnlyOwnGate(t *testing.T) {
	var s *Service
	if err := s.ConfigureNonpaymentDeletion(t.Context(), func(k string) string {
		if k != internalmail.NonpaymentDeletionOutboxEnableEnv {
			t.Fatal("disabled config read", k)
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScheduleNonpaymentDeletionWithNotice(t.Context(), "", time.Time{}, ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if err := s.CompleteNonpaymentDeletionFollowup(t.Context(), "", "", ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGNonpaymentDeletionOriginalPayloadAndDurableFollowup(t *testing.T) {
	f := nonpaymentFixture(t)
	id, at, r, result := queueNonpaymentDeletion(t, f)
	payload, err := nonpaymentDeletionPayloadFor(f.service.nonpaymentDeletion, r)
	if err != nil || !payload.DeletionRequestedAt.Equal(result.DeletionRequestedAt) || !payload.PastDueAt.Equal(at) || !r.ExpiresAt.Time.Equal(result.DeletionRequestedAt.Add(api.InternalMailNonpaymentDeletionTTL)) || r.NonpaymentDeletionRevision.Int64 != 3 {
		t.Fatal("lost original deletion binding", payload, err)
	}
	if strings.Contains(string(r.SealedSecurityAlert), at.Format(time.RFC3339)) {
		t.Fatal("unsealed payload")
	}
	other, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
	if err != nil || other.Followup || other.IntentID != result.IntentID {
		t.Fatal("concurrent tick acquired live follow-up", other, err)
	}
	// The original request can retry after a lost response, without extending mail.
	again, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, result.LeaseID)
	if err != nil || !again.Followup || again.IntentID != result.IntentID || !again.DeletionRequestedAt.Equal(result.DeletionRequestedAt) {
		t.Fatal(again, err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE billing_account_nonpayment_deletions SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	next, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
	if err != nil || !next.Followup || next.LeaseID == result.LeaseID {
		t.Fatal("restart failed to resume expired follow-up", next, err)
	}
	if err := f.service.CompleteNonpaymentDeletionFollowup(t.Context(), id, result.IntentID, result.LeaseID); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("stale follow-up lease acknowledged", err)
	}
	for range 2 {
		if err := f.service.CompleteNonpaymentDeletionFollowup(t.Context(), id, next.IntentID, next.LeaseID); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString()); err != nil || done.Followup {
		t.Fatal("completed follow-up replayed", done, err)
	}
}

func TestPGNonpaymentDeletionRetriesKeepTimeAndDeadline(t *testing.T) {
	f := nonpaymentFixture(t)
	_, _, r, _ := queueNonpaymentDeletion(t, f)
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := row(t, f, uuidString(r.ID.Bytes)); got.State != "queued" || len(got.SealedSecurityAlert) == 0 {
		t.Fatal("retry lost encrypted intent")
	}
	f.sender.send = nil
	if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) || f.sender.calls[0].message.MessageID != "nonpayment-deletion:"+uuidString(r.ID.Bytes) {
		t.Fatal("retry rewrote original message")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGNonpaymentDeletionCurrentPeriodAndDatabaseClock(t *testing.T) {
	for _, mode := range []string{"too_early", "stale_period", "recovered", "operator_suspended", "legacy_deleted", "deletion", "late"} {
		t.Run(mode, func(t *testing.T) {
			f := nonpaymentFixture(t)
			age := 22 * 24 * time.Hour
			if mode == "too_early" {
				age = 20 * 24 * time.Hour
			}
			if mode == "late" {
				age = 60 * 24 * time.Hour
			}
			id, at := nonpaymentAccount(t, f, age)
			switch mode {
			case "stale_period":
				at = at.Add(-time.Hour)
			case "recovered":
				if err := state.NewPgStore(f.pool).UpdateAccountStatus(t.Context(), id, state.AccountActive); err != nil {
					t.Fatal(err)
				}
			case "operator_suspended":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=NULL WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "legacy_deleted":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending',deletion_requested_at=clock_timestamp() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "deletion":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "late" {
				r := row(t, f, got.IntentID)
				if !got.Followup || !r.NonpaymentDeletionAt.Time.Equal(got.DeletionRequestedAt) || !r.ExpiresAt.Time.Equal(got.DeletionRequestedAt.Add(api.InternalMailNonpaymentDeletionTTL)) {
					t.Fatal("late sweep lost saved deletion deadline")
				}
				if err := f.service.DeliverOne(t.Context()); err != nil {
					t.Fatal(err)
				}
				deadline := got.DeletionRequestedAt.Add(api.AccountDeletionGraceDays * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05 UTC")
				if len(f.sender.calls) != 1 || !strings.Contains(f.sender.calls[0].message.TextBody, deadline) {
					t.Fatal("late sweep did not preserve actual grace deadline")
				}
			} else if got.IntentID != "" || got.Followup {
				t.Fatal("ineligible snapshot changed account", got)
			}
		})
	}
}

func TestPGNonpaymentDeletionConcurrentTicksAndLaterOverduePeriod(t *testing.T) {
	f := nonpaymentFixture(t)
	id, at := nonpaymentAccount(t, f, 22*24*time.Hour)
	var wg sync.WaitGroup
	var claims atomic.Int32
	for range 12 {
		wg.Go(func() {
			got, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
			if err != nil {
				t.Error(err)
			}
			if got.Followup {
				claims.Add(1)
			}
		})
	}
	wg.Wait()
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_account_nonpayment_deletions").Scan(&count); err != nil || count != 1 || claims.Load() != 1 {
		t.Fatal("concurrent ticks duplicated deletion", count, claims.Load(), err)
	}
	if err := state.NewPgStore(f.pool).UpdateAccountStatus(t.Context(), id, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	var newer time.Time
	if err := f.pool.QueryRow(t.Context(), "UPDATE accounts SET status='suspended',past_due_at=$2::timestamptz+interval '1 second' WHERE id=$1 RETURNING past_due_at", id, at).Scan(&newer); err != nil {
		t.Fatal(err)
	}
	if old, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString()); err != nil || old.Followup || old.IntentID != "" {
		t.Fatal("old tick scheduled deletion for new period", old, err)
	}
	if fresh, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, newer, uuid.NewString()); err != nil || !fresh.Followup {
		t.Fatal("new period did not receive new notice", fresh, err)
	}
}

func TestPGNonpaymentDeletionLifecycleAndErasure(t *testing.T) {
	for _, mode := range []string{"recovered", "status_aba", "address_aba", "deleted", "security", "off", "suppressed", "erasure", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := nonpaymentFixture(t)
			id, _, r, _ := queueNonpaymentDeletion(t, f)
			var mutation string
			switch mode {
			case "recovered":
				mutation = "UPDATE accounts SET status='active',past_due_at=NULL WHERE id=$1"
			case "status_aba":
				f.env[internalmail.NonpaymentDeletionOutboxEnableEnv] = "0"
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET status='deleted_pending' WHERE id=$1"
				f.env[internalmail.NonpaymentDeletionOutboxEnableEnv] = "1"
			case "address_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET email='one@example.net' WHERE id=$1"
			case "deleted":
				mutation = "UPDATE accounts SET deletion_requested_at=deletion_requested_at+interval '1 second' WHERE id=$1"
			case "security":
				mutation = "UPDATE accounts SET password_reset_revision=password_reset_revision+1,mfa_revision=mfa_revision+1 WHERE id=$1"
			case "off":
				f.env[internalmail.NonpaymentDeletionOutboxEnableEnv] = "0"
			case "suppressed":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')"); err != nil {
					t.Fatal(err)
				}
			case "erasure":
				mutation = "DELETE FROM accounts WHERE id=$1"
			case "corrupt":
				if _, err := f.pool.Exec(t.Context(), "UPDATE platform_mail_producer_outbox SET sealed_security_alert=decode(repeat('00',100),'hex') WHERE id=$1", r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "" {
				if _, err := f.pool.Exec(t.Context(), mutation, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "security" {
				if len(f.sender.calls) != 1 {
					t.Fatal("security change cancelled billing notice")
				}
				return
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("stale or disabled notice handed off")
			}
			if mode == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_account_nonpayment_deletions").Scan(&n); err != nil || n != 0 {
					t.Fatal("erasure retained follow-up", err)
				}
				return
			}
			want := "cancelled"
			if mode == "off" {
				want = "queued"
			}
			if mode == "suppressed" {
				want = "suppressed"
			}
			if mode == "corrupt" {
				want = "failed"
			}
			got := row(t, f, uuidString(r.ID.Bytes))
			if got.State != want {
				t.Fatal(got.State, want)
			}
			if mode != "off" {
				assertMFAAlertErased(t, got)
			}
		})
	}
}

func TestPGNonpaymentDeletionStaleFollowupCannotScheduleAgain(t *testing.T) {
	for _, mode := range []string{"restored_same_period", "status_aba", "address_aba", "deadline", "recovery_generation"} {
		t.Run(mode, func(t *testing.T) {
			f := nonpaymentFixture(t)
			id, at, _, result := queueNonpaymentDeletion(t, f)
			if _, err := f.pool.Exec(t.Context(), "UPDATE billing_account_nonpayment_deletions SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
				t.Fatal(err)
			}
			var mutation string
			switch mode {
			case "restored_same_period":
				mutation = "UPDATE accounts SET status='suspended',deletion_requested_at=NULL WHERE id=$1"
			case "status_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='active' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET status='deleted_pending' WHERE id=$1"
			case "address_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='changed@example.net' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET email='one@example.net' WHERE id=$1"
			case "deadline":
				mutation = "UPDATE accounts SET deletion_requested_at=deletion_requested_at+interval '1 second' WHERE id=$1"
			case "recovery_generation":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO internal_mail_recovery_state(namespace,recovery_id,held) VALUES('platform',$1,false)", uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			if mutation != "" {
				if _, err := f.pool.Exec(t.Context(), mutation, id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString())
			if mode == "recovery_generation" {
				if !errors.Is(err, internalmail.ErrRecoveryHeld) {
					t.Fatal("changed recovery generation resumed follow-up", err)
				}
			} else if err != nil || got.Followup || got.IntentID != "" {
				t.Fatal("stale receipt revived a deletion", got, err)
			}
			if err := f.service.CompleteNonpaymentDeletionFollowup(t.Context(), account(t, f), result.IntentID, result.LeaseID); !errors.Is(err, internalmail.ErrConflict) {
				t.Fatal("another account acknowledged follow-up", err)
			}
		})
	}
}

func TestPGNonpaymentDeletionPaymentRecoveryCancelsSavedNotice(t *testing.T) {
	f := nonpaymentFixture(t)
	id, at, r, _ := queueNonpaymentDeletion(t, f)
	f.env[internalmail.AccountRestoredOutboxEnableEnv] = "1"
	if err := f.service.ConfigureAccountRestored(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	customer := "cus_nonpayment_recovery"
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO billing_identities(account_id,provider,customer_id) VALUES($1,'stripe',$2)", id, customer); err != nil {
		t.Fatal(err)
	}
	if restored, err := f.service.RestorePaymentWithConfirmation(t.Context(), id, "stripe", "evt_nonpayment_recovery", customer); err != nil || !restored {
		t.Fatal("payment did not recover deletion", restored, err)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := row(t, f, uuidString(r.ID.Bytes))
	if len(f.sender.calls) != 0 || got.State != "cancelled" {
		t.Fatal("recovered account received deletion notice", got.State)
	}
	assertMFAAlertErased(t, got)
	if followup, err := f.service.ScheduleNonpaymentDeletionWithNotice(t.Context(), id, at, uuid.NewString()); err != nil || followup.Followup {
		t.Fatal("payment recovery resumed obsolete follow-up", followup, err)
	}
}
