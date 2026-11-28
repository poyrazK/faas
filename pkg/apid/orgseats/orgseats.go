// adr: 20261127200000

// Package orgseats decides how an organization's member seats answer
// invitations and accepts: a plan with no seats (Free) refuses invitations and
// asks for an upgrade, and an accept on a full paid plan says no seat is free
// without disclosing the organization's headcount to the invitee.
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
// reporting "0 of 0"; a full paid plan says every seat is taken. The invitee
// is not a member yet, so neither answer discloses the organization's limit
// or headcount. Lookups are best effort: the refusal stands either way.
func MemberCapProblem(ctx context.Context, store Store, tokenHash []byte) *api.Problem {
	inv, err := store.OrgInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return api.ErrOrgSeatsFull()
	}
	org, err := store.OrgByID(ctx, inv.OrgID)
	if err != nil {
		return api.ErrOrgSeatsFull()
	}
	if org.Plan.OrgMembersMax() <= 0 {
		return api.ErrOrgSeatsUnavailable()
	}
	return api.ErrOrgSeatsFull()
}
