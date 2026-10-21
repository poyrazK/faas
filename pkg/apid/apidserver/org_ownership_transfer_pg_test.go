//go:build !no_pg

// adr: 202610171902

package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An ownership transfer demotes its caller, invalidating the request's own
// offboarding fence. The committed transfer must still answer success: the
// post-commit audit write and response read run outside the fence (nightly
// e2e TestE2E_OrgLifecycle_HappyPath answered a committed transfer with 403).
func TestPGOwnershipTransferAnswersSuccess(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	ctx := t.Context()
	store, acct := *e.Store(), *e.Acct()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "transfer-fence", Name: "Transfer Fence", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	next, err := store.CreateAccount(ctx, "transfer-fence-next@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, next.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/transfer_ownership",
		api.TransferOwnershipRequest{NewOwnerAccountID: next.ID}, map[string]string{"X-Active-Org": org.Slug})
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer ownership: %d %s", rec.Code, rec.Body)
	}
	if m, err := store.OrgMemberByAccount(ctx, org.ID, next.ID); err != nil || m.Role != state.OrgRoleOwner {
		t.Fatalf("new owner = %+v, %v", m, err)
	}
	if m, err := store.OrgMemberByAccount(ctx, org.ID, acct.ID); err != nil || m.Role == state.OrgRoleOwner {
		t.Fatalf("previous owner = %+v, %v", m, err)
	}
}
