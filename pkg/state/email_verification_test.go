package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEmailVerificationTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email:                    "verify@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Account.EmailVerified() {
		t.Fatal("password-signup account started verified")
	}

	hash := []byte("live-verification-token-hash")
	if err := store.IssueEmailVerificationToken(ctx, hash, res.Account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	accountID, err := store.ConsumeEmailVerificationToken(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if accountID != res.Account.ID {
		t.Fatalf("account id = %q, want %q", accountID, res.Account.ID)
	}
	verified, err := store.AccountByID(ctx, res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.EmailVerified() {
		t.Fatal("token consume did not verify account")
	}
	if _, err := store.ConsumeEmailVerificationToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay error = %v, want ErrNotFound", err)
	}
}

func TestEmailVerificationTokenExpiryDoesNotVerify(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email:                    "expired@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := []byte("expired-verification-token-hash")
	if err := store.IssueEmailVerificationToken(ctx, hash, res.Account.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeEmailVerificationToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired consume error = %v, want ErrNotFound", err)
	}
	acct, err := store.AccountByID(ctx, res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if acct.EmailVerified() {
		t.Fatal("expired token verified account")
	}
}

func TestTrustedAccountCreationDefaultsVerified(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "oauth@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.EmailVerified() {
		t.Fatal("trusted/OAuth-compatible account creation must default verified")
	}
}
