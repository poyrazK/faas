package state_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPendingMFAConfirmationMemory(t *testing.T) {
	checkPendingMFAConfirmation(t, state.NewMemStore())
}

func TestPendingMFAConfirmationPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	checkPendingMFAConfirmation(t, state.NewPgStore(pool))
}

func checkPendingMFAConfirmation(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "pending-mfa@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	enrollments := store.(state.MFAEnrollmentStore)
	first, current := []byte("first-factor-ciphertext"), []byte("current-factor-ciphertext")
	for _, encrypted := range [][]byte{first, current} {
		if saved, err := enrollments.SavePendingMFASecret(ctx, acct.ID, encrypted, nil); err != nil || !saved {
			t.Fatalf("save pending factor = %v, %v", saved, err)
		}
	}
	if confirmed, err := enrollments.ConfirmPendingMFA(ctx, acct.ID, first, 10); err != nil || confirmed {
		t.Fatalf("superseded factor confirmed: %v, %v", confirmed, err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			confirmed, err := enrollments.ConfirmPendingMFA(ctx, acct.ID, current, 10)
			if err != nil {
				t.Error(err)
			} else if confirmed {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted %d concurrent confirmations, want one", accepted.Load())
	}
	if saved, err := enrollments.SavePendingMFASecret(ctx, acct.ID, first, nil); err != nil || saved {
		t.Fatalf("setup replaced enrolled factor: %v, %v", saved, err)
	}
	if confirmed, err := enrollments.ConfirmPendingMFA(ctx, acct.ID, current, 11); err != nil || confirmed {
		t.Fatalf("already enrolled factor confirmed again: %v, %v", confirmed, err)
	}
	recorder := store.(state.TOTPStepRecorder)
	if used, err := recorder.AcceptTOTPStep(ctx, acct.ID, 10); err != nil || used {
		t.Fatalf("enrollment code replayed for verification: %v, %v", used, err)
	}
	after, err := store.AccountByID(ctx, acct.ID)
	if err != nil || !after.MFAEnrolled() || after.MFARequired || string(after.MFASecretEncrypted) != string(current) {
		t.Fatalf("enrollment state changed unexpectedly: %v", err)
	}
	if err := store.ClearMFA(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if saved, err := enrollments.SavePendingMFASecret(ctx, acct.ID, first, nil); err != nil || !saved {
		t.Fatalf("start replacement enrollment: %v, %v", saved, err)
	}
	if confirmed, err := enrollments.ConfirmPendingMFA(ctx, acct.ID, first, 10); err != nil || !confirmed {
		t.Fatalf("a new factor must have its own code history: %v, %v", confirmed, err)
	}
}
