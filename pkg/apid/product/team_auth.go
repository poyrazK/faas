package product

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type productTeamError struct {
	status       int
	code, detail string
}

func (e productTeamError) Error() string { return e.code }
func teamUnavailable() error {
	return productTeamError{http.StatusServiceUnavailable, "product_team_unavailable", "Team management is unavailable. Contact your product team."}
}
func teamStale() error {
	return productTeamError{http.StatusConflict, "product_team_review_changed", "The workspace, roles or product policy changed. Load the team and review again."}
}
func teamDenied() error {
	return productTeamError{http.StatusForbidden, "product_team_denied", "Your role cannot manage this team member or assign this role."}
}

type teamAuthRole struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}
type teamAuthState struct {
	ActorID     string                      `json:"actor_id"`
	ActorRole   string                      `json:"actor_role"`
	Members     []api.ProductTeamMember     `json:"members"`
	Invitations []api.ProductTeamInvitation `json:"invitations"`
	Roles       []teamAuthRole              `json:"roles"`
	Snapshot    string                      `json:"snapshot"`
	Protocol    int                         `json:"protocol"`
}
type customerTeam struct {
	realm    customerRealm
	identity authmw.ProductCustomerIdentity
	customer api.ProductCustomer
	token    string
	live     teamAuthState
}

func teamUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
func teamCanonicalID(value string) string {
	id, err := uuid.Parse(value)
	if err != nil {
		return ""
	}
	return id.String()
}

var teamAuthPermission = regexp.MustCompile(`^(?:[a-z][a-z0-9_.:-]{0,63}|\*)$`)

func validateTeamState(v teamAuthState, identity authmw.ProductCustomerIdentity) error {
	if v.Protocol != 1 || !teamUUID(v.ActorID) || v.ActorRole != identity.Access.Role || !accessRevision(v.Snapshot) || v.Members == nil || v.Invitations == nil || v.Roles == nil || len(v.Members) > api.ProductControlTeamMaxMembers || len(v.Invitations) > api.ProductControlTeamMaxInvitations || len(v.Roles) > api.ProductControlTeamMaxAuthRoles {
		return teamUnavailable()
	}
	roles := map[string]bool{}
	for _, r := range v.Roles {
		if !productValidTeamRole(r.Name) || roles[r.Name] || r.Permissions == nil || len(r.Permissions) > api.ProductControlTeamMaxAuthPermissions {
			return teamUnavailable()
		}
		roles[r.Name] = true
		seen := map[string]bool{}
		for _, p := range r.Permissions {
			if len(p) > api.ProductControlTeamAuthPermissionMaxBytes || !teamAuthPermission.MatchString(p) || seen[p] {
				return teamUnavailable()
			}
			seen[p] = true
		}
	}
	seen := map[string]bool{}
	actorFound := false
	for _, m := range v.Members {
		if !teamUUID(m.UserID) || seen[m.UserID] || !roles[m.Role] || len(m.Email) > api.ProductControlTeamEmailMaxBytes || m.CreatedAt.IsZero() {
			return teamUnavailable()
		}
		seen[m.UserID] = true
		if m.UserID == v.ActorID {
			if m.Role != v.ActorRole {
				return teamUnavailable()
			}
			actorFound = true
		}
	}
	seen = map[string]bool{}
	for _, i := range v.Invitations {
		if !teamUUID(i.ID) || seen[i.ID] || !roles[i.Role] || i.Email == "" || len(i.Email) > api.ProductControlTeamEmailMaxBytes || i.CreatedAt.IsZero() || i.ExpiresAt.IsZero() {
			return teamUnavailable()
		}
		seen[i.ID] = true
	}
	if !actorFound {
		return teamUnavailable()
	}
	return nil
}

func (s *Service) loadCustomerTeam(r *http.Request, acct state.Account) (customerTeam, error) {
	var c customerTeam
	identity, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !identity.Access.Permissions.ManageTeam {
		return c, teamDenied()
	}
	realm, err := s.customerRealm(r)
	if err != nil {
		return c, err
	}
	if realm.account.ID != acct.ID || realm.authApp.Slug != identity.AuthApp || teamCanonicalID(realm.authApp.ID) != teamCanonicalID(identity.AuthAppID) || !productTeamPermission(realm.catalog, identity.Access.Role) {
		return c, teamDenied()
	}
	customer, err := realm.store.ProductCustomerByAuthOrganization(r.Context(), acct.ID, realm.app.ID, identity.OrganizationID)
	if err != nil {
		return c, err
	}
	if customer.TenantID != identity.TenantID {
		return c, teamDenied()
	}
	tenants, ok := s.Host.Store().(state.PlatformTenantStore)
	if !ok {
		return c, teamUnavailable()
	}
	tenant, err := tenants.GetPlatformTenant(r.Context(), acct.ID, customer.TenantID)
	if err != nil {
		return c, err
	}
	if tenant.Status != state.PlatformTenantActive {
		return c, teamDenied()
	}
	cookie, err := r.Cookie(realm.cookieName("access"))
	if err != nil {
		return c, customerAuthFailure{http.StatusUnauthorized}
	}
	c = customerTeam{realm: realm, identity: identity, customer: customer, token: cookie.Value}
	if err = s.teamAuthCall(r.Context(), c, http.MethodGet, c.path("/team"), nil, &c.live); err != nil {
		var failure productTeamError
		if errors.As(err, &failure) && failure.status == http.StatusNotFound {
			return c, teamUnavailable()
		}
		return c, err
	}
	return c, validateTeamState(c.live, identity)
}
func (c customerTeam) path(suffix string) string {
	return "/authz/v1/orgs/" + c.identity.OrganizationID + suffix
}

func (s *Service) teamAuthCall(ctx context.Context, c customerTeam, method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.ProductControlAuthTimeoutSeconds)*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, method, c.realm.origin+path, bytes.NewReader(raw))
	if err != nil {
		return teamUnavailable()
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: time.Duration(api.ProductControlAuthTimeoutSeconds) * time.Second}
	if s.AuthClient != nil {
		client = *s.AuthClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return teamUnavailable()
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, api.ProductControlTeamResponseMaxBytes+1))
	if err != nil || len(data) > api.ProductControlTeamResponseMaxBytes {
		return teamUnavailable()
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return teamAuthError(response.StatusCode, data)
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return teamUnavailable()
	}
	return nil
}

func teamAuthError(status int, raw []byte) error {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &body)
	switch status {
	case http.StatusUnauthorized:
		return customerAuthFailure{http.StatusUnauthorized}
	case http.StatusForbidden:
		return productTeamError{status, "product_team_auth_denied", "Your workspace role needs permission to read and manage team members."}
	case http.StatusNotFound:
		return productTeamError{status, "product_team_not_found", "The action or team member was not found. Refresh the team before continuing."}
	case http.StatusConflict:
		switch body.Code {
		case "last_owner":
			return productTeamError{status, "product_team_last_owner", "At least one workspace owner must remain."}
		case "already_invited_or_member":
			return productTeamError{status, "product_team_already_invited", "This address is already a member or has a pending invitation. Check the team before creating another invitation."}
		case "team_action_conflict":
			return productTeamError{status, "product_team_action_conflict", "This reference already belongs to another confirmed team action. Check its status."}
		case "team_limit":
			return productTeamError{status, "product_team_limit", "This workspace exceeds the internal team-management size limit. Contact your product team."}
		default:
			return teamStale()
		}
	case http.StatusBadRequest:
		return productTeamError{status, "product_team_invalid", "This team action or role is unavailable. Refresh the team and review again."}
	default:
		return teamUnavailable()
	}
}
