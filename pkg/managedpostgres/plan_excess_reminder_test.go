// adr: 20261110184700 — amendment 2026-11-12: a week before a database past
// its plan is deleted, its owner is reminded once, naming the date the first
// email named.
package managedpostgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemoryStorePlanExcessRemindersDue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	dates := map[string]time.Time{
		"soon":     now.Add(6 * 24 * time.Hour),
		"week":     now.Add(api.ManagedPostgresPlanExcessDeleteReminder),
		"later":    now.Add(8 * 24 * time.Hour),
		"past":     now.Add(-time.Hour),
		"deleting": now.Add(24 * time.Hour),
		"unmailed": now.Add(24 * time.Hour),
	}
	for id, deleteAt := range dates {
		state := StateReady
		if id == "deleting" {
			state = StateDeleting
		}
		store.databases[id] = Database{ID: id, AccountID: "acct", Name: id, State: state, Spec: Spec{Class: ClassDevelopment}}
		store.planExcessNotices[id] = memoryPlanExcessNotice{PlanExcessNotice: PlanExcessNotice{DatabaseID: id, Reason: PlanExcessDatabaseLimit,
			RecordedAt: deleteAt.Add(-api.ManagedPostgresPlanExcessDeleteAfter)}, mailed: id != "unmailed", deleteAt: deleteAt}
	}
	due, err := store.PlanExcessRemindersDue(ctx, now, 10)
	if err != nil || len(due) != 2 || due[0].DatabaseID != "soon" || due[1].DatabaseID != "week" {
		t.Fatalf("due = %+v, %v; want soon then week", due, err)
	}
	for _, n := range due {
		if !n.Reminder || !n.DeleteAt.Equal(dates[n.DatabaseID]) || !n.RecordedAt.Equal(now) || n.AccountID != "acct" || n.DatabaseName != n.DatabaseID {
			t.Fatalf("reminder %+v", n)
		}
	}
	store.markPlanExcessNoticeReminded("soon")
	if due, err := store.PlanExcessRemindersDue(ctx, now, 10); err != nil || len(due) != 1 || due[0].DatabaseID != "week" {
		t.Fatalf("after a reminder = %+v, %v; want week only", due, err)
	}
	if _, err := store.PlanExcessRemindersDue(ctx, now, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero limit = %v", err)
	}
}

func TestCapEnforcerRemindsBeforePlanExcessDeletes(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		deleteMode CapEnforcementMode
		notifyErr  error
		want       int
	}{
		{name: "deletion on reminds", deleteMode: CapEnforcementOn, want: 1},
		{name: "deletion in shadow does not", deleteMode: CapEnforcementShadow},
		{name: "a failed email is retried later", deleteMode: CapEnforcementOn, notifyErr: errors.New("outbox unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.databases["db"] = Database{ID: "db", AccountID: "acct", Name: "orders", State: StateReady, Spec: Spec{Class: ClassDevelopment}}
			deleteAt := now.Add(3 * 24 * time.Hour)
			store.planExcessNotices["db"] = memoryPlanExcessNotice{PlanExcessNotice: PlanExcessNotice{DatabaseID: "db", Reason: PlanExcessDatabaseLimit,
				RecordedAt: deleteAt.Add(-api.ManagedPostgresPlanExcessDeleteAfter)}, mailed: true, deleteAt: deleteAt}
			var sent []PlanExcessNotice
			e := &CapEnforcer{store: store, now: func() time.Time { return now }, batchSize: 10, planExcessDelete: tc.deleteMode,
				planDelete: func(context.Context, Database) (bool, error) { return false, nil }, logger: slog.New(slog.DiscardHandler),
				planNotify: func(_ context.Context, n PlanExcessNotice) error {
					if tc.notifyErr != nil {
						return tc.notifyErr
					}
					sent = append(sent, n)
					store.markPlanExcessNoticeReminded(n.DatabaseID)
					return nil
				}}
			var summary CapSweepSummary
			e.remindPlanExcessDeletes(context.Background(), &summary)
			if len(sent) != tc.want || summary.PlanExcessReminded != tc.want {
				t.Fatalf("sent %+v, summary %+v; want %d reminders", sent, summary, tc.want)
			}
			if tc.want == 1 && (!sent[0].Reminder || !sent[0].DeleteAt.Equal(deleteAt)) {
				t.Fatalf("reminder %+v", sent[0])
			}
			if tc.notifyErr != nil && summary.PlanExcessNotifyFailed != 1 {
				t.Fatalf("summary %+v; want the failure counted", summary)
			}
			// Reminded once: the next sweep sends nothing more.
			sent = nil
			e.remindPlanExcessDeletes(context.Background(), &summary)
			if tc.want == 1 && len(sent) != 0 {
				t.Fatalf("reminded again: %+v", sent)
			}
		})
	}
}
