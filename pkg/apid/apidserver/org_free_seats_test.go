package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestInviteOnFreeOrgNamesTheUpgrade pins ADR-20261127200000: a Free
// organization has no member seats, so an invitation is refused up front with
// the upgrade command instead of minting a token no one can accept.
func TestInviteOnFreeOrgNamesTheUpgrade(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "free-seats", "Free Seats", api.PlanFree)
	rec := e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/members", api.InviteMemberRequest{
		Email: "ada@example.com", Role: string(state.OrgRoleDeveloper),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("invite on Free: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeOrgInvitationCapExceeded || !strings.Contains(problem.Fix, "gregale --org free-seats billing plan") {
		t.Fatalf("problem = %+v, want invitation-cap code with the upgrade fix", problem)
	}
	pending, err := e.store.CountPendingOrgInvitations(context.Background(), org.ID)
	if err != nil || pending != 0 {
		t.Fatalf("pending invitations = %d, %v; want none minted", pending, err)
	}

	// The same organization on Hobby invites.
	paid := seedSharedOrgWithOwner(t, e, "hobby-seats", "Hobby Seats", api.PlanHobby)
	rec = e.do(t, http.MethodPost, "/v1/orgs/"+paid.Slug+"/members", api.InviteMemberRequest{
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
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOutsideOwner(t, e, "free-accept", "Free Accept", api.PlanFree)
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)
	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("accept on Free: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeOrgMemberCapExceeded || !strings.Contains(problem.Detail, "upgrade") {
		t.Fatalf("problem = %+v, want member-cap code asking for an upgrade", problem)
	}
}
