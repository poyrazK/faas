package agentconnections

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Why an agent may not act in an organization.
var (
	ErrNotAgentMember   = errors.New("this account's role in the organization can't connect agents")
	ErrAgentsNotAllowed = errors.New("the organization does not allow agents")
)

// PolicyFor returns the policy an agent of accountID acting in the shared
// organization orgID is held to, or why it may not act there: the
// organization is personal or being deleted, the account isn't a member
// whose role can see apps, or the policy doesn't allow agents.
func PolicyFor(ctx context.Context, store state.Store, orgID, accountID string) (state.OrgAgentPolicy, error) {
	org, err := store.OrgByID(ctx, orgID)
	if errors.Is(err, state.ErrNotFound) {
		return state.OrgAgentPolicy{}, ErrNotAgentMember
	}
	if err != nil {
		return state.OrgAgentPolicy{}, err
	}
	if org.Personal || org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
		return state.OrgAgentPolicy{}, ErrNotAgentMember
	}
	member, err := store.OrgAccessSnapshot(ctx, orgID, accountID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && !RoleMayConnect(member.Role)) {
		return state.OrgAgentPolicy{}, ErrNotAgentMember
	}
	if err != nil {
		return state.OrgAgentPolicy{}, err
	}
	policy := state.DefaultOrgAgentPolicy(orgID)
	if policies, ok := store.(state.OrgAgentStore); ok {
		if policy, err = policies.OrgAgentPolicy(ctx, orgID); err != nil {
			return state.OrgAgentPolicy{}, err
		}
	}
	if !policy.AgentsAllowed {
		return state.OrgAgentPolicy{}, ErrAgentsNotAllowed
	}
	return policy, nil
}

// Organizations' agent policies (ADR-20261101225122). A policy limits only
// the grantable hosted MCP scopes; apps:read and the scopes that come with
// one (postgres:read with postgres:manage) follow it.

// RoleMayConnect reports whether a member's role may connect an agent to the
// organization: every role that can see the organization's apps.
func RoleMayConnect(role state.OrgRole) bool {
	switch role {
	case state.OrgRoleOwner, state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleViewer:
		return true
	}
	return false
}

// ExceedsPolicy reports whether a key with these scopes may not act in an
// organization under its policy.
func ExceedsPolicy(policy state.OrgAgentPolicy, scopes []string) bool {
	if !policy.AgentsAllowed {
		return true
	}
	for _, scope := range scopes {
		if slices.Contains(api.MCPGrantableScopes, scope) && !slices.Contains(policy.AllowedScopes, scope) {
			return true
		}
	}
	return false
}

// CapScopes keeps the scopes of a hosted MCP sign-in that a policy allows.
// postgres:read comes with postgres:manage, so it goes when manage does.
func CapScopes(policy state.OrgAgentPolicy, scopes []string) []string {
	manage := slices.Contains(policy.AllowedScopes, api.ScopeManagedPostgresManage)
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if slices.Contains(api.MCPGrantableScopes, scope) && !slices.Contains(policy.AllowedScopes, scope) {
			continue
		}
		if scope == api.ScopeManagedPostgresRead && !manage {
			continue
		}
		out = append(out, scope)
	}
	return out
}

// ValidPolicyScopes reports whether every scope is a grantable hosted MCP
// scope, each listed once.
func ValidPolicyScopes(scopes []string) bool {
	for i, scope := range scopes {
		if !slices.Contains(api.MCPGrantableScopes, scope) || slices.Contains(scopes[:i], scope) {
			return false
		}
	}
	return true
}
