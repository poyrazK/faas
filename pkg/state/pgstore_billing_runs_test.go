//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1503 — runs discover customers from the month's billable usage,
// lease their items to one worker, and schedules start one run per month.
func TestPgBillingRunLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "billing-run-"+uuid.NewString()[:8], "Billing run tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := store.CreateAPIConsumer(ctx, accountID, appID, "cus_Direct", "Direct")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := store.CreateAPIConsumer(ctx, accountID, appID, "linked", "Linked")
	if err != nil {
		t.Fatal(err)
	}
	idle, err := store.CreateAPIConsumer(ctx, accountID, appID, "idle", "Idle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, linked.ID); err != nil {
		t.Fatal(err)
	}
	sept := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range []state.APIConsumerUsageEvent{
		{ConsumerKey: direct.ID, WindowStart: sept.Add(time.Hour), RequestCount: 3, BillableUnits: 3},
		{ConsumerKey: linked.ID, PlatformTenantID: tenant.ID, WindowStart: sept.Add(2 * time.Hour), RequestCount: 2, BillableUnits: 2},
		{ConsumerKey: idle.ID, WindowStart: sept.AddDate(0, 1, 0), RequestCount: 4, BillableUnits: 4},
	} {
		e.EventID, e.AccountID, e.AppID = uuid.NewString(), accountID, appID
		if _, err := store.RecordAPIConsumerUsage(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	opts := state.BillingRunOptions{Sources: []string{state.BillingRunSourcePlatformTenants, state.BillingRunSourceAppConsumers},
		Finalize: true, Export: state.BillingRunExportStripe, HoldOnGaps: true}
	run, err := store.CreateBillingRun(ctx, state.BillingRun{AccountID: accountID, PeriodStart: sept, PeriodEnd: sept.AddDate(0, 1, 0),
		BillingRunOptions: opts, Trigger: state.BillingRunTriggerAPI})
	if err != nil || run.Status != state.BillingRunQueued {
		t.Fatalf("CreateBillingRun = %+v, %v", run, err)
	}
	if active, err := store.CreateBillingRun(ctx, run); !errors.Is(err, state.ErrConflict) || active.ID != run.ID {
		t.Fatalf("second run = %s, %v; want ErrConflict naming %s", active.ID, err, run.ID)
	}

	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	claimed, err := store.ClaimBillingRun(ctx, "owner-1", now, time.Minute)
	if err != nil || claimed.ID != run.ID || claimed.Status != state.BillingRunRunning {
		t.Fatalf("ClaimBillingRun = %+v, %v", claimed, err)
	}
	if _, err := store.ClaimBillingRun(ctx, "owner-2", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim while leased: %v, want ErrNotFound", err)
	}
	if err := store.DiscoverBillingRunItems(ctx, run.ID, "owner-2", "tenant", now); !errors.Is(err, state.ErrBillingRunLeaseLost) {
		t.Fatalf("discover without lease: %v", err)
	}
	if err := store.DiscoverBillingRunItems(ctx, run.ID, "owner-1", "billed on the tenant statement", now); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListBillingRunItems(ctx, accountID, run.ID, "", "", 10)
	if err != nil || len(items) != 3 {
		t.Fatalf("items = %+v, %v; want direct, linked and the tenant", items, err)
	}
	outcomes := map[string]string{}
	for _, item := range items {
		outcomes[item.SourceID] = item.Outcome
	}
	if outcomes[direct.ID] != state.BillingRunItemPending || outcomes[linked.ID] != state.BillingRunItemSkipped ||
		outcomes[tenant.ID] != state.BillingRunItemPending {
		t.Fatalf("outcomes = %v", outcomes)
	}
	page, err := store.ListBillingRunItems(ctx, accountID, run.ID, "", items[0].Key(), 1)
	if err != nil || len(page) != 1 || page[0].Key() != items[1].Key() {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	due, err := store.ListDueBillingRunItems(ctx, run.ID, "owner-1", now, 10)
	if err != nil || len(due) != 2 || due[0].SourceKind != state.StatementSourcePlatformTenant {
		t.Fatalf("due = %+v, %v; want the tenant first, then the direct consumer", due, err)
	}

	tenantItem, directItem := due[0], due[1]
	directItem.Outcome, directItem.StatementID, directItem.Revision, directItem.Currency = state.BillingRunItemExported, uuid.NewString(), 1, "EUR"
	directItem.AmountMillicents, directItem.InvoiceID, directItem.ExportedCount = 3750, "in_1", 1
	if err := store.UpdateBillingRunItem(ctx, "owner-2", directItem); !errors.Is(err, state.ErrBillingRunLeaseLost) {
		t.Fatalf("update without lease: %v", err)
	}
	if err := store.UpdateBillingRunItem(ctx, "owner-1", directItem); err != nil {
		t.Fatal(err)
	}
	released, err := store.ReleaseBillingRun(ctx, run.ID, "owner-1", now)
	if err != nil || released.Status != state.BillingRunRunning || released.LeaseOwner != "" {
		t.Fatalf("release with a pending item = %+v, %v", released, err)
	}

	if _, err := store.ClaimBillingRun(ctx, "owner-2", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	tenantItem.Outcome, tenantItem.Reason = state.BillingRunItemHeld, "mixed currencies"
	if err := store.UpdateBillingRunItem(ctx, "owner-2", tenantItem); err != nil {
		t.Fatal(err)
	}
	done, err := store.ReleaseBillingRun(ctx, run.ID, "owner-2", now.Add(time.Minute))
	if err != nil || done.Status != state.BillingRunCompleted || done.CompletedAt == nil {
		t.Fatalf("release with no pending item = %+v, %v", done, err)
	}
	if done.Counts[state.BillingRunItemExported] != 1 || done.Counts[state.BillingRunItemSkipped] != 1 || done.Counts[state.BillingRunItemHeld] != 1 {
		t.Fatalf("counts = %v", done.Counts)
	}
	exported, err := store.ListBillingRunItems(ctx, accountID, run.ID, state.BillingRunItemExported, "", 10)
	if err != nil || len(exported) != 1 || exported[0].InvoiceID != "in_1" || exported[0].AmountMillicents != 3750 {
		t.Fatalf("exported items = %+v, %v", exported, err)
	}
	if runs, err := store.ListBillingRuns(ctx, accountID, 5); err != nil || len(runs) != 1 || runs[0].Counts[state.BillingRunItemHeld] != 1 {
		t.Fatalf("ListBillingRuns = %+v, %v", runs, err)
	}
	if _, err := store.StatementHandoffBlocked(ctx, accountID, state.StatementSourceAppConsumer, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("StatementHandoffBlocked(unknown) err = %v", err)
	}

	oct := sept.AddDate(0, 1, 0)
	sched, err := store.PutBillingRunSchedule(ctx, state.BillingRunSchedule{AccountID: accountID, BillingRunOptions: opts,
		DelayHours: 24, NextPeriodStart: oct, NextRunAt: oct.AddDate(0, 1, 0).Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if due, err := store.ListDueBillingRunSchedules(ctx, sched.NextRunAt.Add(-time.Second), 10); err != nil || len(due) != 0 {
		t.Fatalf("due before next_run_at = %+v, %v", due, err)
	}
	dueSchedules, err := store.ListDueBillingRunSchedules(ctx, sched.NextRunAt, 10)
	if err != nil || len(dueSchedules) != 1 {
		t.Fatalf("due schedules = %+v, %v", dueSchedules, err)
	}
	next := sched
	next.NextPeriodStart, next.NextRunAt = oct.AddDate(0, 1, 0), oct.AddDate(0, 2, 0).Add(24*time.Hour)
	scheduled, err := store.StartScheduledBillingRun(ctx, dueSchedules[0], state.BillingRun{AccountID: accountID, PeriodStart: oct,
		PeriodEnd: oct.AddDate(0, 1, 0), BillingRunOptions: opts, Trigger: state.BillingRunTriggerSchedule}, next)
	if err != nil || scheduled.Trigger != state.BillingRunTriggerSchedule {
		t.Fatalf("StartScheduledBillingRun = %+v, %v", scheduled, err)
	}
	if _, err := store.StartScheduledBillingRun(ctx, dueSchedules[0], state.BillingRun{AccountID: accountID, PeriodStart: oct,
		PeriodEnd: oct.AddDate(0, 1, 0), BillingRunOptions: opts, Trigger: state.BillingRunTriggerSchedule}, next); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale schedule start: %v, want ErrConflict", err)
	}
	advanced, err := store.GetBillingRunSchedule(ctx, accountID)
	if err != nil || !advanced.NextPeriodStart.Equal(next.NextPeriodStart) || advanced.LastPeriodStart == nil || !advanced.LastPeriodStart.Equal(oct) {
		t.Fatalf("advanced schedule = %+v, %v", advanced, err)
	}
	if err := store.DeleteBillingRunSchedule(ctx, accountID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBillingRunSchedule(ctx, accountID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
