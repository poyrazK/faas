package apidserver_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The TOTP attempt limit is shared by every apid through the store: ten
// attempts on one process use up the account's window on another, and a
// correct code on either clears it (ADR 20261110120000).
func TestTOTPAttemptsSharedAcrossProcesses(t *testing.T) {
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := apidserver.NewServer(store, log, "gregale.dev", apidserver.NoopNotifier{})
	second := apidserver.NewServer(store, log, "gregale.dev", apidserver.NoopNotifier{})
	acct, err := store.CreateAccount(t.Context(), "totp-shared@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < apidserver.TOTPMaxFailures; i++ {
		if wait := first.ReserveTOTPAttempt(t.Context(), acct.ID); wait != 0 {
			t.Fatalf("attempt %d on the first apid refused", i+1)
		}
	}
	if wait := second.ReserveTOTPAttempt(t.Context(), acct.ID); wait == 0 {
		t.Fatal("the second apid allowed an attempt past the account's limit")
	}
	first.ResetTOTPAttempts(t.Context(), acct.ID)
	if wait := second.ReserveTOTPAttempt(t.Context(), acct.ID); wait != 0 {
		t.Fatal("a correct code on one apid did not clear the shared count")
	}
}
