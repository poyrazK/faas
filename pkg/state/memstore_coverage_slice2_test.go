package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreCoverageInvocations(t *testing.T) {
	m, ctx, account, app, _ := state.MemCoverageFixture(t)
	now := time.Now()
	enqueue := func(source state.InvocationSource) (state.Invocation, error) {
		return m.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: source})
	}
	enqueueDue := func(source state.InvocationSource, due time.Time) (state.Invocation, error) {
		return m.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: source, DueAt: due})
	}
	due, err := enqueueDue(state.InvocationCron, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InvocationByID(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing invocation = %v", err)
	}
	if got, err := m.InvocationByID(ctx, due.ID); err != nil || got.ID != due.ID {
		t.Fatalf("invocation lookup = %+v, %v", got, err)
	}
	if got, err := m.ListDueInvocations(ctx, now, 10); err != nil || len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("due list returned %d rows", len(got))
	}
	if _, err := m.ClaimInvocation(ctx, "missing", "instance", 30); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing claim = %v", err)
	}
	claimed, err := m.ClaimInvocation(ctx, due.ID, "instance", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimInvocation(ctx, due.ID, "instance", 30); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("double claim = %v", err)
	}
	if err := m.CompleteInvocation(ctx, "missing", json.RawMessage(`{}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing complete = %v", err)
	}
	if err := m.CompleteInvocation(ctx, claimed.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.FailInvocation(ctx, claimed.ID, "noop", 0, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fail completed = %v", err)
	}
	fail, err := enqueue(state.InvocationAsyncInvoke)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimInvocation(ctx, fail.ID, "instance", 30); err != nil {
		t.Fatal(err)
	}
	if err := m.FailInvocation(ctx, fail.ID, "boom", time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.FailInvocation(ctx, fail.ID, "perm", 0, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.InvocationByID(ctx, fail.ID); got.State != state.InvocationFailed {
		t.Fatalf("permanent fail = %+v", got)
	}
	cancel, err := enqueue(state.InvocationAsyncInvoke)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CancelInvocation(ctx, cancel.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelInvocation(ctx, cancel.ID); err != nil {
		t.Fatalf("cancel double = %v", err)
	}
	if err := m.CancelInvocation(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancel missing = %v", err)
	}
	if n, err := m.CountInstanceInvocationsInMinute(ctx, "instance", now); err != nil || n != 0 {
		t.Fatalf("count before stamp = %d, %v", n, err)
	}
	if err := m.StampInstanceInvocation(ctx, "missing", "instance"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stamp missing invocation = %v", err)
	}
	if _, err := m.ListInvocationsForAccount(ctx, account.ID, 10, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ListInvocationsForApp(ctx, "missing"); len(got) != 0 {
		t.Fatalf("missing invocations for app = %+v", got)
	}
	if _, err := m.CountInstanceInvocationsInMinute(ctx, "missing", now); err != nil {
		t.Fatal(err)
	}
}

func TestMemStoreCoveragePaddleAndInvoices(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if ok, _ := m.HasPaddleOverageMonth(ctx, account.ID, month); ok {
		t.Fatal("paddle overage preexists")
	}
	if err := m.RecordPaddleOverageMonth(ctx, account.ID, month); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.HasPaddleOverageMonth(ctx, account.ID, month); !ok {
		t.Fatal("paddle overage not recorded")
	}
	window := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)
	first, err := m.ClaimPaddleOverageWindow(ctx, account.ID, window, "pod-a", time.Minute)
	if err != nil || !first {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	dup, err := m.ClaimPaddleOverageWindow(ctx, account.ID, window, "pod-a", time.Minute)
	if err != nil || dup {
		t.Fatalf("duplicate claim while fresh = %v, %v", dup, err)
	}
	lost, err := m.ClaimPaddleOverageWindow(ctx, account.ID, window, "pod-b", time.Minute)
	if err != nil || lost {
		t.Fatalf("foreign claim should lose = %v, %v", lost, err)
	}
	if err := m.CompletePaddleOverageWindow(ctx, account.ID, window, 4096); err != nil {
		t.Fatal(err)
	}
	if err := m.CompletePaddleOverageWindow(ctx, account.ID, window.Add(time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimPaddleOverageWindow(ctx, account.ID, window, "pod-c", time.Minute); err != nil {
		t.Fatal(err)
	}
	if n, err := m.ReapStalePaddleOverageClaims(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("reap fresh = %d, %v", n, err)
	}
	m.SetPaddleOverageClaimForTest(account.ID, window, "pod-a", time.Now().Add(-2*time.Hour), true)
	if n, err := m.ReapStalePaddleOverageClaims(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("reap completed = %d, %v", n, err)
	}
	m.SetPaddleOverageClaimForTest(account.ID, window.Add(time.Hour), "pod-a", time.Now().Add(-2*time.Hour), false)
	if n, err := m.ReapStalePaddleOverageClaims(ctx, time.Hour); err != nil || n != 1 {
		t.Fatalf("reap stale pending = %d, %v", n, err)
	}
	if ok, err := m.HasPaddleOverageMonth(ctx, "missing", month); err != nil || ok {
		t.Fatalf("missing paddle month = %v, %v", ok, err)
	}
	if _, err := m.ListInvoicesForAccount(ctx, account.ID, nil, time.Now(), 10); err != nil {
		t.Fatal(err)
	}
	invoice := state.Invoice{ID: uuid.NewString(), AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 0, 15), TotalCents: 1234, Currency: "USD", CreatedAt: time.Now()}
	m.SeedInvoiceForTest(invoice)
	got, err := m.ListInvoicesForAccount(ctx, account.ID, &month, time.Time{}, 10)
	if err != nil || len(got) != 1 || got[0].ID != invoice.ID {
		t.Fatalf("invoice list = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageInstancesAndSnapshots(t *testing.T) {
	m, ctx, _, app, deployment := state.MemCoverageFixture(t)
	instance, err := m.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 512, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	m.BackdateForTest(instance.ID, time.Now().Add(-2*time.Hour))
	if err := m.UpdateInstanceStateToTerminal(ctx, instance.ID, string(state.StateStopped), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListInstancesInTerminalStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(-time.Hour)); err != nil || len(got) != 1 {
		t.Fatalf("terminal instance list = %+v, %v", got, err)
	}
	if got, err := m.ListInstancesByStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(-time.Hour)); err != nil || len(got) != 1 {
		t.Fatalf("state-age list = %+v, %v", got, err)
	}
	if got, err := m.ListInstancesInTerminalStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(-3*time.Hour)); err != nil || len(got) != 0 {
		t.Fatalf("terminal empty list = %+v, %v", got, err)
	}
	if _, err := m.ListLatestInstancePerApp(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetInstanceRuntime(ctx, instance.ID, "ns", "10.0.0.2", 20000); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListAllInstances(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LiveDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("no live deployment = %v", err)
	}
	if err := m.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs", "apps/rootfs", 10); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	live, err := m.LiveDeployment(ctx, app.ID)
	if err != nil || live.ID != deployment.ID {
		t.Fatalf("live deployment = %+v, %v", live, err)
	}
	if _, err := m.LatestSupersededDeployment(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("superseded missing = %v", err)
	}
	if _, err := m.ListDeploymentsForApp(ctx, app.ID, 10, 0); err != nil {
		t.Fatal(err)
	}
	snap := state.Snapshot{DeploymentID: deployment.ID, FCVersion: "v1", StorageKey: "snap/coverage"}
	created, err := m.CreateSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	m.SetSnapshotStorageKeyForTest(deployment.ID, "snap/updated")
	if got, _ := m.LatestSnapshot(ctx, deployment.ID); got.StorageKey != "snap/updated" {
		t.Fatalf("updated snapshot key = %+v", got)
	}
	if got, err := m.ListSnapshotsForGC(ctx); err != nil || len(got) != 1 {
		t.Fatalf("snapshot GC list = %+v, %v", got, err)
	}
	if err := m.MarkSnapshotStale(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListSnapshotsForGC(ctx); err != nil || len(got) != 0 {
		t.Fatalf("post-stale GC list = %+v, %v", got, err)
	}
	if n, err := m.DeleteSnapshotsByID(ctx, []string{created.ID, "missing"}); err != nil || n != 1 {
		t.Fatalf("delete snapshots = %d, %v", n, err)
	}
	if n, err := m.DeleteSnapshotsStaleOlderThan(ctx, time.Hour); err != nil || n < 0 {
		t.Fatalf("delete snapshots stale = %d, %v", n, err)
	}
}

func TestMemStoreCoverageCliCronAndInventory(t *testing.T) {
	m, ctx, account, app, _ := state.MemCoverageFixture(t)
	hash := []byte("peek-cli")
	if err := m.IssueCliAuthCode(ctx, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, got, err := m.PeekCliAuthCode(ctx, hash); err != nil || status != api.CliAuthStatusPending || got != "" {
		t.Fatalf("peek pending = %v, %q, %v", status, got, err)
	}
	if err := m.ClaimCliAuthCode(ctx, hash, account.ID); err != nil {
		t.Fatal(err)
	}
	if status, got, err := m.PeekCliAuthCode(ctx, hash); err != nil || status != api.CliAuthStatusPending || got != account.ID {
		t.Fatalf("peek after claim = %v, %q, %v", status, got, err)
	}
	if _, _, err := m.PeekCliAuthCode(ctx, []byte("missing")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("peek missing = %v", err)
	}
	if _, gotAccount, err := m.ConsumeCliAuthCode(ctx, hash); err != nil || gotAccount != account.ID {
		t.Fatalf("consume claimed code = %q, %v", gotAccount, err)
	}
	if status, gotAccount, err := m.PeekCliAuthCode(ctx, hash); err != nil || status != api.CliAuthStatusConsumed || gotAccount != account.ID {
		t.Fatalf("peek after consume = %v, %q, %v", status, gotAccount, err)
	}
	expired := []byte("peek-expired")
	if err := m.IssueCliAuthCode(ctx, expired, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.PeekCliAuthCode(ctx, expired); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("peek expired = %v", err)
	}
	loginHash := []byte("login-coverage")
	if err := m.IssueLoginToken(ctx, loginHash, account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConsumeLoginToken(ctx, loginHash); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteOldLoginTokens(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cron, err := m.CreateCron(ctx, app.ID, "* * * * *", "/", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.CronByID(ctx, cron.ID); err != nil || got.ID != cron.ID {
		t.Fatalf("cron lookup = %+v, %v", got, err)
	}
	if _, err := m.CronByID(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing cron = %v", err)
	}
	if _, err := m.ListDomainsForApp(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListAPIKeys(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListAPIKeys(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ListAllComputeNodes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.TouchKeyLastUsed(ctx, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("touch missing key = %v", err)
	}
}
