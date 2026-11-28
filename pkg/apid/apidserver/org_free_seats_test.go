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

// TestInviteOnFreeOrgNamesTheUpgrade pins ADR-20261127200000: a Free
// organization has no member seats, so an invitation is refused up front with
// the upgrade command instead of minting a token no one can accept.
func TestInviteOnFreeOrgNamesTheUpgrade(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "free-seats", "Free Seats", api.PlanFree)
	rec := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/members", api.InviteMemberRequest{
		Email: "ada@example.com", Role: string(state.OrgRoleDeveloper),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("invite on Free: status=%d body=%s", rec.Code, rec.Body.String())
	}
	problem := decodeProblem(t, rec.Body.Bytes())
	if problem.Code != api.CodeOrgInvitationCapExceeded || !strings.Contains(problem.Fix, "gregale --org free-seats billing plan") {
		t.Fatalf("problem = %+v, want invitation-cap code with the upgrade fix", problem)
	}
	pending, err := store.CountPendingOrgInvitations(context.Background(), org.ID)
	if err != nil || pending != 0 {
		t.Fatalf("pending invitations = %d, %v; want none minted", pending, err)
	}

	// The same organization on Hobby invites.
	paid := apidserver.SeedSharedOrgWithOwner(t, e, "hobby-seats", "Hobby Seats", api.PlanHobby)
	rec = e.Do(t, http.MethodPost, "/v1/orgs/"+paid.Slug+"/members", api.InviteMemberRequest{
		Email: "ada@example.com", Role: string(state.OrgRoleDeveloper),
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite on Hobby: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAcceptOnFreeOrgAsksForAnUpgrade covers invitations minted before the
// invite-time refusal (or before a downgrade): accepting one on a plan with no
// seats says the owner must upgrade instead of "0 of 0 members".
func TestAcceptOnFreeOrgAsksForAnUpgrade(t *testing.T) {
	e, cookie := apidserver.SetupWithSessionFreshStepUpForTest(t)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOutsideOwner(t, e, "free-accept", "Free Accept", api.PlanFree)
	wireToken, _ := apidserver.SeedInvitationForPrincipal(t, store, &org, e.Acct().ID, e.Acct().Email, state.OrgRoleDeveloper)
	rec := e.DoWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("accept on Free: status=%d body=%s", rec.Code, rec.Body.String())
	}
	problem := decodeProblem(t, rec.Body.Bytes())
	if problem.Code != api.CodeOrgMemberCapExceeded || !strings.Contains(problem.Detail, "upgrade") {
		t.Fatalf("problem = %+v, want member-cap code asking for an upgrade", problem)
	}
}

// TestAcceptOnFullOrgDisclosesNoHeadcount: an accept on a full paid
// organization (here over its cap after a downgrade) says no seat is free and
// does not tell the invitee, not yet a member, the limit or headcount.
func TestAcceptOnFullOrgDisclosesNoHeadcount(t *testing.T) {
	e, cookie := apidserver.SetupWithSessionFreshStepUpForTest(t)
	store := *e.Store()
	ctx := context.Background()
	org := apidserver.SeedSharedOrgWithOutsideOwner(t, e, "full-hobby", "Full Hobby", api.PlanHobby)
	limit := api.PlanHobby.OrgMembersMax()
	for i := 0; i < limit+1; i++ { // the outside owner plus limit+1: over a downgraded cap
		member, err := store.CreateAccount(ctx, "full-"+string(rune('a'+i))+"@hobby.test", api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount %d: %v", i, err)
		}
		if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatalf("AddOrgMember %d: %v", i, err)
		}
	}
	wireToken, _ := apidserver.SeedInvitationForPrincipal(t, store, &org, e.Acct().ID, e.Acct().Email, state.OrgRoleDeveloper)
	rec := e.DoWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("accept on full org: status=%d body=%s", rec.Code, rec.Body.String())
	}
	problem := decodeProblem(t, rec.Body.Bytes())
	if problem.Code != api.CodeOrgMemberCapExceeded || !strings.Contains(problem.Detail, "seat") ||
		strings.ContainsAny(problem.Detail, "0123456789") || problem.Limit != nil || problem.Observed != nil {
		t.Fatalf("problem = %+v, want a seat refusal with no limit or headcount", problem)
	}
}
