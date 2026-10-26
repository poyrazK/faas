package grace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
)

// An account whose external cleanup keeps failing stays in deleted_pending;
// the sweep reports how many are held so the hold cannot outlast the DPA's
// deletion window unnoticed.
func TestRunOnce_ReportsAccountsHeldByExternalCleanup(t *testing.T) {
	store := state.NewMemStore()
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	registry := prometheus.NewRegistry()
	cleanupErr := errors.New("archive bucket unavailable")
	p := params(store, &recordingSender{}, makeNotifier(&recordingNotifier{}), nowFrozen(time.Now().Add(31*24*time.Hour)), runOnceInterval)
	p.Registry = registry
	p.BeforeAccountDelete = func(context.Context, state.Account) error { return cleanupErr }
	g := grace.New(p)

	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := metricValue(t, registry, "apid_account_grace_deferred_accounts"); got != 1 {
		t.Fatalf("deferred accounts = %v, want 1", got)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); err != nil {
		t.Fatalf("account deleted while its cleanup failed: %v", err)
	}

	cleanupErr = nil
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := metricValue(t, registry, "apid_account_grace_deferred_accounts"); got != 0 {
		t.Fatalf("deferred accounts = %v after cleanup succeeded, want 0", got)
	}
}
