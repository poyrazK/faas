package product

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func productValidTeamRole(role string) bool { return productcontrol.ValidAccessRole(role) }
func productTeamPermission(c api.ProductCatalog, role string) bool {
	return productcontrol.EffectiveAccess(c.Spec.Access, role).ManageTeam
}
func (c customerTeam) canManageRole(role string) bool {
	return !c.customer.Suspended && productcontrol.TeamRoleAllowed(c.realm.catalog.Spec.Team, c.live.ActorRole, role) && (role != "owner" || c.live.ActorRole == "owner")
}
func (c customerTeam) workspacePermissions(role string) []string {
	for _, r := range c.live.Roles {
		if r.Name == role {
			return slices.Clone(r.Permissions)
		}
	}
	return []string{}
}

func productTeamView(c customerTeam) api.ProductTeamView {
	v := api.ProductTeamView{ActorID: c.live.ActorID, ActorRole: c.live.ActorRole, Revision: c.realm.catalog.Revision, Members: []api.ProductTeamMember{}, Invitations: []api.ProductTeamInvitation{}, AssignableRoles: []api.ProductTeamRole{}, Suspended: c.customer.Suspended, InvitationDefaultSeconds: api.ProductControlTeamInvitationDefaultSeconds}
	for _, m := range c.live.Members {
		m.Permissions = productcontrol.EffectiveAccess(c.realm.catalog.Spec.Access, m.Role)
		m.CanManage = m.UserID != c.live.ActorID && c.canManageRole(m.Role)
		v.Members = append(v.Members, m)
	}
	for _, i := range c.live.Invitations {
		i.Permissions = productcontrol.EffectiveAccess(c.realm.catalog.Spec.Access, i.Role)
		i.CanRevoke = c.canManageRole(i.Role)
		v.Invitations = append(v.Invitations, i)
	}
	for _, r := range c.live.Roles {
		if c.canManageRole(r.Name) {
			v.AssignableRoles = append(v.AssignableRoles, api.ProductTeamRole{Role: r.Name, Permissions: productcontrol.EffectiveAccess(c.realm.catalog.Spec.Access, r.Name), AuthPermissions: slices.Clone(r.Permissions)})
		}
	}
	sort.Slice(v.AssignableRoles, func(i, j int) bool { return v.AssignableRoles[i].Role < v.AssignableRoles[j].Role })
	return v
}

var teamEmail = regexp.MustCompile(`^[^\x00-\x20\x7f<>@]+@[^\x00-\x20\x7f<>@]+$`)

func normalizeTeamRequest(r api.ProductTeamActionRequest) (api.ProductTeamActionRequest, error) {
	if !teamUUID(r.ID) {
		return r, state.ErrInvalidArgument
	}
	switch r.Action {
	case "invite":
		r.Email = strings.ToLower(strings.TrimSpace(r.Email))
		if r.ExpiresIn == 0 {
			r.ExpiresIn = api.ProductControlTeamInvitationDefaultSeconds
		}
		if r.TargetID != "" || !teamEmail.MatchString(r.Email) || len(r.Email) > api.ProductControlTeamEmailMaxBytes || !productValidTeamRole(r.Role) || r.ExpiresIn < api.ProductControlTeamInvitationMinSeconds || r.ExpiresIn > api.ProductControlTeamInvitationMaxSeconds {
			return r, state.ErrInvalidArgument
		}
	case "set_role":
		if !teamUUID(r.TargetID) || !productValidTeamRole(r.Role) || r.Email != "" || r.ExpiresIn != 0 {
			return r, state.ErrInvalidArgument
		}
	case "remove_member", "revoke_invitation":
		if !teamUUID(r.TargetID) || r.Role != "" || r.Email != "" || r.ExpiresIn != 0 {
			return r, state.ErrInvalidArgument
		}
	default:
		return r, state.ErrInvalidArgument
	}
	return r, nil
}

func productTeamPreview(c customerTeam, request api.ProductTeamActionRequest) (api.ProductTeamActionPreview, error) {
	var p api.ProductTeamActionPreview
	r, err := normalizeTeamRequest(request)
	if err != nil {
		return p, err
	}
	if c.customer.Suspended {
		return p, teamDenied()
	}
	p = api.ProductTeamActionPreview{Request: r, ActorID: c.live.ActorID, ActorRole: c.live.ActorRole, Revision: c.realm.catalog.Revision, AuthSnapshot: c.live.Snapshot, BeforeWorkspacePermissions: []string{}, AfterWorkspacePermissions: []string{}}
	switch r.Action {
	case "invite":
		if !c.canManageRole(r.Role) || !slices.ContainsFunc(c.live.Roles, func(role teamAuthRole) bool { return role.Name == r.Role }) {
			return p, teamDenied()
		}
		for _, m := range c.live.Members {
			if strings.EqualFold(m.Email, r.Email) {
				return p, teamAuthError(http.StatusConflict, []byte(`{"code":"already_invited_or_member"}`))
			}
		}
		for _, i := range c.live.Invitations {
			if strings.EqualFold(i.Email, r.Email) {
				return p, teamAuthError(http.StatusConflict, []byte(`{"code":"already_invited_or_member"}`))
			}
		}
		p.Subject, p.AfterRole = r.Email, r.Role
	case "set_role", "remove_member":
		var member *api.ProductTeamMember
		for _, m := range c.live.Members {
			if m.UserID == r.TargetID {
				member = &m
				break
			}
		}
		if member == nil {
			return p, state.ErrNotFound
		}
		if r.TargetID == c.live.ActorID || !c.canManageRole(member.Role) {
			return p, teamDenied()
		}
		p.Subject = member.Email
		if p.Subject == "" {
			p.Subject = member.UserID
		}
		p.BeforeRole = member.Role
		if r.Action == "set_role" {
			if !c.canManageRole(r.Role) || !slices.ContainsFunc(c.live.Roles, func(role teamAuthRole) bool { return role.Name == r.Role }) {
				return p, teamDenied()
			}
			if r.Role == member.Role {
				return p, state.ErrInvalidArgument
			}
			p.AfterRole = r.Role
		}
	case "revoke_invitation":
		var invite *api.ProductTeamInvitation
		for _, i := range c.live.Invitations {
			if i.ID == r.TargetID {
				invite = &i
				break
			}
		}
		if invite == nil {
			return p, state.ErrNotFound
		}
		if !c.canManageRole(invite.Role) {
			return p, teamDenied()
		}
		p.Subject, p.BeforeRole = invite.Email, invite.Role
	}
	p.BeforePermissions = productcontrol.EffectiveAccess(c.realm.catalog.Spec.Access, p.BeforeRole)
	p.AfterPermissions = productcontrol.EffectiveAccess(c.realm.catalog.Spec.Access, p.AfterRole)
	p.BeforeWorkspacePermissions = c.workspacePermissions(p.BeforeRole)
	p.AfterWorkspacePermissions = c.workspacePermissions(p.AfterRole)
	p.Hash = productTeamHash(struct {
		Account, App, Tenant, Realm string
		Customer                    api.ProductCustomer
		Preview                     api.ProductTeamActionPreview
	}{c.realm.account.ID, c.realm.app.ID, c.identity.TenantID, c.realm.authApp.ID, c.customer, p})
	return p, nil
}
func productTeamHash(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
