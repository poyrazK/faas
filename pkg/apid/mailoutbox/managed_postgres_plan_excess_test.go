// adr: 20261102125208 — each database suspended past its plan is mailed at
// most once, from an encrypted snapshot, to the active account that owns it.
package mailoutbox

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

func planExcessFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.ManagedPostgresPlanExcessOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureManagedPostgresPlanExcess(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.managedPostgresPlanExcess.sender = f.sender
	return f
}

// planExcessNotice records a notice for a new database, as the cap sweep does.
func planExcessNotice(t *testing.T, f *fixture, reason, class string, recordedAt time.Time) ManagedPostgresPlanExcessNotice {
	t.Helper()
	notice := ManagedPostgresPlanExcessNotice{DatabaseID: uuid.NewString(), DatabaseName: "events", ServiceClass: class, Reason: reason, RecordedAt: recordedAt}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO managed_postgres_databases(id) VALUES($1)", notice.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_plan_excess_notices(database_id,reason,recorded_at) VALUES($1,$2,$3)`,
		notice.DatabaseID, reason, recordedAt); err != nil {
		t.Fatal(err)
	}
	return notice
}

func planExcessIntents(t *testing.T, f *fixture) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE template='managed_postgres_plan_excess' ORDER BY created_at")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestManagedPostgresPlanExcessDisabledReadsOnlyOwnGate(t *testing.T) {
	var service *Service
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := service.ConfigureManagedPostgresPlanExcess(t.Context(), func(k string) string {
			if k != internalmail.ManagedPostgresPlanExcessOutboxEnableEnv {
				t.Fatal("disabled config read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.NoticeManagedPostgresPlanExcess(t.Context(), uuid.NewString(), ManagedPostgresPlanExcessNotice{}); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGManagedPostgresPlanExcessQueuesOnceAndDeliversTheSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		reason, class, body string
	}{
		{"database_limit", "development", "fewer databases than you have"},
		{"service_class", "production", "no longer includes the production class"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			f := planExcessFixture(t)
			id := account(t, f)
			notice := planExcessNotice(t, f, tc.reason, tc.class, now.Add(-time.Minute))
			for range 2 {
				if err := f.service.NoticeManagedPostgresPlanExcess(t.Context(), id, notice); err != nil {
					t.Fatal(err)
				}
			}
			intents := planExcessIntents(t, f)
			if len(intents) != 1 {
				t.Fatalf("intents %v; want one for a repeated notice", intents)
			}
			var linked string
			if err := f.pool.QueryRow(t.Context(), "SELECT mail_intent_id::text FROM managed_postgres_plan_excess_notices WHERE database_id=$1", notice.DatabaseID).Scan(&linked); err != nil || linked != intents[0] {
				t.Fatalf("notice linked to %q, %v; want %s", linked, err, intents[0])
			}
			r := row(t, f, intents[0])
			if want := now.Add(-time.Minute + api.InternalMailManagedPostgresPlanExcessTTL); !r.ExpiresAt.Time.Equal(want) || strings.Contains(string(r.SealedSecurityAlert), "events") {
				t.Fatalf("intent expires %v, want %v, or stored the snapshot unsealed", r.ExpiresAt.Time, want)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 1 {
				after := row(t, f, intents[0])
				t.Fatalf("sent %d messages; intent %s %s", len(f.sender.calls), after.State, after.Code)
			}
			message := f.sender.calls[0].message
			if message.MessageID != "managed-postgres-plan-excess:"+intents[0] || message.Subject != "Your database events is suspended" ||
				!strings.Contains(message.TextBody, tc.body) || message.To[0] != "one@example.net" {
				t.Fatalf("message %+v lost the snapshot", message)
			}
			assertMFAAlertErased(t, row(t, f, intents[0]))
		})
	}
}

func TestPGManagedPostgresPlanExcessSkipsWhatItMustNotMail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name       string
		recordedAt time.Time
		setup      string
		noAccount  bool
	}{
		{name: "older than the email's lifetime", recordedAt: now.Add(-api.InternalMailManagedPostgresPlanExcessTTL - time.Minute)},
		{name: "inactive account", recordedAt: now, setup: "UPDATE accounts SET status='deleted_pending' WHERE id=$1"},
		{name: "account being deleted", recordedAt: now, setup: "UPDATE accounts SET deletion_requested_at=now() WHERE id=$1"},
		{name: "account gone", recordedAt: now, noAccount: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := planExcessFixture(t)
			id := uuid.NewString()
			if !tc.noAccount {
				id = account(t, f)
			}
			if tc.setup != "" {
				if _, err := f.pool.Exec(t.Context(), tc.setup, id); err != nil {
					t.Fatal(err)
				}
			}
			notice := planExcessNotice(t, f, "database_limit", "development", tc.recordedAt)
			if err := f.service.NoticeManagedPostgresPlanExcess(t.Context(), id, notice); err != nil {
				t.Fatal(err)
			}
			if intents := planExcessIntents(t, f); len(intents) != 0 {
				t.Fatalf("queued %v", intents)
			}
		})
	}
}
