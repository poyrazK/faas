package product

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func reviewedTeamFixture() customerTeam {
	actor, member, org, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	return customerTeam{
		realm:    customerRealm{account: state.Account{ID: uuid.NewString()}, app: state.App{ID: uuid.NewString()}, authApp: state.App{ID: uuid.NewString()}, catalog: api.ProductCatalog{Revision: strings.Repeat("a", 64), Spec: api.ProductCatalogSpec{Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"manager": {api.ProductPermissionManageTeam}, "member": {api.ProductPermissionViewWork}, "operator": {api.ProductPermissionRunWork}, "owner": {api.ProductPermissionManageTeam}}}, Team: &api.ProductTeamPolicy{Delegations: map[string][]string{"manager": {"member", "operator", "owner"}}}}}},
		identity: authmw.ProductCustomerIdentity{OrganizationID: org, TenantID: tenant, Access: api.ProductCustomerAccess{Role: "manager", Permissions: api.ProductPermissions{ManageTeam: true}}},
		customer: api.ProductCustomer{TenantID: tenant, AuthOrganizationID: org, Plan: "starter"},
		live:     teamAuthState{Protocol: 1, ActorID: actor, ActorRole: "manager", Snapshot: strings.Repeat("b", 64), Members: []api.ProductTeamMember{{UserID: actor, Role: "manager", Email: "manager@example.test", CreatedAt: time.Now()}, {UserID: member, Role: "member", Email: "member@example.test", CreatedAt: time.Now()}}, Invitations: []api.ProductTeamInvitation{}, Roles: []teamAuthRole{{Name: "manager", Permissions: []string{"org:read", "members:manage"}}, {Name: "member", Permissions: []string{"org:read"}}, {Name: "operator", Permissions: []string{"org:read", "doc:write"}}, {Name: "owner", Permissions: []string{"*"}}}},
	}
}

func TestProductTeamReviewUsesBothRoleBoundariesAndCurrentTerms(t *testing.T) {
	c := reviewedTeamFixture()
	r := api.ProductTeamActionRequest{ID: uuid.NewString(), Action: "set_role", TargetID: c.live.Members[1].UserID, Role: "operator"}
	p, err := productTeamPreview(c, r)
	if err != nil || !p.BeforePermissions.ViewWork || !p.AfterPermissions.RunWork || !reflect.DeepEqual(p.AfterWorkspacePermissions, []string{"org:read", "doc:write"}) || p.AuthSnapshot != c.live.Snapshot {
		t.Fatalf("incomplete before/after review: %+v %v", p, err)
	}
	for _, mutate := range []func(*customerTeam){
		func(c *customerTeam) { c.customer.Suspended = true },
		func(c *customerTeam) { c.live.Members[1].Role = "owner" },
		func(c *customerTeam) { c.realm.catalog.Spec.Team.Delegations["manager"] = []string{"operator"} },
		func(c *customerTeam) { c.realm.catalog.Spec.Team.Delegations["manager"] = []string{"member"} },
	} {
		denied := reviewedTeamFixture()
		r.TargetID = denied.live.Members[1].UserID
		mutate(&denied)
		if _, err := productTeamPreview(denied, r); err == nil {
			t.Fatal("unauthorized current or proposed role accepted")
		}
	}
	r.TargetID = c.live.ActorID
	if _, err := productTeamPreview(c, r); err == nil {
		t.Fatal("self role change accepted")
	}
	r.TargetID = c.live.Members[1].UserID
	c.live.Snapshot = strings.Repeat("c", 64)
	changed, err := productTeamPreview(c, r)
	if err != nil || changed.Hash == p.Hash {
		t.Fatal("review hash omitted the Auth state")
	}
	c.customer.Plan = "changed"
	other, err := productTeamPreview(c, r)
	if err != nil || other.Hash == changed.Hash {
		t.Fatal("review hash omitted current customer terms")
	}
}

func TestProductTeamViewAndInviteValidation(t *testing.T) {
	c := reviewedTeamFixture()
	if err := validateTeamState(c.live, c.identity); err != nil {
		t.Fatal(err)
	}
	v := productTeamView(c)
	if v.Members[0].CanManage || !v.Members[1].CanManage || len(v.AssignableRoles) != 2 {
		t.Fatalf("self controls or unapproved owner role exposed: %+v", v)
	}
	r := api.ProductTeamActionRequest{ID: uuid.NewString(), Action: "invite", Email: " FRIEND@Example.Test ", Role: "member"}
	p, err := productTeamPreview(c, r)
	if err != nil || p.Request.Email != "friend@example.test" || p.Request.ExpiresIn != api.ProductControlTeamInvitationDefaultSeconds {
		t.Fatal("invitation normalization failed", err)
	}
	for _, r := range []api.ProductTeamActionRequest{
		{ID: uuid.NewString(), Action: "invite", Email: "member@example.test", Role: "member"},
		{ID: uuid.NewString(), Action: "invite", Email: "a@b", Role: "member", ExpiresIn: 1},
		{ID: uuid.NewString(), Action: "set_role", TargetID: c.live.Members[1].UserID, Role: "member"},
		{ID: uuid.NewString(), Action: "remove_member", TargetID: uuid.NewString()},
	} {
		if _, err := productTeamPreview(c, r); err == nil {
			t.Fatal("invalid or duplicate action accepted")
		}
	}
	c.live.Members = append(c.live.Members, c.live.Members[0])
	if validateTeamState(c.live, c.identity) == nil {
		t.Fatal("duplicate Auth member accepted")
	}
}

func TestProductTeamReceiptProjectionRemovesContextAndCredentials(t *testing.T) {
	c := reviewedTeamFixture()
	r := api.ProductTeamActionRequest{ID: uuid.NewString(), Action: "set_role", TargetID: c.live.Members[1].UserID, Role: "operator"}
	request, _ := json.Marshal(teamAuthRequest{ProductTeamActionRequest: r, ExpectedSnapshot: c.live.Snapshot, Context: c.actionContext(strings.Repeat("d", 64))})
	result, _ := json.Marshal(map[string]any{"user_id": r.TargetID, "before_role": "member", "after_role": "operator", "token": "do-not-disclose", "unknown": "private"})
	receipt := api.ProductTeamActionReceipt{ID: r.ID, OrganizationID: c.identity.OrganizationID, ActorID: c.live.ActorID, ActorRole: c.live.ActorRole, Action: r.Action, Request: request, Result: result, CreatedAt: time.Now()}
	projected, err := teamReceiptProjection(c, receipt)
	if err != nil || projected.ReviewHash != strings.Repeat("d", 64) || strings.Contains(string(projected.Request), "context") || strings.Contains(string(projected.Result), "private") || strings.Contains(string(projected.Result), "do-not-disclose") {
		t.Fatal("receipt projection leaked private context or credentials", err)
	}
	c.identity.TenantID = uuid.NewString()
	projected, err = teamReceiptProjection(c, receipt)
	if err != nil || projected.ReviewHash != "" {
		t.Fatal("another product scope claimed its review hash")
	}
	c.identity.OrganizationID = uuid.NewString()
	if _, err := teamReceiptProjection(c, receipt); err == nil {
		t.Fatal("foreign organization receipt accepted")
	}
}
