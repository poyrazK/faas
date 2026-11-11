// adr: 20261111130000
package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// noticeOrg is a shared organization owned by owner, with admin as a member
// and an organization key for the owner.
func noticeOrg(t *testing.T, store state.Store, owner, admin state.Account) (state.Org, string) {
	t.Helper()
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "notice-co", Name: "Notice Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]state.OrgRole{owner.ID: state.OrgRoleOwner, admin.ID: state.OrgRoleAdmin} {
		if err := store.AddOrgMember(t.Context(), org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, owner.ID, hash, "owner-org", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	return org, plain
}

func mailed(mailer *apidserver.RecordingMailer, to, fragment string) bool {
	for _, body := range flowMailsTo(mailer, to) {
		if strings.Contains(body, fragment) {
			return true
		}
	}
	return false
}

// Removing a member and changing a role mail the owners and the member
// concerned; they were only in the activity feed.
func TestOrgAccessChangesAreMailed(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	owner, _ := flowAccountKey(t, store, "notice-owner@example.com", true)
	admin, _ := flowAccountKey(t, store, "notice-admin@example.com", true)
	org, ownerKey := noticeOrg(t, store, owner, admin)

	if rec := flowBearer(h, http.MethodPatch, "/v1/orgs/"+org.Slug+"/members/"+admin.ID, ownerKey, `{"role":"developer"}`); rec.Code != http.StatusOK {
		t.Fatalf("role change = %d %s", rec.Code, rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+admin.ID, ownerKey, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("removal = %d %s", rec.Code, rec.Body.String())
	}
	srv.WaitPasswordResetMail()
	for _, to := range []string{owner.Email, admin.Email} {
		if !mailed(mailer, to, "to developer") {
			t.Errorf("%s got no role-change notice", to)
		}
		if !mailed(mailer, to, "removed n***@example.com") {
			t.Errorf("%s got no removal notice", to)
		}
	}
}

// A new deploy token mails a credential notice, and a password reset's key
// review lists the deploy tokens the account holds.
func TestDeployTokensAreNoticedAndReviewed(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	acct, key := flowAccountKey(t, store, "token-holder@example.com", true)
	if _, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "notice-api", Status: state.AppActive, RAMMB: 256}); err != nil {
		t.Fatal(err)
	}
	if rec := flowBearer(srv.Handler(), http.MethodPost, "/v1/apps/notice-api/deploy-tokens", key, `{"label":"ci"}`); rec.Code != http.StatusCreated {
		t.Fatalf("mint deploy token = %d %s", rec.Code, rec.Body.String())
	}
	srv.WaitPasswordResetMail()
	if !mailed(mailer, acct.Email, "A deploy token") {
		t.Errorf("no credential notice for the new deploy token: %v", flowMailsTo(mailer, acct.Email))
	}
	review := srv.KeysLeftAfterReset(context.Background(), acct)
	found := false
	for _, k := range review {
		found = found || k.Label == "deploy token ci"
	}
	if !found {
		t.Errorf("reset review = %+v, want the deploy token listed", review)
	}
}
