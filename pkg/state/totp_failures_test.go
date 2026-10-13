// adr: 1152
// totp_failures_test.go pins the shared per-account TOTP failure window on
// both stores: failures count inside the window, the oldest is reported for
// Retry-After, and a reset clears the account.

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_TOTPFailureWindow(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "totp-mem@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runTOTPFailureContract(t, ctx, store, acct.ID)
}

func TestPg_TOTPFailureWindow(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "totp-pg@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runTOTPFailureContract(t, ctx, s, acct.ID)
}

func runTOTPFailureContract(t *testing.T, ctx context.Context, s state.TOTPFailureStore, accountID string) {
	t.Helper()
	const window = 15 * time.Minute
	w, err := s.TOTPFailures(ctx, accountID, window)
	if err != nil || w.Failures != 0 {
		t.Fatalf("fresh window = %+v, err=%v", w, err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordTOTPFailure(ctx, accountID, window); err != nil {
			t.Fatal(err)
		}
	}
	w, err = s.TOTPFailures(ctx, accountID, window)
	if err != nil || w.Failures != 3 {
		t.Fatalf("after three failures = %+v, err=%v", w, err)
	}
	if w.Oldest.After(w.CheckedAt) || w.CheckedAt.Sub(w.Oldest) > time.Minute {
		t.Fatalf("oldest %v not just before check %v", w.Oldest, w.CheckedAt)
	}
	// A one-second window has already moved past failures recorded a
	// moment ago once a second has passed.
	time.Sleep(1100 * time.Millisecond)
	if short, err := s.TOTPFailures(ctx, accountID, time.Second); err != nil || short.Failures != 0 {
		t.Fatalf("one-second window after 1.1 s = %+v, err=%v", short, err)
	}
	if err := s.ResetTOTPFailures(ctx, accountID); err != nil {
		t.Fatal(err)
	}
	if w, err := s.TOTPFailures(ctx, accountID, window); err != nil || w.Failures != 0 {
		t.Fatalf("after reset = %+v, err=%v", w, err)
	}
	if err := s.RecordTOTPFailure(ctx, "not-a-uuid", window); err == nil {
		if _, isMem := s.(*state.MemStore); !isMem {
			t.Fatal("non-UUID account id accepted by the Postgres store")
		}
	}
}
