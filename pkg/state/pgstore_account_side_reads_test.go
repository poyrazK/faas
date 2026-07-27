package state_test

// PgStore coverage gap tests for account side-reads and provider-ID joins.
//
// This file covers Store methods that had no PgStore test before slice 6:
//
//   Lookup:    AccountByKeyHash (via the api_keys JOIN shape),
//              AccountByProviderCustomerID, AccountByPaddleCustomerID.
//   Mutate:    UpdateAccountProviderCustomerID,
//              UpdateAccountStripeSubscriptionItem,
//              UpdateAccountPaddleCustomerID.
//   Walk:      ListAllAccounts.
//
// All assertions go through the Store API; no raw SQL.
//
// Helpers reused: pgStore(t), createAccount(t,s,ctx,email) → acctID,
// pgTestEmail(t).

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_AccountByKeyHash_ResolvesViaAPIKeyJoin pins the index-backed
// JOIN shape: a freshly-created api_keys row joins to accounts and the
// Account projection is filled. AuthenticateKey is the canonical caller
// (cmd/apid server.go s.auth) — covering AccountByKeyHash directly pins
// the join semantics independent of APIKeyByHash's separate read.
func TestPg_AccountByKeyHash_ResolvesViaAPIKeyJoin(t *testing.T) {
	s, ctx := pgStore(t)
	email := pgTestEmail(t)
	acctID := createAccount(t, s, ctx, email)
	hash := []byte("keyhash-32-bytes-X1234567890abcdef")
	if _, err := s.CreateAPIKey(ctx, acctID, hash, "test-label", []string{"deploy:write"}); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	got, err := s.AccountByKeyHash(ctx, hash)
	if err != nil {
		t.Fatalf("AccountByKeyHash: %v", err)
	}
	if got.ID != acctID {
		t.Errorf("ID = %q, want %q", got.ID, acctID)
	}
	// Pin the email value exactly — a future refactor of pgTestEmail
	// (or a separate accounts row inserted by a sibling test in the same
	// schema) would silently leak past an `!= ""` check.
	if got.Email != email {
		t.Errorf("Email = %q, want %q", got.Email, email)
	}
}

// TestPg_AccountByKeyHash_UnknownHashReturnsErrNotFound pins the miss
// path via mapErr (pgx.ErrNoRows → ErrNotFound).
func TestPg_AccountByKeyHash_UnknownHashReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.AccountByKeyHash(ctx, []byte("never-stored-hash-X1234567890abc"))
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AccountByKeyKeyHash(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_UpdateAccountProviderCustomerID_PersistsAndReadsBack pins the
// Stripe webhook side: customer.subscription.created writes the
// provider_customer_id; AccountByProviderCustomerID reads it back. The
// round-trip is the OOB path for the post-2024-04 customer ID swap
// (issue #52).
func TestPg_UpdateAccountProviderCustomerID_PersistsAndReadsBack(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if err := s.UpdateAccountProviderCustomerID(ctx, acctID, "cus_TEST"); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}
	got, err := s.AccountByProviderCustomerID(ctx, "cus_TEST")
	if err != nil {
		t.Fatalf("AccountByProviderCustomerID: %v", err)
	}
	if got.ID != acctID {
		t.Errorf("ID = %q, want %q", got.ID, acctID)
	}
}

// TestPg_UpdateAccountProviderCustomerID_UnknownAccountReturnsErrNotFound
// pins the RowsAffected==0 → ErrNotFound branch.
func TestPg_UpdateAccountProviderCustomerID_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.UpdateAccountProviderCustomerID(ctx, "00000000-0000-0000-0000-000000000000", "cus_X")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("UpdateAccountProviderCustomerID(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_UpdateAccountStripeSubscriptionItem_PersistsAndReadsBack pins the
// si_… storage path. meterd's hourly push reads this column to know where
// to POST the UsageRecord (issue #52).
func TestPg_UpdateAccountStripeSubscriptionItem_PersistsAndReadsBack(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if err := s.UpdateAccountStripeSubscriptionItem(ctx, acctID, "si_TEST123"); err != nil {
		t.Fatalf("UpdateAccountStripeSubscriptionItem: %v", err)
	}
	// Read-back goes through AccountByID (the only Store method that
	// projects stripe_subscription_item into the Account struct today
	// — AccountByProviderCustomerID and ListAllAccounts also project
	// it; verify via AccountByID to exercise the column-read in its
	// most-used form).
	got, err := s.AccountByID(ctx, acctID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.StripeSubscriptionItem != "si_TEST123" {
		t.Errorf("StripeSubscriptionItem = %q, want si_TEST123", got.StripeSubscriptionItem)
	}
}

// TestPg_UpdateAccountStripeSubscriptionItem_UnknownAccountReturnsErrNotFound
// pins the RowsAffected==0 → ErrNotFound branch.
func TestPg_UpdateAccountStripeSubscriptionItem_UnknownAccountReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.UpdateAccountStripeSubscriptionItem(ctx, "00000000-0000-0000-0000-000000000000", "si_X")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("UpdateAccountStripeSubscriptionItem(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_UpdateAccountPaddleCustomerID_Persists pins the Paddle side of
// provider_customer_id (the column is reused per ADR-025). The mutation
// is identical to the Stripe path; the dedicated method keeps Paddle call
// sites self-documenting.
func TestPg_UpdateAccountPaddleCustomerID_Persists(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	if err := s.UpdateAccountPaddleCustomerID(ctx, acctID, "ctm_TEST"); err != nil {
		t.Fatalf("UpdateAccountPaddleCustomerID: %v", err)
	}
	got, err := s.AccountByPaddleCustomerID(ctx, "ctm_TEST")
	if err != nil {
		t.Fatalf("AccountByPaddleCustomerID: %v", err)
	}
	if got.ID != acctID {
		t.Errorf("ID = %q, want %q", got.ID, acctID)
	}
}

// TestPg_AccountByPaddleCustomerID_UnknownReturnsErrNotFound pins the
// reverse-lookup miss branch (delegated to AccountByProviderCustomerID).
func TestPg_AccountByPaddleCustomerID_UnknownReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.AccountByPaddleCustomerID(ctx, "ctm_NEVER")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AccountByPaddleCustomerID(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPg_ListAllAccounts_ReturnsAll pins the meterd walk: every account
// the Store method sees comes back. The chokepoint caller (meterd) filters
// in memory — but the Store itself must surface every row, not just the
// active subset. This test pins the unfiltered surface; deactivation and
// deletion status do not narrow the result set (covered separately at
// pgstore_account_deletion_test.go).
func TestPg_ListAllAccounts_ReturnsAll(t *testing.T) {
	s, ctx := pgStore(t)
	acctA := createAccount(t, s, ctx, pgTestEmail(t)+"-a")
	acctB := createAccount(t, s, ctx, pgTestEmail(t)+"-b")
	all, err := s.ListAllAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAllAccounts: %v", err)
	}
	seen := map[string]bool{}
	for _, a := range all {
		seen[a.ID] = true
	}
	for _, id := range []string{acctA, acctB} {
		if !seen[id] {
			t.Errorf("ListAllAccounts missing acct %q", id)
		}
	}
}
