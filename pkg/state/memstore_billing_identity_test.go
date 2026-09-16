package state

import (
	"context"
	"errors"
	"testing"
)

func TestMemStoreUpdateAccountBillingInfo(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "billing-identity@example.com", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	updated, err := store.UpdateAccountBillingInfo(ctx, acct.ID, "Acme GmbH", "Hauptstrasse 1, Berlin", "DE123456789")
	if err != nil {
		t.Fatalf("UpdateAccountBillingInfo: %v", err)
	}
	if updated.BusinessName != "Acme GmbH" || updated.BillingAddress != "Hauptstrasse 1, Berlin" || updated.TaxID != "DE123456789" {
		t.Fatalf("updated billing identity = %+v", updated)
	}
	read, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if read.BusinessName != updated.BusinessName || read.BillingAddress != updated.BillingAddress || read.TaxID != updated.TaxID {
		t.Fatalf("persisted billing identity = %+v, want %+v", read, updated)
	}
	if _, err := store.UpdateAccountBillingInfo(ctx, "missing", "x", "y", "z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account error = %v, want ErrNotFound", err)
	}
	cleared, err := store.UpdateAccountBillingInfo(ctx, acct.ID, "", "", "")
	if err != nil {
		t.Fatalf("clear billing identity: %v", err)
	}
	if cleared.BusinessName != "" || cleared.BillingAddress != "" || cleared.TaxID != "" {
		t.Fatalf("cleared billing identity = %+v", cleared)
	}
}
