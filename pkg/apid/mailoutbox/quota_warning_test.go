package mailoutbox

import (
	"context"
	"errors"
	"fmt"
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
)

const quotaMigration = "20261017133739854_platform_mail_quota_warning_outbox.sql"

// Derive an over-quota fixture from the current plan contract.
func quotaWarningTestUsage(plan api.Plan) int64 {
	return int64(plan.PlanIncludedGBHours()+1) * 1024 * 3600
}

func quotaFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.QuotaWarningOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	f.env["FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL"] = "https://dashboard.example.test/notifications/unsubscribe"
	if err := f.service.ConfigureQuotaWarning(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.quotaWarning.sender = f.sender
	return f
}

func quotaAccount(t *testing.T, f *fixture, plan api.Plan, used int64) (string, time.Time) {
	t.Helper()
	id := account(t, f)
	if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET plan=$2 WHERE id=$1", id, string(plan)); err != nil {
		t.Fatal(err)
	}
	day, err := f.service.queries.QuotaWarningDay(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO usage_minutes(account_id,minute,mb_seconds) VALUES($1,$2,$3)", id, day.Time, used); err != nil {
		t.Fatal(err)
	}
	return id, day.Time
}

func queueQuotaWarning(t *testing.T, f *fixture) (string, time.Time, q.PlatformMailProducerOutbox, api.InternalMailQuotaWarningResponse) {
	t.Helper()
	id, day := quotaAccount(t, f, api.PlanPro, quotaWarningTestUsage(api.PlanPro))
	result, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, uuid.NewString())
	if err != nil || !result.Followup {
		t.Fatal(result, err)
	}
	return id, day, row(t, f, result.IntentID), result
}

func TestQuotaWarningDisabledReadsOnlyOwnGate(t *testing.T) {
	var service *Service
	for _, value := range []string{"", "0", "true", " 1", "1 "} {
		if err := service.ConfigureQuotaWarning(t.Context(), func(k string) string {
			if k != internalmail.QuotaWarningOutboxEnableEnv {
				t.Fatal("disabled config read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.WarnQuotaWithNotice(t.Context(), "", time.Time{}, api.PlanPro, ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
	if err := service.CompleteQuotaWarningFollowup(t.Context(), "", "", ""); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGQuotaWarningSnapshotDailyDedupeAndDurableFollowup(t *testing.T) {
	f := quotaFixture(t)
	id, day, r, result := queueQuotaWarning(t, f)
	payload, err := quotaWarningPayloadFor(f.service.quotaWarning, r)
	if err != nil || payload.Plan != api.PlanPro || payload.UsedMBSeconds != quotaWarningTestUsage(api.PlanPro) || payload.QuotaGB != api.PlanPro.PlanIncludedGBHours() || !payload.Day.Equal(day) || !payload.ObservedAt.Equal(result.ObservedAt) || !r.ExpiresAt.Time.Equal(day.Add(24*time.Hour)) || r.QuotaWarningRevision.Int64 != 3 {
		t.Fatal("lost authoritative snapshot", payload, err)
	}
	if strings.Contains(string(r.SealedSecurityAlert), "unsubscribe") {
		t.Fatal("payload is not encrypted")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE usage_minutes SET mb_seconds=mb_seconds*2 WHERE account_id=$1", id); err != nil {
		t.Fatal(err)
	}
	other, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, uuid.NewString())
	if err != nil || other.Followup || other.IntentID != result.IntentID || other.UsedMBSeconds != result.UsedMBSeconds {
		t.Fatal("concurrent tick changed original snapshot or claimed live lease", other, err)
	}
	again, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, result.LeaseID)
	if err != nil || !again.Followup || again.LeaseID != result.LeaseID || again.UsedMBSeconds != result.UsedMBSeconds {
		t.Fatal("same request lost saved follow-up", again, err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE billing_quota_warnings SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	next, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, uuid.NewString())
	if err != nil || !next.Followup || next.LeaseID == result.LeaseID || next.UsedMBSeconds != result.UsedMBSeconds {
		t.Fatal("restart did not resume original observation", next, err)
	}
	if err := f.service.CompleteQuotaWarningFollowup(t.Context(), id, result.IntentID, result.LeaseID); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("stale lease completed follow-up", err)
	}
	if err := f.service.CompleteQuotaWarningFollowup(t.Context(), account(t, f), next.IntentID, next.LeaseID); !errors.Is(err, internalmail.ErrConflict) {
		t.Fatal("another account completed follow-up", err)
	}
	for range 2 {
		if err := f.service.CompleteQuotaWarningFollowup(t.Context(), id, next.IntentID, next.LeaseID); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, uuid.NewString()); err != nil || done.Followup {
		t.Fatal("completed follow-up replayed", done, err)
	}
}

func TestPGQuotaWarningEligibilityAndUTC(t *testing.T) {
	for _, mode := range []string{"under", "rounds_to_threshold", "free", "stale_plan", "yesterday", "tomorrow", "non_midnight", "legacy_stamp", "future_stamp", "deleting", "unknown", "other_month", "timezone"} {
		t.Run(mode, func(t *testing.T) {
			f := quotaFixture(t)
			used, plan := int64(api.PlanPro.PlanIncludedGBHours())*1024*3600, api.PlanPro
			if mode == "under" {
				used -= 2 // old six-decimal rounding remains just below quota
			}
			if mode == "rounds_to_threshold" {
				used-- // the existing rounded quota decision reaches exactly 100%
			}
			if mode == "free" {
				plan = api.PlanFree
			}
			id, day := quotaAccount(t, f, plan, used)
			switch mode {
			case "stale_plan":
				plan = api.PlanHobby
			case "yesterday":
				day = day.Add(-24 * time.Hour)
			case "tomorrow":
				day = day.Add(24 * time.Hour)
			case "non_midnight":
				day = day.Add(time.Second)
			case "legacy_stamp", "future_stamp":
				stamp := day
				if mode == "future_stamp" {
					stamp = stamp.Add(24 * time.Hour)
				}
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET last_quota_warning_at=$2 WHERE id=$1", id, stamp); err != nil {
					t.Fatal(err)
				}
			case "deleting":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='deleted_pending',deletion_requested_at=clock_timestamp() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				id = uuid.NewString()
			case "other_month":
				if _, err := f.pool.Exec(t.Context(), "UPDATE usage_minutes SET minute=minute-interval '1 month'"); err != nil {
					t.Fatal(err)
				}
			case "timezone":
				if _, err := f.pool.Exec(t.Context(), "SET TIME ZONE 'Pacific/Honolulu'"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, plan, uuid.NewString())
			invalid := mode == "free" || mode == "non_midnight"
			if invalid {
				if !errors.Is(err, internalmail.ErrInvalid) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			eligible := mode == "rounds_to_threshold" || mode == "timezone"
			if got.Followup != eligible || (got.IntentID != "") != eligible {
				t.Fatal("ineligible or stale command changed daily stamp", got)
			}
		})
	}
}

func TestPGQuotaWarningConcurrentTicks(t *testing.T) {
	f := quotaFixture(t)
	id, day := quotaAccount(t, f, api.PlanScale, quotaWarningTestUsage(api.PlanScale))
	var wg sync.WaitGroup
	var claims atomic.Int32
	for range 12 {
		wg.Go(func() {
			got, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanScale, uuid.NewString())
			if err != nil {
				t.Error(err)
			}
			if got.Followup {
				claims.Add(1)
			}
		})
	}
	wg.Wait()
	var receipts, intents int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM billing_quota_warnings),(SELECT count(*) FROM platform_mail_producer_outbox WHERE template='quota_warning')").Scan(&receipts, &intents); err != nil || receipts != 1 || intents != 1 || claims.Load() != 1 {
		t.Fatal("duplicate quota warning", receipts, intents, claims.Load(), err)
	}
}

func TestPGQuotaWarningStableRetryIncludesBothAlternativesAndHeaders(t *testing.T) {
	f := quotaFixture(t)
	_, _, r, _ := queueQuotaWarning(t, f)
	f.sender.send = func(context.Context, mail.Message, internalmail.MessageTiming) error { return mail.ErrTransient }
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.service.quotaWarningUnsubscribe = "https://changed.example.test/unsubscribe"
	if _, err := f.pool.Exec(t.Context(), "UPDATE usage_minutes SET mb_seconds=mb_seconds*2; UPDATE platform_mail_producer_outbox SET next_attempt_at=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	f.sender.send = nil
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 2 || !reflect.DeepEqual(f.sender.calls[0], f.sender.calls[1]) {
		t.Fatal("retry changed frozen notice")
	}
	message := f.sender.calls[1].message
	if message.MessageID != "quota-warning:"+uuidString(r.ID.Bytes) || message.HTMLBody == "" || !strings.Contains(message.TextBody, fmt.Sprintf("%d.00", api.PlanPro.PlanIncludedGBHours()+1)) || !strings.Contains(message.HTMLBody, fmt.Sprintf("%d.00", api.PlanPro.PlanIncludedGBHours()+1)) || message.Headers["List-Unsubscribe"] != "<https://dashboard.example.test/notifications/unsubscribe>" || message.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatal("lost usage snapshot, alternatives or unsubscribe headers")
	}
	assertMFAAlertErased(t, row(t, f, uuidString(r.ID.Bytes)))
}

func TestPGQuotaWarningLifecycleAndStaleFollowup(t *testing.T) {
	for _, mode := range []string{"address_aba", "plan", "plan_aba", "status_aba", "deletion", "stamp_clear", "stamp_aba", "suppressed", "off", "security", "erasure", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			f := quotaFixture(t)
			id, day, r, _ := queueQuotaWarning(t, f)
			var mutation string
			switch mode {
			case "address_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET email='other@example.net' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET email='one@example.net' WHERE id=$1"
			case "plan", "plan_aba":
				mutation = "UPDATE accounts SET plan='scale' WHERE id=$1"
				if mode == "plan_aba" {
					if _, err := f.pool.Exec(t.Context(), mutation, id); err != nil {
						t.Fatal(err)
					}
					mutation = "UPDATE accounts SET plan='pro' WHERE id=$1"
				}
			case "status_aba":
				if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET status='suspended' WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				mutation = "UPDATE accounts SET status='active' WHERE id=$1"
			case "deletion":
				mutation = "UPDATE accounts SET deletion_requested_at=clock_timestamp() WHERE id=$1"
			case "stamp_clear", "stamp_aba":
				mutation = "UPDATE accounts SET last_quota_warning_at=NULL WHERE id=$1"
				if mode == "stamp_aba" {
					if _, err := f.pool.Exec(t.Context(), mutation, id); err != nil {
						t.Fatal(err)
					}
					if _, err := f.pool.Exec(t.Context(), "UPDATE accounts SET last_quota_warning_at=$2 WHERE id=$1", id, day); err != nil {
						t.Fatal(err)
					}
					mutation = ""
				}
			case "suppressed":
				if _, err := f.pool.Exec(t.Context(), "INSERT INTO mail_suppressions(email) VALUES('one@example.net')"); err != nil {
					t.Fatal(err)
				}
			case "off":
				f.env[internalmail.QuotaWarningOutboxEnableEnv] = "0"
			case "security":
				mutation = "UPDATE accounts SET password_reset_revision=password_reset_revision+1,mfa_revision=mfa_revision+1 WHERE id=$1"
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
					t.Fatal("security change cancelled quota warning")
				}
				return
			}
			if len(f.sender.calls) != 0 {
				t.Fatal("stale warning sent")
			}
			if mode == "erasure" {
				var n int
				if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM billing_quota_warnings").Scan(&n); err != nil || n != 0 {
					t.Fatal("erasure retained receipt", n, err)
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
			if want == "cancelled" {
				if _, err := f.pool.Exec(t.Context(), "UPDATE billing_quota_warnings SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
					t.Fatal(err)
				}
				if again, err := f.service.WarnQuotaWithNotice(t.Context(), id, day, api.PlanPro, uuid.NewString()); err != nil || again.Followup || again.IntentID != "" {
					t.Fatal("stale receipt revived follow-up", again, err)
				}
			}
		})
	}
}
