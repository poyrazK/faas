// adr: 20261125231222 — each weekly awake notice is mailed at most once, from
// an encrypted snapshot, to the active account that owns the database.
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

func awakeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.ManagedPostgresAwakeOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureManagedPostgresAwake(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.managedPostgresAwake.sender = f.sender
	return f
}

// awakeNotice records a notice for a new database, as the awake sweep does.
func awakeNotice(t *testing.T, f *fixture, accountID string, recordedAt time.Time) ManagedPostgresAwakeNotice {
	t.Helper()
	week := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	notice := ManagedPostgresAwakeNotice{DatabaseID: uuid.NewString(), DatabaseName: "orders", WeekStart: week, AwakeHoursPerDay: 9,
		Starts: 672, Cause: "cron", AppSlug: "api", Detail: "/cron/cleanup", CauseStarts: 670, RecordedAt: recordedAt}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO managed_postgres_databases(id) VALUES($1)", notice.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_awake_notices(database_id,account_id,week_start,awake_seconds,starts,cause,cause_starts,recorded_at)
		VALUES($1,$2,$3,$4,$5,'cron',$6,$7)`, notice.DatabaseID, accountID, week, 9*7*3600, notice.Starts, notice.CauseStarts, recordedAt); err != nil {
		t.Fatal(err)
	}
	return notice
}

func awakeIntents(t *testing.T, f *fixture) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE template='managed_postgres_awake' ORDER BY created_at")
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

func TestManagedPostgresAwakeDisabledReadsOnlyOwnGate(t *testing.T) {
	var service *Service
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := service.ConfigureManagedPostgresAwake(t.Context(), func(k string) string {
			if k != internalmail.ManagedPostgresAwakeOutboxEnableEnv {
				t.Fatal("disabled config read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.NoticeManagedPostgresAwake(t.Context(), uuid.NewString(), ManagedPostgresAwakeNotice{}); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGManagedPostgresAwakeQueuesOnceAndDeliversTheSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	f := awakeFixture(t)
	id := account(t, f)
	notice := awakeNotice(t, f, id, now.Add(-time.Minute))
	for range 2 {
		if err := f.service.NoticeManagedPostgresAwake(t.Context(), id, notice); err != nil {
			t.Fatal(err)
		}
	}
	intents := awakeIntents(t, f)
	if len(intents) != 1 {
		t.Fatalf("intents %v; want one for a repeated notice", intents)
	}
	var mailed bool
	if err := f.pool.QueryRow(t.Context(), "SELECT mailed_at IS NOT NULL FROM managed_postgres_awake_notices WHERE database_id=$1", notice.DatabaseID).Scan(&mailed); err != nil || !mailed {
		t.Fatalf("notice mailed %v, %v; want marked", mailed, err)
	}
	r := row(t, f, intents[0])
	if want := now.Add(-time.Minute + api.InternalMailManagedPostgresAwakeTTL); !r.ExpiresAt.Time.Equal(want) || strings.Contains(string(r.SealedSecurityAlert), "orders") {
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
	if message.MessageID != "managed-postgres-awake:"+intents[0] || message.Subject != "Your database orders was awake 9 hours a day" ||
		!strings.Contains(message.TextBody, "The cron /cron/cleanup of api started it 670") || message.To[0] != "one@example.net" {
		t.Fatalf("message %+v lost the snapshot", message)
	}
	assertMFAAlertErased(t, row(t, f, intents[0]))
}

func TestPGManagedPostgresAwakeSkipsWhatItMustNotMail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name       string
		recordedAt time.Time
		setup      string
		noAccount  bool
		noNotice   bool
	}{
		{name: "older than the email's lifetime", recordedAt: now.Add(-api.InternalMailManagedPostgresAwakeTTL - time.Minute)},
		{name: "inactive account", recordedAt: now, setup: "UPDATE accounts SET status='deleted_pending' WHERE id=$1"},
		{name: "account being deleted", recordedAt: now, setup: "UPDATE accounts SET deletion_requested_at=now() WHERE id=$1"},
		{name: "account gone", recordedAt: now, noAccount: true},
		{name: "notice gone", recordedAt: now, noNotice: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := awakeFixture(t)
			id := uuid.NewString()
			if !tc.noAccount {
				id = account(t, f)
			}
			if tc.setup != "" {
				if _, err := f.pool.Exec(t.Context(), tc.setup, id); err != nil {
					t.Fatal(err)
				}
			}
			notice := awakeNotice(t, f, id, tc.recordedAt)
			if tc.noNotice {
				if _, err := f.pool.Exec(t.Context(), "DELETE FROM managed_postgres_awake_notices WHERE database_id=$1", notice.DatabaseID); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.NoticeManagedPostgresAwake(t.Context(), id, notice); err != nil {
				t.Fatal(err)
			}
			if intents := awakeIntents(t, f); len(intents) != 0 {
				t.Fatalf("queued %v", intents)
			}
		})
	}
}
