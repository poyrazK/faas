// adr: 20261025094915 — cap events are recorded once per subject, UTC month
// and threshold, and the suspension time is read back for the usage view.
package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresStoreCapEvents(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	event := CapEvent{SubjectID: accountID, PeriodStart: month, Threshold: CapThresholdSuspended,
		ReachedAt: month.Add(72 * time.Hour), Used: 90000, Allowance: 90000, Ceiling: 90000}
	if at, err := store.CapSuspendedAt(ctx, accountID, month); err != nil || at != nil {
		t.Fatalf("before any event: %v, %v", at, err)
	}
	if recorded, err := store.RecordCapEvent(ctx, event); err != nil || !recorded {
		t.Fatalf("first record: %v, %v", recorded, err)
	}
	repeat := event
	repeat.ReachedAt = event.ReachedAt.Add(time.Hour)
	if recorded, err := store.RecordCapEvent(ctx, repeat); err != nil || recorded {
		t.Fatalf("repeat record: %v, %v", recorded, err)
	}
	warning := event
	warning.Threshold, warning.ReachedAt = CapThresholdWarning, month.Add(time.Hour)
	if recorded, err := store.RecordCapEvent(ctx, warning); err != nil || !recorded {
		t.Fatalf("warning record: %v, %v", recorded, err)
	}
	severe, err := store.MostSevereCapEvent(ctx, accountID, month.Add(time.Hour))
	if err != nil || severe == nil || severe.Threshold != CapThresholdSuspended || severe.Mailed || !severe.ReachedAt.Equal(event.ReachedAt) ||
		severe.Used != event.Used || severe.Ceiling != event.Ceiling || !severe.PeriodStart.Equal(month) {
		t.Fatalf("most severe %+v, %v; want the unmailed suspension", severe, err)
	}
	if next, err := store.MostSevereCapEvent(ctx, accountID, month.AddDate(0, 1, 0)); err != nil || next != nil {
		t.Fatalf("next month: %+v, %v", next, err)
	}
	// ADR-20261110160000: the egress hold is read on its own and never
	// ranks among the compute thresholds.
	held := CapEvent{SubjectID: accountID, PeriodStart: month, Threshold: CapThresholdEgressHeld,
		ReachedAt: month.Add(96 * time.Hour), Used: 600 << 30, Allowance: 500 << 30, Ceiling: 500 << 30}
	if recorded, err := store.RecordCapEvent(ctx, held); err != nil || !recorded {
		t.Fatalf("egress hold record: %v, %v", recorded, err)
	}
	if severe, err := store.MostSevereCapEvent(ctx, accountID, month); err != nil || severe == nil || severe.Threshold != CapThresholdSuspended {
		t.Fatalf("most severe with an egress hold %+v, %v; want the suspension", severe, err)
	}
	read, err := store.CapEvent(ctx, accountID, month.Add(time.Hour), CapThresholdEgressHeld)
	if err != nil || read == nil || read.Used != held.Used || read.Ceiling != held.Ceiling || read.Mailed || !read.ReachedAt.Equal(held.ReachedAt) {
		t.Fatalf("egress hold read %+v, %v", read, err)
	}
	if none, err := store.CapEvent(ctx, accountID, month, CapThresholdCeilingWarning); err != nil || none != nil {
		t.Fatalf("absent threshold %+v, %v", none, err)
	}
	at, err := store.CapSuspendedAt(ctx, accountID, month.Add(10*24*time.Hour))
	if err != nil || at == nil || !at.Equal(event.ReachedAt) {
		t.Fatalf("suspended at %v, %v; want the first record's %v", at, err, event.ReachedAt)
	}
	for name, bad := range map[string]CapEvent{
		"mid-month period":   {SubjectID: accountID, PeriodStart: month.Add(time.Hour), Threshold: CapThresholdWarning, ReachedAt: month.Add(2 * time.Hour), Allowance: 1, Ceiling: 1},
		"unknown threshold":  {SubjectID: accountID, PeriodStart: month, Threshold: "nearly", ReachedAt: month, Allowance: 1, Ceiling: 1},
		"ceiling below plan": {SubjectID: accountID, PeriodStart: month, Threshold: CapThresholdWarning, ReachedAt: month, Allowance: 2, Ceiling: 1},
	} {
		if _, err := store.RecordCapEvent(ctx, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := store.RecordCapEvent(ctx, CapEvent{SubjectID: uuid.NewString(), PeriodStart: month, Threshold: CapThresholdWarning,
		ReachedAt: month, Allowance: 1, Ceiling: 1}); err == nil {
		t.Fatal("event stored for a subject that is not a resource namespace")
	}
}
