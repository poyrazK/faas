package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Issue #328: the email-assisted MFA disable store contract is one-shot,
// account-bound, and invalidates an older pending request.
func TestMemStoreMFADisableRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "mfa-disable@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}

	firstHash := []byte("first-mfa-disable-hash")
	requestedAt := time.Now().Add(-time.Hour)
	if err := store.IssueMFADisableRequest(ctx, firstHash, acct.ID, requestedAt); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMFADisableRequest(ctx, firstHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != acct.ID || !got.RequestedAt.Equal(requestedAt) {
		t.Fatalf("request = %+v, want account %q at %v", got, acct.ID, requestedAt)
	}

	secondHash := []byte("second-mfa-disable-hash")
	if err := store.IssueMFADisableRequest(ctx, secondHash, acct.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMFADisableRequest(ctx, firstHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("superseded request error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetMFADisableRequest(ctx, []byte("unknown")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown request error = %v, want ErrNotFound", err)
	}

	accountID, err := store.ConsumeMFADisableRequest(ctx, secondHash)
	if err != nil || accountID != acct.ID {
		t.Fatalf("consume = %q, %v; want %q, nil", accountID, err, acct.ID)
	}
	if _, err := store.ConsumeMFADisableRequest(ctx, secondHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay error = %v, want ErrNotFound", err)
	}
	if err := store.IssueMFADisableRequest(ctx, []byte("missing-account"), "missing-account", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing account error = %v, want ErrNotFound", err)
	}
}

func TestPgStoreMFADisableRequestLifecycle(t *testing.T) {
	store, ctx := pgStore(t)
	acct, err := store.CreateAccount(ctx, "pg-mfa-disable@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}

	hash := []byte("pg-mfa-disable-hash")
	requestedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	if err := store.IssueMFADisableRequest(ctx, hash, acct.ID, requestedAt); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMFADisableRequest(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != acct.ID || !got.RequestedAt.Equal(requestedAt) {
		t.Fatalf("request = %+v, want account %q at %v", got, acct.ID, requestedAt)
	}
	if _, err := store.GetMFADisableRequest(ctx, []byte("unknown")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown request error = %v, want ErrNotFound", err)
	}
	accountID, err := store.ConsumeMFADisableRequest(ctx, hash)
	if err != nil || accountID != acct.ID {
		t.Fatalf("consume = %q, %v; want %q, nil", accountID, err, acct.ID)
	}
	if _, err := store.GetMFADisableRequest(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("consumed request error = %v, want ErrNotFound", err)
	}
	if _, err := store.ConsumeMFADisableRequest(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay error = %v, want ErrNotFound", err)
	}
}
