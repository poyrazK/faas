package conformance

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testRemovedMemberCanRejoin pins that removal keeps the membership row
// with removed_at, that OrgMemberByAccount still returns it (callers must
// check RemovedAt), and that a removed member who accepts a new
// invitation is active again. The (org_id, account_id) primary key made
// the accept collide with the removed row, so a removed member could
// never rejoin: the accept answered "already a member".
func testRemovedMemberCanRejoin(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	suffix := uuid.NewString()[:8]
	org, err := s.CreateOrg(ctx, state.Org{Slug: "rejoin-" + suffix, Name: "Rejoin", Plan: api.PlanScale})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	owner := fx.Account.ID
	if err := s.AddOrgMember(ctx, org.ID, owner, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember(owner): %v", err)
	}
	member, err := s.CreateAccount(ctx, "rejoin-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	invite := func(role state.OrgRole) []byte {
		t.Helper()
		sum := sha256.Sum256([]byte(uuid.NewString()))
		if _, err := s.CreateOrgInvitation(ctx, state.OrgInvitation{
			OrgID: org.ID, Email: member.Email, Role: role, TokenHash: sum[:],
			InvitedByAccountID: &owner, ExpiresAt: time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("CreateOrgInvitation: %v", err)
		}
		return sum[:]
	}
	if _, _, err := s.ConsumeOrgInvitation(ctx, invite(state.OrgRoleDeveloper), member); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if err := s.RemoveOrgMember(ctx, org.ID, member.ID); err != nil {
		t.Fatalf("RemoveOrgMember: %v", err)
	}
	removed, err := s.OrgMemberByAccount(ctx, org.ID, member.ID)
	if err != nil || removed.RemovedAt == nil {
		t.Fatalf("OrgMemberByAccount after removal = %+v, %v; want the row with RemovedAt set", removed, err)
	}
	mem, _, err := s.ConsumeOrgInvitation(ctx, invite(state.OrgRoleAdmin), member)
	if err != nil {
		t.Fatalf("re-invited removed member accept: %v (want the membership reactivated)", err)
	}
	if mem.Role != state.OrgRoleAdmin || mem.RemovedAt != nil {
		t.Fatalf("reactivated membership = %+v, want active admin", mem)
	}
	again, err := s.OrgMemberByAccount(ctx, org.ID, member.ID)
	if err != nil || again.RemovedAt != nil || again.Role != state.OrgRoleAdmin {
		t.Fatalf("OrgMemberByAccount after rejoin = %+v, %v", again, err)
	}
	if _, _, err := s.ConsumeOrgInvitation(ctx, invite(state.OrgRoleViewer), member); !errors.Is(err, state.ErrOrgAlreadyMember) {
		t.Fatalf("accept by an active member: err = %v, want ErrOrgAlreadyMember", err)
	}
}
