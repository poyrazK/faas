package state_test

// adr: 701

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

func TestPgPublicWithdrawalCancelledPublicationKeepsFenceUntilStableRetry(t *testing.T) {
	s, pool, _, r := publicEdgeFixture(t)
	old := r.Members[0]
	_ = replacePublicSession(t, s, r)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE runtime_upgrade_public_edge_withdrawal_receipts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	tk := ingress.NewActivityTracker()
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(ctx, old, publicWithdrawalSnapshot(tk))
		done <- err
	}()
	waitPublicEdgeRead(t, pool, blocker)
	if _, err := tk.Begin(); err == nil {
		t.Fatal("blocked publication left admission open")
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("publication did not cancel", err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if pendingPublicWithdrawals(t, pool) != 1 {
		t.Fatal("cancelled publication sealed withdrawal")
	}
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM runtime_upgrade_public_edge_withdrawals`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	before, err := tk.Withdraw(id)
	if err != nil || !before.Closed || !before.Known || before.Active != 0 {
		t.Fatal(before, err)
	}
	if _, err := s.RepairRuntimeUpgradePublicEdgeWithdrawal(t.Context(), old, publicWithdrawalSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if after, err := tk.Withdraw(id); err != nil || after != before {
		t.Fatal("retry replaced local fence", after, before, err)
	}
	if pendingPublicWithdrawals(t, pool) != 0 {
		t.Fatal("stable retry did not seal")
	}
}
