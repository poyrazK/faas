package authz

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// OrgResolver is the dependency LoadOrgWithResolver reads to resolve
// (org-by-slug, membership-by-account) per request. The concrete
// implementation lives in cmd/apid (wiring the pkg/state.Store); the
// tests use stubs (see loadorg_test.go).
//
// The interface deliberately exposes ONLY the two methods LoadOrg
// needs — a wider surface (e.g. ListOrgMembers, ListOrgInvitations)
// would leak state-package concerns into the authz vocabulary and
// tempt future handlers to bypass AuthorizeOrgAction.
type OrgResolver interface {
	// OrgBySlug returns the org identified by slug. The pkg/state
	// store returns state.ErrNotFound when the slug is unknown
	// (load-bearing for LoadOrgWithResolver's 404 vs 403 split).
	OrgBySlug(ctx context.Context, slug string) (state.Org, error)

	// OrgMemberByAccount returns the membership row for
	// (orgID, accountID). pkg/state returns state.ErrNotFound
	// when the account is not a member — LoadOrgWithResolver
	// translates that to 403 org_role_forbidden (IDOR-safe: a
	// non-member of an existing org gets the same response shape
	// as a non-member of a non-existent org, only the code
	// differs).
	OrgMemberByAccount(ctx context.Context, orgID, accountID string) (state.OrgMembership, error)
}

// StoreBackedResolver is the production OrgResolver implementation.
// It delegates OrgBySlug and OrgMemberByAccount to pkg/state.Store —
// the methods landed in PR 2 (issue #190 / IAM-6 / ADR-061).
//
// PR 4 ships this as a straight pass-through. PR 7 may add a small
// in-process cache (bounded LRU keyed by (slug, accountID)) when
// admission pressure makes the per-request SELECT hot.
type StoreBackedResolver struct {
	Store state.Store
}

// NewStoreBackedResolver returns a StoreBackedResolver wrapping the
// given store. Panics on nil store — there is no fallback path; the
// cmd/apid wiring MUST supply a real store.
func NewStoreBackedResolver(s state.Store) *StoreBackedResolver {
	if s == nil {
		panic("authz: NewStoreBackedResolver called with nil store")
	}
	return &StoreBackedResolver{Store: s}
}

// OrgBySlug delegates to the underlying store.
func (r *StoreBackedResolver) OrgBySlug(ctx context.Context, slug string) (state.Org, error) {
	return r.Store.OrgBySlug(ctx, slug)
}

// OrgMemberByAccount delegates to the underlying store.
func (r *StoreBackedResolver) OrgMemberByAccount(ctx context.Context, orgID, accountID string) (state.OrgMembership, error) {
	return r.Store.OrgMemberByAccount(ctx, orgID, accountID)
}

// OrgMFAPolicyResolver is the optional resolver method LoadOrg uses to refuse
// members without two-factor authentication on an organization that requires
// it with a clear 403 (ADR 20261113180000). The org-access snapshot enforces
// the same rule for every other organization path.
type OrgMFAPolicyResolver interface {
	OrgRequiresMFA(ctx context.Context, orgID string) (bool, error)
}

// OrgRequiresMFA reads the organization's security policy.
func (r *StoreBackedResolver) OrgRequiresMFA(ctx context.Context, orgID string) (bool, error) {
	store, ok := r.Store.(state.OrgSecurityPolicyStore)
	if !ok {
		return false, nil
	}
	policy, err := store.OrgSecurityPolicy(ctx, orgID)
	return policy.RequireMFA, err
}
