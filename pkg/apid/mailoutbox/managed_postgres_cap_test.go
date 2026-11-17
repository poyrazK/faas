// adr: 20261025094915 — each recorded compute cap threshold is mailed at most
// once, from an encrypted snapshot, to an active personal account.
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

func capFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.ManagedPostgresCapOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureManagedPostgresCap(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.managedPostgresCap.sender = f.sender
	return f
}

// capEvent records a threshold for a personal account, as the cap sweep does.
func capEvent(t *testing.T, f *fixture, subject, threshold string, overage bool, reachedAt time.Time) ManagedPostgresCapEvent {
	t.Helper()
	month := time.Date(reachedAt.Year(), reachedAt.Month(), 1, 0, 0, 0, 0, time.UTC)
	event := ManagedPostgresCapEvent{SubjectID: subject, PeriodStart: month, Threshold: threshold, ReachedAt: reachedAt,
		Used: 90000, Allowance: 72000, Ceiling: 144000, Overage: overage}
	if !overage {
		event.Ceiling = event.Allowance
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO resource_namespaces(id) VALUES($1) ON CONFLICT DO NOTHING", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_cap_events(subject_id,period_start,threshold,reached_at,
		used_unit_seconds,allowance_unit_seconds,ceiling_unit_seconds,overage_enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		subject, month, threshold, reachedAt, event.Used, event.Allowance, event.Ceiling, overage); err != nil {
		t.Fatal(err)
	}
	return event
}

func capIntents(t *testing.T, f *fixture, account string) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE account_id=$1 AND template='managed_postgres_cap' ORDER BY created_at", account)
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

func TestManagedPostgresCapDisabledReadsOnlyOwnGate(t *testing.T) {
	var service *Service
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := service.ConfigureManagedPostgresCap(t.Context(), func(k string) string {
			if k != internalmail.ManagedPostgresCapOutboxEnableEnv {
				t.Fatal("disabled config read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.NoticeManagedPostgresCap(t.Context(), uuid.NewString(), ManagedPostgresCapEvent{}); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGManagedPostgresCapQueuesOnceAndDeliversTheSnapshot(t *testing.T) {
	f := capFixture(t)
	id := account(t, f)
	reached := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	event := capEvent(t, f, id, "suspended", false, reached)
	for range 2 {
		if err := f.service.NoticeManagedPostgresCap(t.Context(), id, event); err != nil {
			t.Fatal(err)
		}
	}
	intents := capIntents(t, f, id)
	if len(intents) != 1 {
		t.Fatalf("intents %v; want one for a repeated notice", intents)
	}
	var linked string
	if err := f.pool.QueryRow(t.Context(), "SELECT mail_intent_id::text FROM managed_postgres_cap_events WHERE subject_id=$1", id).Scan(&linked); err != nil || linked != intents[0] {
		t.Fatalf("event linked to %q, %v; want %s", linked, err, intents[0])
	}
	r := row(t, f, intents[0])
	if !r.ExpiresAt.Time.Equal(reached.Add(api.InternalMailManagedPostgresCapTTL)) || strings.Contains(string(r.SealedSecurityAlert), "suspended") {
		t.Fatal("intent lost its deadline or stored the snapshot unsealed")
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 {
		after := row(t, f, intents[0])
		t.Fatalf("sent %d messages; intent %s %s", len(f.sender.calls), after.State, after.Code)
	}
	message := f.sender.calls[0].message
	if message.MessageID != "managed-postgres-cap:"+intents[0] || !strings.Contains(message.Subject, "suspended") ||
		!strings.Contains(message.TextBody, "20.0 compute-unit hours") || message.To[0] != "one@example.net" {
		t.Fatalf("message %+v lost the snapshot", message)
	}
	assertMFAAlertErased(t, row(t, f, intents[0]))
}

// ADR-20261110160000: the egress hold goes through the cap email path and
// reports egress, not compute.
func TestPGManagedPostgresCapMailsTheEgressHold(t *testing.T) {
	f := capFixture(t)
	id := account(t, f)
	reached := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	event := capEvent(t, f, id, "egress_held", false, reached)
	if err := f.service.NoticeManagedPostgresCap(t.Context(), id, event); err != nil {
		t.Fatal(err)
	}
	intents := capIntents(t, f, id)
	if len(intents) != 1 {
		t.Fatalf("intents %v; want one", intents)
	}
	if err := f.service.DeliverOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.sender.calls) != 1 {
		after := row(t, f, intents[0])
		t.Fatalf("sent %d messages; intent %s %s", len(f.sender.calls), after.State, after.Code)
	}
	message := f.sender.calls[0].message
	if !strings.Contains(message.Subject, "paused until next month") || !strings.Contains(message.TextBody, "monthly egress allowance") ||
		strings.Contains(message.TextBody, "compute-unit") {
		t.Fatalf("message %+v is not the egress hold", message)
	}
}

func TestPGManagedPostgresCapSkipsWhatItMustNotMail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name      string
		threshold string
		overage   bool
		reached   time.Time
		setup     string
		noAccount bool
	}{
		{name: "allowance without overage", threshold: "allowance", reached: now.Add(-time.Minute)},
		{name: "older than the email's lifetime", threshold: "suspended", reached: now.Add(-api.InternalMailManagedPostgresCapTTL - time.Minute)},
		{name: "inactive account", threshold: "warning", reached: now.Add(-time.Minute), setup: "UPDATE accounts SET status='deleted_pending' WHERE id=$1"},
		{name: "account being deleted", threshold: "warning", reached: now.Add(-time.Minute), setup: "UPDATE accounts SET deletion_requested_at=now() WHERE id=$1"},
		{name: "organization subject", threshold: "ceiling_warning", overage: true, reached: now.Add(-time.Minute), noAccount: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.reached.Month() != now.Month() {
				t.Skip("the fixture month changed under the test")
			}
			f := capFixture(t)
			subject := uuid.NewString()
			if !tc.noAccount {
				subject = account(t, f)
			}
			if tc.setup != "" {
				if _, err := f.pool.Exec(t.Context(), tc.setup, subject); err != nil {
					t.Fatal(err)
				}
			}
			event := capEvent(t, f, subject, tc.threshold, tc.overage, tc.reached)
			if err := f.service.NoticeManagedPostgresCap(t.Context(), subject, event); err != nil {
				t.Fatal(err)
			}
			var queued int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM platform_mail_producer_outbox WHERE template='managed_postgres_cap'").Scan(&queued); err != nil || queued != 0 {
				t.Fatalf("queued %d, %v", queued, err)
			}
		})
	}
}

// ADR-20261110160000 amendment 2026-11-13: each database held for its own
// egress allowance gets its own email, naming it, linked to its notice so it
// is mailed once.
func TestPGManagedPostgresCapMailsEachHeldDatabase(t *testing.T) {
	f := capFixture(t)
	id := account(t, f)
	reached := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	month := time.Date(reached.Year(), reached.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO resource_namespaces(id) VALUES($1) ON CONFLICT DO NOTHING", id); err != nil {
		t.Fatal(err)
	}
	databases := map[string]string{}
	for _, name := range []string{"orders", "events"} {
		database := uuid.NewString()
		databases[database] = name
		if _, err := f.pool.Exec(t.Context(), "INSERT INTO managed_postgres_databases(id) VALUES($1)", database); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_egress_hold_notices(database_id,period_start,subject_id,reached_at,
			used_bytes,allowance_bytes,ceiling_bytes,overage_billed) VALUES($1,$2,$3,$4,$5,$6,$6,false)`,
			database, month, id, reached, int64(510)<<30, int64(500)<<30); err != nil {
			t.Fatal(err)
		}
		event := ManagedPostgresCapEvent{DatabaseID: database, DatabaseName: name, SubjectID: id, PeriodStart: month, Threshold: "egress_held",
			ReachedAt: reached, Used: 510 << 30, Allowance: 500 << 30, Ceiling: 500 << 30}
		for range 2 {
			if err := f.service.NoticeManagedPostgresCap(t.Context(), id, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if intents := capIntents(t, f, id); len(intents) != 2 {
		t.Fatalf("intents %v; want one for each held database", intents)
	}
	var linked int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM managed_postgres_egress_hold_notices WHERE subject_id=$1 AND mail_intent_id IS NOT NULL", id).Scan(&linked); err != nil || linked != 2 {
		t.Fatalf("linked notices %d, %v", linked, err)
	}
	for range 2 {
		if err := f.service.DeliverOne(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sender.calls) != 2 {
		t.Fatalf("sent %d messages; want 2", len(f.sender.calls))
	}
	seen := map[string]bool{}
	for _, call := range f.sender.calls {
		for _, name := range databases {
			if strings.Contains(call.message.Subject, "database "+name+" is paused") && strings.Contains(call.message.TextBody, "Your database "+name+" sent") {
				seen[name] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("messages %+v do not name each database", f.sender.calls)
	}
	// A notice for another subject's database is not linked.
	other := uuid.NewString()
	for database := range databases {
		err := f.service.NoticeManagedPostgresCap(t.Context(), id, ManagedPostgresCapEvent{DatabaseID: database, SubjectID: other, PeriodStart: month,
			Threshold: "egress_held", ReachedAt: reached, Used: 1, Allowance: 1, Ceiling: 1})
		if err != nil || len(capIntents(t, f, id)) != 2 {
			t.Fatalf("cross-subject notice: %v, intents %v", err, capIntents(t, f, id))
		}
		break
	}
	// Only an egress hold names a database.
	if err := f.service.NoticeManagedPostgresCap(t.Context(), id, ManagedPostgresCapEvent{DatabaseID: uuid.NewString(), SubjectID: id, PeriodStart: month,
		Threshold: "warning", ReachedAt: reached, Used: 1, Allowance: 1, Ceiling: 1}); !errors.Is(err, internalmail.ErrInvalid) {
		t.Fatalf("compute threshold with a database = %v", err)
	}
}
