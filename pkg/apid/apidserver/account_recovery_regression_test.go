package apidserver_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func auditRejoin(t *testing.T, st state.Store, owner state.Account) {
	t.Helper()
	ctx := t.Context()
	org, err := st.CreateOrg(ctx, state.Org{Slug: "rejoin-probe", Name: "Rejoin", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateAccount(ctx, "rejoin-probe@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveOrgMember(ctx, org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := st.OrgMemberByAccount(ctx, org.ID, member.ID)
	if err != nil || removed.RemovedAt == nil {
		t.Fatalf("removed membership: %+v %v", removed, err)
	}
	activity := func(kind string) state.OrgActivity {
		actor := uuid.MustParse(owner.ID)
		return state.OrgActivity{
			OrgID: uuid.MustParse(org.ID), Kind: kind, ActorType: state.OrgActivityActorUser,
			ActorAccountID: &actor, ActorLabel: owner.Email, ResourceType: "invitation", ResourceLabel: "invitation",
			SourceType: kind, SourceID: uuid.NewString(), Data: []byte(`{}`),
		}
	}
	mutations := st.(state.OrgActivityAccessMutationStore)
	tokenHash := api.HashToken([]byte("account-audit-local-invitation"))
	if _, _, err := mutations.CreateOrgInvitationWithActivity(ctx, state.OrgInvitation{
		OrgID: org.ID, Email: member.Email, Role: state.OrgRoleDeveloper, TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(time.Hour), InvitedByAccountID: &owner.ID,
	}, activity("org.invitation.created")); err != nil {
		t.Fatal(err)
	}
	added := activity("org.member.added")
	added.ResourceType = "member"
	joined, _, _, _, err := mutations.ConsumeOrgInvitationWithActivity(ctx, tokenHash, member, activity("org.invitation.accepted"), added)
	if err != nil || joined.RemovedAt != nil {
		t.Fatalf("removed member accepting a new invitation through production activity path: %v; want active rejoined membership", err)
	}
}

func TestAccountAuditRemovedMemberCanRejoinMemory(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	auditRejoin(t, *e.Store(), *e.Acct())
}

func TestAccountAuditSuspendedAccountCanStartMFARecovery(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	_, _, _ = e.GenerateEnrolledAccount(t)
	if err := (*e.Store()).UpdateAccountStatus(t.Context(), (*e.Acct()).ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	cookie := e.MfaIssueWithPending(t, true)
	req := httptest.NewRequest("GET", "/v1/auth/csrf?action=mfa_recover", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("suspended account cannot obtain required recovery CSRF token: %d %s", rec.Code, rec.Body.String())
	}
}
