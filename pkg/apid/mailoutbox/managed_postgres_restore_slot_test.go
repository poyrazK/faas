// adr: 20261031123259 — each recorded restore slot notice is mailed at most
// once, from an encrypted snapshot, to the active account that restored the
// copy, and an 'ending' notice never outlives the slot.
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

func restoreSlotFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	_, path := verificationIdentity(t)
	f.env[internalmail.ManagedPostgresRestoreSlotOutboxEnableEnv], f.env[VerificationIdentityEnv] = "1", path
	if err := f.service.ConfigureManagedPostgresRestoreSlot(t.Context(), func(k string) string { return f.env[k] }); err != nil {
		t.Fatal(err)
	}
	f.service.managedPostgresRestoreSlot.sender = f.sender
	return f
}

// restoreSlotNotice records a notice for a new copy, as the slot sweep does.
func restoreSlotNotice(t *testing.T, f *fixture, kind string, slotUntil, recordedAt time.Time) ManagedPostgresRestoreSlotNotice {
	t.Helper()
	notice := ManagedPostgresRestoreSlotNotice{DatabaseID: uuid.NewString(), DatabaseName: "orders-copy", Kind: kind, SlotUntil: slotUntil, RecordedAt: recordedAt}
	if kind == "expired" {
		notice.RestorableUntil = slotUntil.Add(api.ManagedPostgresDeleteGracePeriod)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO managed_postgres_databases(id) VALUES($1)", notice.DatabaseID); err != nil {
		t.Fatal(err)
	}
	var restorable any
	if !notice.RestorableUntil.IsZero() {
		restorable = notice.RestorableUntil
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO managed_postgres_restore_slot_notices(database_id,kind,slot_until,restorable_until,recorded_at)
		VALUES($1,$2,$3,$4,$5)`, notice.DatabaseID, kind, slotUntil, restorable, recordedAt); err != nil {
		t.Fatal(err)
	}
	return notice
}

func restoreSlotIntents(t *testing.T, f *fixture) []string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT id::text FROM platform_mail_producer_outbox WHERE template='managed_postgres_restore_slot' ORDER BY created_at")
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

func TestManagedPostgresRestoreSlotDisabledReadsOnlyOwnGate(t *testing.T) {
	var service *Service
	for _, value := range []string{"", "0", "true", " 1"} {
		if err := service.ConfigureManagedPostgresRestoreSlot(t.Context(), func(k string) string {
			if k != internalmail.ManagedPostgresRestoreSlotOutboxEnableEnv {
				t.Fatal("disabled config read", k)
			}
			return value
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.NoticeManagedPostgresRestoreSlot(t.Context(), uuid.NewString(), ManagedPostgresRestoreSlotNotice{}); !errors.Is(err, internalmail.ErrDisabled) {
		t.Fatal(err)
	}
}

func TestPGManagedPostgresRestoreSlotQueuesOnceAndDeliversTheSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		kind       string
		slotUntil  time.Time
		recordedAt time.Time
		expires    time.Time
		subject    string
		body       string
	}{
		// An 'ending' email expires with the slot when that comes first.
		{"ending", now.Add(6 * time.Hour), now.Add(-time.Hour), now.Add(6 * time.Hour), "will be deleted on", "delete another database or upgrade your plan"},
		{"expired", now.Add(-time.Hour), now.Add(-time.Minute), now.Add(-time.Minute + api.InternalMailManagedPostgresRestoreSlotTTL), "scheduled for deletion", "gregale postgres recover orders-copy"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f := restoreSlotFixture(t)
			id := account(t, f)
			notice := restoreSlotNotice(t, f, tc.kind, tc.slotUntil, tc.recordedAt)
			for range 2 {
				if err := f.service.NoticeManagedPostgresRestoreSlot(t.Context(), id, notice); err != nil {
					t.Fatal(err)
				}
			}
			intents := restoreSlotIntents(t, f)
			if len(intents) != 1 {
				t.Fatalf("intents %v; want one for a repeated notice", intents)
			}
			var linked string
			if err := f.pool.QueryRow(t.Context(), "SELECT mail_intent_id::text FROM managed_postgres_restore_slot_notices WHERE database_id=$1", notice.DatabaseID).Scan(&linked); err != nil || linked != intents[0] {
				t.Fatalf("notice linked to %q, %v; want %s", linked, err, intents[0])
			}
			r := row(t, f, intents[0])
			if !r.ExpiresAt.Time.Equal(tc.expires) || strings.Contains(string(r.SealedSecurityAlert), "orders-copy") {
				t.Fatalf("intent expires %v, want %v, or stored the snapshot unsealed", r.ExpiresAt.Time, tc.expires)
			}
			if err := f.service.DeliverOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(f.sender.calls) != 1 {
				after := row(t, f, intents[0])
				t.Fatalf("sent %d messages; intent %s %s", len(f.sender.calls), after.State, after.Code)
			}
			message := f.sender.calls[0].message
			if message.MessageID != "managed-postgres-restore-slot:"+intents[0] || !strings.Contains(message.Subject, tc.subject) ||
				!strings.Contains(message.TextBody, tc.body) || message.To[0] != "one@example.net" {
				t.Fatalf("message %+v lost the snapshot", message)
			}
			assertMFAAlertErased(t, row(t, f, intents[0]))
		})
	}
}

func TestPGManagedPostgresRestoreSlotSkipsWhatItMustNotMail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name       string
		kind       string
		slotUntil  time.Time
		recordedAt time.Time
		setup      string
		noAccount  bool
	}{
		{name: "older than the email's lifetime", kind: "expired", slotUntil: now.Add(-48 * time.Hour), recordedAt: now.Add(-api.InternalMailManagedPostgresRestoreSlotTTL - time.Minute)},
		{name: "an ending notice past its slot", kind: "ending", slotUntil: now.Add(-time.Minute), recordedAt: now.Add(-time.Hour)},
		{name: "inactive account", kind: "ending", slotUntil: now.Add(time.Hour), recordedAt: now, setup: "UPDATE accounts SET status='deleted_pending' WHERE id=$1"},
		{name: "account being deleted", kind: "expired", slotUntil: now, recordedAt: now, setup: "UPDATE accounts SET deletion_requested_at=now() WHERE id=$1"},
		{name: "account gone", kind: "ending", slotUntil: now.Add(time.Hour), recordedAt: now, noAccount: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := restoreSlotFixture(t)
			id := uuid.NewString()
			if !tc.noAccount {
				id = account(t, f)
			}
			if tc.setup != "" {
				if _, err := f.pool.Exec(t.Context(), tc.setup, id); err != nil {
					t.Fatal(err)
				}
			}
			notice := restoreSlotNotice(t, f, tc.kind, tc.slotUntil, tc.recordedAt)
			if err := f.service.NoticeManagedPostgresRestoreSlot(t.Context(), id, notice); err != nil {
				t.Fatal(err)
			}
			if intents := restoreSlotIntents(t, f); len(intents) != 0 {
				t.Fatalf("queued %v", intents)
			}
		})
	}
}
