package state_test

// adr: 20261021081828

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSubmissionCompanyNamespaceMemory(t *testing.T) {
	testProjectSubmissionCompanyNamespace(t, state.NewMemStore())
}

// An adopted organization keeps projects in its own resource namespace, so
// release receipts must be recordable under the organization's ID.
func testProjectSubmissionCompanyNamespace(t *testing.T, store state.Store) {
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "company-receipt@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "company-receipts", Name: "Company Receipts", Plan: api.PlanScale})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_company_receipts"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_company_receipts"); err != nil {
		t.Fatal(err)
	}
	if adopted, err := store.OrgByID(ctx, org.ID); err != nil || !adopted.ResourceOwnershipEnabled {
		t.Fatalf("organization not adopted: %+v %v", adopted, err)
	}
	ledger := store.(state.ProjectSubmissionStore)
	id := strings.Repeat("d", 32)
	frame := api.ProjectSubmissionReceipt{AttemptID: id, ProjectSlug: "company-shop", Status: "processing"}
	reserved, err := ledger.ReserveProjectSubmission(ctx, org.ID, id, strings.Repeat("e", 64), frame)
	if err != nil || !reserved {
		t.Fatalf("company reservation=%v %v", reserved, err)
	}
	receipt, err := ledger.ProjectSubmission(ctx, org.ID, id)
	if err != nil || receipt.ProjectSlug != frame.ProjectSlug {
		t.Fatalf("company receipt=%+v %v", receipt, err)
	}
	// Unknown namespaces are still refused.
	if _, err := ledger.ReserveProjectSubmission(ctx, "00000000-0000-4000-8000-000000000000", id, strings.Repeat("e", 64), frame); err == nil {
		t.Fatal("reserved a receipt for an unknown namespace")
	}
}
