package state_test

// adr: 20261104211412 — the one-Free-organization cap holds when ownership is
// transferred and after a Free organization is deleted this month.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type freeOrgCapStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateSharedOrg(ctx context.Context, ownerID, slug, name string) (state.Org, error)
	AddOrgMember(ctx context.Context, orgID, accountID string, role state.OrgRole, invitedBy *string) error
	TransferOrgOwnership(ctx context.Context, orgID, fromAccountID, toAccountID string) error
	SoftDeleteOrg(ctx context.Context, id string) error
}

func checkFreeOrgCapHoldsOnTransferAndDeletion(t *testing.T, s freeOrgCapStore) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, "free-org-a@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateAccount(ctx, "free-org-b@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	handOff := func(slug string) error {
		org, err := s.CreateSharedOrg(ctx, a.ID, slug, slug)
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		if err := s.AddOrgMember(ctx, org.ID, b.ID, state.OrgRoleDeveloper, &a.ID); err != nil {
			t.Fatalf("add member to %s: %v", slug, err)
		}
		return s.TransferOrgOwnership(ctx, org.ID, a.ID, b.ID)
	}
	if err := handOff("free-one"); err != nil {
		t.Fatalf("first transfer: %v, want B to take its one Free organization", err)
	}
	var limit *state.OrgFreeLimitError
	if err := handOff("free-two"); !errors.As(err, &limit) {
		t.Fatalf("second transfer = %v, want OrgFreeLimitError: B already owns a Free organization", err)
	}

	// A owns free-two (the refused transfer left it with A). Deleting it this
	// month does not free A's slot for a fresh Free allowance.
	orgs := freeOrgIDOwnedBy(t, s, a.ID)
	if err := s.SoftDeleteOrg(ctx, orgs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSharedOrg(ctx, a.ID, "free-three", "free-three"); !errors.As(err, &limit) {
		t.Fatalf("create after deleting a Free org this month = %v, want OrgFreeLimitError", err)
	}
}

func freeOrgIDOwnedBy(t *testing.T, s freeOrgCapStore, accountID string) string {
	t.Helper()
	lister, ok := s.(interface {
		ListOrgsForAccount(ctx context.Context, accountID string) ([]state.Org, error)
	})
	if !ok {
		t.Fatal("store cannot list organizations")
	}
	orgs, err := lister.ListOrgsForAccount(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, org := range orgs {
		if !org.Personal && org.Slug == "free-two" {
			return org.ID
		}
	}
	t.Fatal("free-two not found")
	return ""
}

func TestFreeOrgCapHoldsOnTransferAndDeletion_Mem(t *testing.T) {
	checkFreeOrgCapHoldsOnTransferAndDeletion(t, state.NewMemStore())
}

func TestPg_FreeOrgCapHoldsOnTransferAndDeletion(t *testing.T) {
	s, _ := pgStore(t)
	checkFreeOrgCapHoldsOnTransferAndDeletion(t, s)
}
