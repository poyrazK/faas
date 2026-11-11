package apidserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// changeEmailForTest applies a confirmed email change to acct.
func changeEmailForTest(t *testing.T, ctx context.Context, store *state.MemStore, acct state.Account, newEmail string) state.EmailChange {
	t.Helper()
	token := api.HashToken([]byte("change-" + acct.ID))
	if err := store.IssueEmailChange(ctx, token, acct.ID, newEmail, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	change, err := store.ConsumeEmailChange(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	return change
}

// A confirmed email change moves the billing email with it: the account's
// own billing customer, and each organization that bills to the old address.
// A provider refusal leaves that billing email as it was and is reported.
func TestEmailChangeMovesBillingEmail(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "moves-co", "Moves Co", api.PlanPro)
	member, _ := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	fake := &identityPolar{customers: map[string]identityCustomer{
		"cus-org":   {email: member.Email, external: org.ID},
		"cus-me":    {email: e.Acct().Email, external: e.Acct().ID},
		"cus-taken": {email: "taken@example.com", external: "someone-else"},
	}}
	identityProvider(t, e, fake)
	if rec := e.Do(t, http.MethodPut, "/v1/orgs/"+org.Slug+"/billing/email", api.OrgBillingEmailRequest{BillingEmail: member.Email}, nil); rec.Code != http.StatusOK {
		t.Fatalf("set org billing email: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []state.BillingIdentity{
		{AccountID: org.ID, Provider: "polar", CustomerID: "cus-org", SubscriptionID: "sub-org"},
		{AccountID: e.Acct().ID, Provider: "polar", CustomerID: "cus-me", SubscriptionID: "sub-me"},
	} {
		if err := store.UpsertBillingIdentity(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	srv := *e.S()

	// The member who is the organization's billing contact moves.
	change := changeEmailForTest(t, ctx, store, member, "member-new@example.com")
	if failed := srv.MoveBillingEmails(ctx, change); len(failed) != 0 {
		t.Fatalf("member move failed for %v", failed)
	}
	subject, err := state.BillingResourceAccount(ctx, store, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if subject.Email != "member-new@example.com" || fake.email("cus-org") != "member-new@example.com" {
		t.Fatalf("org billing email %q, customer %q; want member-new@example.com", subject.Email, fake.email("cus-org"))
	}

	// The owner bills personally; the new address is another customer's.
	change = changeEmailForTest(t, ctx, store, *e.Acct(), "taken@example.com")
	failed := srv.MoveBillingEmails(ctx, change)
	if len(failed) != 1 || failed[0] != "your personal account" {
		t.Fatalf("failed = %v, want the personal account", failed)
	}
	if fake.email("cus-me") == "taken@example.com" {
		t.Fatal("a refused move changed the customer")
	}
	if fake.email("cus-org") != "member-new@example.com" {
		t.Fatal("the owner's change moved an organization billing to another member")
	}
}
