package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func orgPolicyFixture(t *testing.T, store state.Store) (state.Org, state.Account, string, state.Account, string) {
	t.Helper()
	owner, ownerKey := flowAccountKey(t, store, "policy-owner@example.com", true)
	admin, adminKey := flowAccountKey(t, store, "policy-admin@example.com", true)
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "policy-team", Name: "Policy Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	return org, owner, ownerKey, admin, adminKey
}

func waitForMail(t *testing.T, mailer *apidserver.RecordingMailer, to, contains string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, body := range flowMailsTo(mailer, to) {
			if strings.Contains(body, contains) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no mail to %s containing %q; got %q", to, contains, flowMailsTo(mailer, to))
}

// Transferring ownership and deleting an organization need a signed-in
// browser; an API key, however it leaked, cannot (ADR 20261113180000).
func TestOrgOwnershipAndDeletionNeedBrowserSession(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, _, ownerKey, admin, _ := orgPolicyFixture(t, store)
	rec := flowBearer(h, http.MethodPost, "/v1/orgs/policy-team/transfer_ownership", ownerKey, `{"new_owner_account_id":"`+admin.ID+`"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeBrowserSessionRequired) {
		t.Fatalf("transfer with a key = %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeBrowserSessionRequired)
	}
	rec = flowBearer(h, http.MethodDelete, "/v1/orgs/policy-team", ownerKey, "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeBrowserSessionRequired) {
		t.Fatalf("delete with a key = %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeBrowserSessionRequired)
	}
}

// Only an owner can invite an admin, and an admin's new org key is mailed to
// the owners.
func TestAdminInvitesAndOrgKeysAreOwnerVisible(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	_, owner, _, _, adminKey := orgPolicyFixture(t, store)
	if rec := flowBearer(h, http.MethodPost, "/v1/orgs/policy-team/members", adminKey, `{"email":"second@example.com","role":"admin"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("admin inviting an admin = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodPost, "/v1/orgs/policy-team/members", adminKey, `{"email":"dev@example.com","role":"developer"}`); rec.Code != http.StatusCreated {
		t.Fatalf("admin inviting a developer = %d %s, want 201", rec.Code, rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodPost, "/v1/orgs/policy-team/keys", adminKey, `{"label":"ci"}`); rec.Code != http.StatusCreated {
		t.Fatalf("admin creating an org key = %d %s, want 201", rec.Code, rec.Body.String())
	}
	waitForMail(t, mailer, owner.Email, "API key")
}

// An organization that requires two-factor authentication refuses members
// without it with org_mfa_required, and only a member with two-factor can
// turn the requirement on.
func TestOrgRequiresMFA(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	org, owner, ownerKey, _, adminKey := orgPolicyFixture(t, store)
	if rec := flowBearer(h, http.MethodPut, "/v1/orgs/policy-team/security-policy", ownerKey, `{"require_mfa":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("owner without MFA requiring it = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if err := store.SetMFASecret(t.Context(), owner.ID, []byte("sealed-secret"), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMFAEnrolled(t.Context(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if rec := flowBearer(h, http.MethodPut, "/v1/orgs/policy-team/security-policy", ownerKey, `{"require_mfa":true}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"require_mfa":true`) {
		t.Fatalf("owner with MFA requiring it = %d %s, want 200", rec.Code, rec.Body.String())
	}
	rec := flowBearer(h, http.MethodGet, "/v1/orgs/policy-team/members", adminKey, "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeOrgMFARequired) {
		t.Fatalf("member without MFA = %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeOrgMFARequired)
	}
	if rec := flowBearer(h, http.MethodGet, "/v1/orgs/policy-team/members", ownerKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("owner with MFA = %d %s, want 200", rec.Code, rec.Body.String())
	}
	_ = org
}
