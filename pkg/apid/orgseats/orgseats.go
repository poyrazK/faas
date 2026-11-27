// adr: 20261127200000

// Package orgseats decides how an organization's member seats answer
// invitations and accepts: a plan with no seats (Free) refuses invitations and
// asks for an upgrade, and a full paid plan reports its live member count.
package orgseats

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the state the seat answers read.
type Store interface {
	OrgByID(ctx context.Context, id string) (state.Org, error)
	OrgInvitationByTokenHash(ctx context.Context, hash []byte) (state.OrgInvitation, error)
	CountActiveOrgMembers(ctx context.Context, orgID string) (int, error)
}

// HasSeats reports whether org's plan can seat members other than its owner
// and hold pending invitations. Free and unknown plans cannot.
func HasSeats(org state.Org) bool {
	return org.Plan.OrgMembersMax() > 0 && org.Plan.OrgPendingInvitationsMax() > 0
}

// NoSeatsProblem is the 403 an invitation on a plan without seats answers.
func NoSeatsProblem(org state.Org) *api.Problem {
	return api.ErrOrgPlanHasNoSeats(org.Slug, org.Plan)
}

// MemberCapProblem is the 403 an accept answers when the store refused it at
// the member cap. A plan with no seats asks for an upgrade rather than
// reporting "0 of 0"; a full paid plan reports its limit and the live
// active-member count, which a downgrade can leave above the limit. Lookups
// are best effort: the refusal stands either way.
func MemberCapProblem(ctx context.Context, store Store, tokenHash []byte) *api.Problem {
	inv, err := store.OrgInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return api.ErrOrgMemberCapExceeded(0, 0)
	}
	org, err := store.OrgByID(ctx, inv.OrgID)
	if err != nil {
		return api.ErrOrgMemberCapExceeded(0, 0)
	}
	limit := org.Plan.OrgMembersMax()
	if limit <= 0 {
		return api.ErrOrgSeatsUnavailable()
	}
	active, err := store.CountActiveOrgMembers(ctx, org.ID)
	if err != nil {
		active = limit
	}
	return api.ErrOrgMemberCapExceeded(limit, active)
}
