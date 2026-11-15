package state_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A pending email-assisted MFA disable is visible until it is cancelled, and
// cancelling reports how many requests it ended (F14-1, ADR-20261115100000).
func mfaDisablePendingContract(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	pending, ok := store.(state.MFADisablePendingStore)
	if !ok {
		t.Fatal("store has no pending MFA disable support")
	}
	acct, err := store.CreateAccount(ctx, "mfa-pending@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if at, err := pending.PendingMFADisableRequestedAt(ctx, acct.ID); err != nil || at != nil {
		t.Fatalf("pending before any request = %v %v, want none", at, err)
	}
	token := make([]byte, 32)
	_, _ = rand.Read(token)
	requested := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.IssueMFADisableRequest(ctx, api.HashToken(token), acct.ID, requested); err != nil {
		t.Fatal(err)
	}
	at, err := pending.PendingMFADisableRequestedAt(ctx, acct.ID)
	if err != nil || at == nil || !at.Equal(requested) {
		t.Fatalf("pending after a request = %v %v, want %v", at, err, requested)
	}
	if n, err := pending.CancelPendingMFADisable(ctx, acct.ID); err != nil || n != 1 {
		t.Fatalf("cancel = %d %v, want 1", n, err)
	}
	if at, err := pending.PendingMFADisableRequestedAt(ctx, acct.ID); err != nil || at != nil {
		t.Fatalf("pending after cancel = %v %v, want none", at, err)
	}
	if _, err := store.GetMFADisableRequest(ctx, api.HashToken(token)); err == nil {
		t.Fatal("a cancelled request can still be read as live")
	}
	if n, err := pending.CancelPendingMFADisable(ctx, acct.ID); err != nil || n != 0 {
		t.Fatalf("second cancel = %d %v, want 0", n, err)
	}
}

func TestMFADisablePendingMem(t *testing.T) {
	mfaDisablePendingContract(t, state.NewMemStore())
}

func TestMFADisablePendingPG(t *testing.T) {
	mfaDisablePendingContract(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
