package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMemStoreCoverageQueueIntrospection(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	now := time.Now()

	// QueueState on an empty app → zero stats.
	if s, err := m.QueueState(ctx, app.ID); err != nil || s.Depth != 0 || s.InFlight != 0 {
		t.Fatalf("queue state empty = %+v, %v", s, err)
	}
	if s, err := m.QueueState(ctx, "missing"); err != nil || s.Depth != 0 {
		t.Fatalf("queue state missing = %+v, %v", s, err)
	}
	// Enqueue three queue-source rows: two pending, one dispatching with a
	// live lease, one dispatching with an expired lease.
	p1, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationPending, DueAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationPending, DueAt: now, CreatedAt: now.Add(-30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	disp, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationDispatching, DueAt: now, CreatedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	lease := now.Add(5 * time.Minute)
	disp.LeaseExpiresAt = &lease
	if err := m.stampInvocationRowForTest(disp); err != nil {
		t.Fatal(err)
	}
	expiredDisp, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationDispatching, DueAt: now, CreatedAt: now.Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	oldLease := now.Add(-time.Minute)
	expiredDisp.LeaseExpiresAt = &oldLease
	if err := m.stampInvocationRowForTest(expiredDisp); err != nil {
		t.Fatal(err)
	}
	// A non-queue source (cron) must not count toward depth.
	if _, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationCron, State: InvocationPending, DueAt: now}); err != nil {
		t.Fatal(err)
	}
	stats, err := m.QueueState(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Depth != 4 || stats.InFlight != 1 {
		t.Fatalf("queue state = %+v", stats)
	}
	if stats.OldestPendingAt.IsZero() || !stats.OldestPendingAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("oldest pending = %v", stats.OldestPendingAt)
	}

	// QueuePeek — oldest-first, limit clamp, cursor.
	if got, err := m.QueuePeek(ctx, app.ID, 0, ""); err != nil || len(got) != 2 {
		t.Fatalf("peek default limit = %d, %v", len(got), err)
	}
	peeked, err := m.QueuePeek(ctx, app.ID, 1, "")
	if err != nil || len(peeked) != 1 || peeked[0].ID != p1.ID {
		t.Fatalf("peek limit 1 = %+v, %v", peeked, err)
	}
	// Unknown cursor → empty (the impl returns nil,nil rather than
	// falling back to "start from oldest").
	if got, err := m.QueuePeek(ctx, app.ID, 10, "not-a-row"); err != nil || len(got) != 0 {
		t.Fatalf("peek unknown cursor = %d, %v", len(got), err)
	}
	// Cursor after p1 → p2 only.
	after, err := m.QueuePeek(ctx, app.ID, 10, p1.ID)
	if err != nil || len(after) != 1 || after[0].ID != p2.ID {
		t.Fatalf("peek after cursor = %+v, %v", after, err)
	}
	// QueueDeadLetter — empty first, then a dead-letter row.
	if got, err := m.QueueDeadLetter(ctx, app.ID, 10, ""); err != nil || len(got) != 0 {
		t.Fatalf("dead letter empty = %d, %v", len(got), err)
	}
	dl, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationDeadLetter, DueAt: now, CreatedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	dl2, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationQueue, State: InvocationDeadLetter, DueAt: now, CreatedAt: now.Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.QueueDeadLetter(ctx, app.ID, 0, ""); err != nil || len(got) != 2 {
		t.Fatalf("dead letter rows = %d, %v", len(got), err)
	}
	// Newest-first.
	dlRows, _ := m.QueueDeadLetter(ctx, app.ID, 10, "")
	if dlRows[0].ID != dl.ID || dlRows[1].ID != dl2.ID {
		t.Fatalf("dead letter order = %s, %s", dlRows[0].ID, dlRows[1].ID)
	}
	// Cursor (strictly older than anchor).
	if got, err := m.QueueDeadLetter(ctx, app.ID, 10, dl.ID); err != nil || len(got) != 1 || got[0].ID != dl2.ID {
		t.Fatalf("dead letter cursor = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageInvocationStamps(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	minute := time.Now().UTC().Truncate(time.Minute)
	// DueAt must fall INSIDE the minute window for
	// CountInstanceInvocationsInMinute to count it.
	inv, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationAsyncInvoke, DueAt: minute.Add(15 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	// StampInstanceInvocation on a pending row → ErrNotFound.
	if err := m.StampInstanceInvocation(ctx, inv.ID, "inst-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stamp pending = %v", err)
	}
	// Claim → dispatching, then stamp succeeds.
	claimed, err := m.ClaimInvocation(ctx, inv.ID, "inst-0", 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StampInstanceInvocation(ctx, claimed.ID, "inst-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.StampInstanceInvocation(ctx, "missing", "inst-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stamp missing = %v", err)
	}
	// CountInstanceInvocationsInMinute — dispatching rows in the minute.
	if n, err := m.CountInstanceInvocationsInMinute(ctx, "inst-1", minute); err != nil || n != 1 {
		t.Fatalf("count invocations in minute = %d, %v", n, err)
	}
	// A second dispatching row in the same minute for a different
	// instance must not count.
	inv2, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationAsyncInvoke, DueAt: minute.Add(30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimInvocation(ctx, inv2.ID, "inst-2", 30); err != nil {
		t.Fatal(err)
	}
	if n, _ := m.CountInstanceInvocationsInMinute(ctx, "inst-1", minute); n != 1 {
		t.Fatalf("count after other instance = %d", n)
	}
	// Completed rows are excluded (non-dispatching).
	inv3, err := m.EnqueueInvocation(ctx, Invocation{AccountID: account.ID, AppID: app.ID, Source: InvocationAsyncInvoke, DueAt: minute.Add(45 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimInvocation(ctx, inv3.ID, "inst-1", 30); err != nil {
		t.Fatal(err)
	}
	if err := m.CompleteInvocation(ctx, inv3.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if n, _ := m.CountInstanceInvocationsInMinute(ctx, "inst-1", minute); n != 1 {
		t.Fatalf("count after complete = %d", n)
	}
}

// stampInvocationRowForTest mutates an invocation row in place (the
// public API has no "set lease" seam; the queue tests need a row whose
// LeaseExpiresAt is already populated before QueueState reads it).
func (m *MemStore) stampInvocationRowForTest(inv Invocation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invocations[inv.ID] = inv
	return nil
}

func TestMemStoreCoverageInstances(t *testing.T) {
	m, ctx, account, app, deployment := memCoverageFixture(t)
	now := time.Now()

	// CreateInstance + InstanceByID (hit/miss).
	ins, err := m.CreateInstance(ctx, app.ID, deployment.ID, string(StateRunning), 512, DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.InstanceByID(ctx, ins.ID); err != nil || got.ID != ins.ID {
		t.Fatalf("instance by id = %+v, %v", got, err)
	}
	if _, err := m.InstanceByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("instance by id missing = %v", err)
	}
	// UpdateInstanceState (hit/miss).
	if err := m.UpdateInstanceState(ctx, ins.ID, string(StateWaking)); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateInstanceState(ctx, "missing", string(StateWaking)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update state missing = %v", err)
	}
	// ListInstancesForAccountPaged — clamp + cursor + ordering.
	if got, err := m.ListInstancesForAccountPaged(ctx, account.ID, 0, ""); err != nil || len(got) != 1 {
		t.Fatalf("instances paged default = %d, %v", len(got), err)
	}
	if got, err := m.ListInstancesForAccountPaged(ctx, account.ID, 1000, ""); err != nil || len(got) != 1 {
		t.Fatalf("instances paged clamp = %d, %v", len(got), err)
	}
	if got, err := m.ListInstancesForAccountPaged(ctx, account.ID, 10, ins.ID); err != nil || len(got) != 0 {
		t.Fatalf("instances paged cursor = %d, %v", len(got), err)
	}
	ins2, err := m.CreateInstance(ctx, app.ID, deployment.ID, string(StateRunning), 256, DefaultLocalNodeName, "wake-2")
	if err != nil {
		t.Fatal(err)
	}
	// ListInstancesForAccount — both rows.
	if got, err := m.ListInstancesForAccount(ctx, account.ID); err != nil || len(got) != 2 {
		t.Fatalf("instances for account = %d, %v", len(got), err)
	}
	// ListLatestInstancePerApp — one entry per app, newest wins.
	latest, err := m.ListLatestInstancePerApp(ctx, account.ID)
	if err != nil || len(latest) != 1 {
		t.Fatalf("latest per app = %+v, %v", latest, err)
	}
	if latest[app.ID].ID != ins2.ID {
		t.Fatalf("latest per app = %s, want %s", latest[app.ID].ID, ins2.ID)
	}
	// RunningInstanceForApp — newest running wins, then missing.
	if got, err := m.RunningInstanceForApp(ctx, app.ID); err != nil || got.ID != ins2.ID {
		t.Fatalf("running instance = %+v, %v", got, err)
	}
	// Non-running state → ErrNotFound.
	if err := m.UpdateInstanceState(ctx, ins2.ID, string(StateStopped)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunningInstanceForApp(ctx, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("running none = %v", err)
	}
	// UpsertNodeKey / LookupNodeKey.
	if err := m.UpsertNodeKey(ctx, DefaultLocalNodeName, "key-1", "pem-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertNodeKey(ctx, DefaultLocalNodeName, "key-1", "pem-2"); err != nil {
		t.Fatal("re-insert same key should be no-op")
	}
	if pem, ok := m.LookupNodeKey(ctx, DefaultLocalNodeName, "key-1"); !ok || pem != "pem-1" {
		t.Fatalf("lookup node key = %q, %v", pem, ok)
	}
	if _, ok := m.LookupNodeKey(ctx, DefaultLocalNodeName, "missing"); ok {
		t.Fatal("lookup missing key should be false")
	}
	if _, ok := m.LookupNodeKey(ctx, "", ""); ok {
		t.Fatal("lookup empty key should be false")
	}
	_ = now
}

func TestMemStoreCoverageMeteringAndInvoices(t *testing.T) {
	m, ctx, account, app, _ := memCoverageFixture(t)
	now := time.Now().UTC()
	minute := now.Truncate(time.Minute)

	// AppendBuilderUsage — first write wins (idempotent on build_id).
	if err := m.AppendBuilderUsage(ctx, account.ID, app.ID, "build-1", now, "dockerfile", 120); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendBuilderUsage(ctx, account.ID, app.ID, "build-1", now, "dockerfile", 999); err != nil {
		t.Fatal(err)
	}
	// UsageDaily / AppendSnapshotStorage / StorageUsage / LatestSnapshotBytes
	// are no-op MemStore surfaces (PgStore owns the real rollup).
	if got, err := m.UsageDaily(ctx, account.ID, minute); err != nil || got != nil {
		t.Fatalf("usage daily = %+v, %v", got, err)
	}
	if err := m.AppendSnapshotStorage(ctx, account.ID, app.ID, minute, 100, 200); err != nil {
		t.Fatal(err)
	}
	if got, err := m.StorageUsage(ctx, account.ID, minute); err != nil || got != nil {
		t.Fatalf("storage usage = %+v, %v", got, err)
	}
	if mb, disk, err := m.LatestSnapshotBytes(ctx, app.ID); err != nil || mb != 0 || disk != 0 {
		t.Fatalf("latest snapshot bytes = %d/%d, %v", mb, disk, err)
	}
	// GetInvoiceByID — hit + miss. SeedInvoiceForTest is the test seam;
	// PeriodEnd must fall inside the month the list filter uses.
	// Keep the fixture inside the selected month even when the suite runs
	// during the final two hours of a UTC month.
	periodEnd := time.Date(minute.Year(), minute.Month(), 15, 12, 0, 0, 0, time.UTC)
	m.SeedInvoiceForTest(Invoice{ID: "inv-1", AccountID: account.ID, Provider: "stripe", Status: "paid", TotalCents: 100, PeriodEnd: periodEnd})
	if got, err := m.GetInvoiceByID(ctx, "inv-1"); err != nil || got.ID != "inv-1" {
		t.Fatalf("invoice by id = %+v, %v", got, err)
	}
	if _, err := m.GetInvoiceByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invoice missing = %v", err)
	}
	// ListInvoicesForAccount with a month filter + limit clamp.
	month := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC)
	if got, err := m.ListInvoicesForAccount(ctx, account.ID, &month, time.Time{}, 0); err != nil || len(got) != 1 {
		t.Fatalf("list invoices = %+v, %v", got, err)
	}
	if got, err := m.ListInvoicesForAccount(ctx, account.ID, nil, time.Time{}, 10); err != nil || len(got) != 1 {
		t.Fatalf("list invoices no filter = %+v, %v", got, err)
	}
	// boolOrFalse helper (nil → false, non-nil → value).
	if boolOrFalse(nil) {
		t.Fatal("boolOrFalse(nil) should be false")
	}
	tf := true
	if !boolOrFalse(&tf) {
		t.Fatal("boolOrFalse(&true) should be true")
	}
	f := false
	if boolOrFalse(&f) {
		t.Fatal("boolOrFalse(&false) should be false")
	}
}
