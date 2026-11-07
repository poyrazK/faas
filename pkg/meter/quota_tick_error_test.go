package meter

// adr: 20261104211412 — a quota sweep that could not run records a failed
// tick, so FaasMeterdQuotaFailing and meterd's health see it; a later clean
// sweep clears it.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type failingAccountList struct {
	MemStore *state.MemStore
	fail     *atomic.Bool
}

func (s failingAccountList) ListAllAccounts(ctx context.Context) ([]state.Account, error) {
	if s.fail.Load() {
		return nil, errors.New("database unavailable")
	}
	return s.MemStore.ListAllAccounts(ctx)
}

func TestQuotaTickRecordsASweepThatCouldNotRun(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	if _, err := mem.CreateAccount(ctx, "free@example.com", api.PlanFree); err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	fail.Store(true)
	now := time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC)
	loop := NewLoop(statetest.NewMem(failingAccountList{MemStore: mem, fail: &fail}), nil, nil, nil, nil, nil, nil, nil, nil,
		func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)

	if err := loop.runQuotaOnce(ctx); err == nil {
		t.Fatal("runQuotaOnce hid an account listing failure")
	}
	loop.RunQuotaTick(ctx)
	loop.lastTickMu.RLock()
	errText := loop.lastTickErr["quota"]
	loop.lastTickMu.RUnlock()
	if errText == "" {
		t.Fatal("the quota tick looked healthy after a sweep that could not list accounts")
	}

	fail.Store(false)
	loop.RunQuotaTick(ctx)
	loop.lastTickMu.RLock()
	errText = loop.lastTickErr["quota"]
	loop.lastTickMu.RUnlock()
	if errText != "" {
		t.Fatalf("a clean sweep kept the failure: %q", errText)
	}
}
